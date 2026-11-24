package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/mlmodels"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestMLModels(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	temps := domain.Model{ID: "m-1", OwnerID: alice.ID, Name: "temps", Kind: domain.ModelWorker, LastAnswerAt: at, CreatedAt: at, UpdatedAt: at}
	view := mlmodels.View{Model: temps, Workers: []mlmodels.Connected{{Name: "gpu-1", Version: "1.2.0", Since: at, Seen: at, Holding: 1}}}
	workerKey := map[string]string{"Authorization": "Bearer pw_secret"}

	server := func(t *testing.T, authSvc *httpapimocks.MockAuthService, ml *httpapimocks.MockMLModelService) *httpapi.Server {
		t.Helper()

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, MLModels: ml})
		require.NoError(t, err)

		return srv
	}

	withScopes := func(t *testing.T, scopes ...domain.Scope) *httpapimocks.MockAuthService {
		t.Helper()

		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, alice, scopes...)

		return authSvc
	}

	t.Run("made, then listed with its workers connected and the limits", func(t *testing.T) {
		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().Create(mock.Anything, alice.ID, mlmodels.Spec{Name: "temps", Kind: domain.ModelWorker}).Return(mlmodels.View{Model: temps}, nil).Once()
		ml.EXPECT().List(mock.Anything, alice.ID).Return([]mlmodels.View{view}, nil).Once()
		ml.EXPECT().UsedBy(mock.Anything, alice.ID).Return(map[string][]domain.Workflow{
			"temps": {{ID: "wf-1", Name: "Weather bets", Active: true}},
		}, nil).Once()
		ml.EXPECT().Limits().Return(mlmodels.Limits{
			Models: 20, Workers: 10, Items: 1000, RequestBytes: 1 << 20, AnswerBytes: 1 << 20, MaxWait: 5 * time.Minute, Poll: 50 * time.Second,
		}).Once()

		srv := server(t, withScopes(t, domain.ScopeData), ml)

		w := request{method: "POST", path: "/api/v1/models", body: `{"name": "temps", "kind": "worker"}`, headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

		w = request{method: "GET", path: "/api/v1/models", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"models": [{"name": "temps", "kind": "worker", "lastAnswerAt": "2026-11-24T12:00:00Z",
			"workers": [{"name": "gpu-1", "version": "1.2.0", "since": "2026-11-24T12:00:00Z", "seen": "2026-11-24T12:00:00Z", "holding": 1}],
			"createdAt": "2026-11-24T12:00:00Z", "updatedAt": "2026-11-24T12:00:00Z",
			"workflows": [{"id": "wf-1", "name": "Weather bets", "active": true}]}],
			"limits": {"models": 20, "workers": 10, "items": 1000, "requestBytes": 1048576, "answerBytes": 1048576,
			"maxWaitSeconds": 300, "pollSeconds": 50}}`, w.Body.String())
	})

	t.Run("asked, its answers exact, those off the schema said", func(t *testing.T) {
		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().Ask(mock.Anything, alice.ID, "temps", mock.MatchedBy(func(items []json.RawMessage) bool {
			return len(items) == 2 && string(items[0]) == `{"city": "NYC"}`
		}), mock.Anything, 10*time.Second).Return(mlmodels.Answer{
			Answers: []json.RawMessage{json.RawMessage(`{"high":51.25}`), json.RawMessage(`"warm"`)},
			Version: "1.2.0", Worker: "gpu-1", Refused: map[int]error{1: errors.New("the answer does not keep to the schema")},
		}, nil).Once()

		w := request{
			method: "POST", path: "/api/v1/models/temps/ask", headers: bearer,
			body: `{"items": [{"city": "NYC"}, {"city": "LDN"}], "schema": {"type": "object"}, "waitSeconds": 10}`,
		}.do(t, server(t, withScopes(t, domain.ScopeData), ml))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"answers": [{"high": 51.25}, "warm"], "version": "1.2.0", "worker": "gpu-1",
			"refused": {"1": "the answer does not keep to the schema"}}`, w.Body.String())
	})

	t.Run("why asking fails", func(t *testing.T) {
		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().Ask(mock.Anything, alice.ID, "temps", mock.Anything, mock.Anything, mlmodels.DefaultWait).Return(mlmodels.Answer{}, domain.ErrModelOffline).Once()
		ml.EXPECT().Ask(mock.Anything, alice.ID, "slow", mock.Anything, mock.Anything, mlmodels.DefaultWait).Return(mlmodels.Answer{}, domain.ErrModelTimeout).Once()

		srv := server(t, withScopes(t, domain.ScopeData), ml)

		w := request{method: "POST", path: "/api/v1/models/temps/ask", body: `{"items": [{}]}`, headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"model_offline"`)

		w = request{method: "POST", path: "/api/v1/models/slow/ask", body: `{"items": [{}]}`, headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusGatewayTimeout, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"model_timeout"`)
	})

	t.Run("a worker key is made at a browser, its secret shown once; a token may not", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, alice)

		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().CreateKey(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.SessionID == "s-1" }), "temps", "gpu box").
			Return("pw_secret", domain.WorkerKey{ID: "k-1", Name: "gpu box", Prefix: "pw_secret", CreatedAt: at}, nil).Once()

		w := request{method: "POST", path: "/api/v1/models/temps/keys", body: `{"name": "gpu box"}`, headers: cookie}.do(t, server(t, authSvc, ml))
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.JSONEq(t, `{"key": {"id": "k-1", "name": "gpu box", "prefix": "pw_secret", "createdAt": "2026-11-24T12:00:00Z"},
			"secret": "pw_secret"}`, w.Body.String())

		w = request{method: "POST", path: "/api/v1/models/temps/keys", body: `{"name": "x"}`, headers: bearer}.
			do(t, server(t, withScopes(t, domain.ScopeData), httpapimocks.NewMockMLModelService(t)))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("a read token reads them but writes, asks and makes none", func(t *testing.T) {
		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().Get(mock.Anything, alice.ID, "temps").Return(view, nil).Once()
		ml.EXPECT().UsedBy(mock.Anything, alice.ID).Return(nil, nil).Once()

		srv := server(t, withScopes(t, domain.ScopeRead, domain.ScopeBuild), ml)

		w := request{method: "GET", path: "/api/v1/models/temps", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		for _, r := range []request{
			{method: "POST", path: "/api/v1/models", body: `{"name": "x", "kind": "worker"}`, headers: bearer},
			{method: "POST", path: "/api/v1/models/temps/ask", body: `{"items": [{}]}`, headers: bearer},
			{method: "DELETE", path: "/api/v1/models/temps", headers: bearer},
		} {
			w := r.do(t, srv)
			assert.Equal(t, http.StatusForbidden, w.Code, r.method+" "+r.path)
		}
	})

	t.Run("a worker, with its model's key alone, takes the next request and answers it", func(t *testing.T) {
		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().Worker(mock.Anything, "pw_secret", mock.Anything).Return(temps, nil).Times(3)
		ml.EXPECT().Take(mock.Anything, temps, mlmodels.Worker{Name: "gpu-1", Version: "1.2.0"}).Return(mlmodels.Request{
			ID: "r-1", Items: []json.RawMessage{json.RawMessage(`{"city":"NYC","price":0.53}`)}, Deadline: at,
		}, true, nil).Once()
		ml.EXPECT().Take(mock.Anything, temps, mlmodels.Worker{Name: "gpu-1", Version: "1.2.0"}).Return(mlmodels.Request{}, false, nil).Once()
		ml.EXPECT().Answer(mock.Anything, temps, "r-1", mlmodels.Worker{Name: "gpu-1", Version: "1.2.0"}, mock.MatchedBy(func(a []json.RawMessage) bool {
			return len(a) == 1 && string(a[0]) == `{"high": 51.25}`
		})).Return(nil).Once()

		srv := server(t, httpapimocks.NewMockAuthService(t), ml)
		take := `{"worker": "gpu-1", "version": "1.2.0"}`

		w := request{method: "POST", path: "/api/v1/worker/next", body: take, headers: workerKey}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"model": "temps", "request": {"id": "r-1", "items": [{"city": "NYC", "price": 0.53}],
			"deadline": "2026-11-24T12:00:00Z"}}`, w.Body.String())

		w = request{method: "POST", path: "/api/v1/worker/next", body: take, headers: workerKey}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, "none came: call again")

		w = request{
			method: "POST", path: "/api/v1/worker/requests/r-1/answer", headers: workerKey,
			body: `{"worker": "gpu-1", "version": "1.2.0", "answers": [{"high": 51.25}]}`,
		}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	})

	t.Run("a worker route takes no token nor session, and refuses a wrong key", func(t *testing.T) {
		ml := httpapimocks.NewMockMLModelService(t)
		ml.EXPECT().Worker(mock.Anything, "pt_secret", mock.Anything).Return(domain.Model{}, domain.ErrUnauthenticated).Once()

		srv := server(t, httpapimocks.NewMockAuthService(t), ml)

		w := request{method: "POST", path: "/api/v1/worker/next", body: `{"worker": "x"}`, headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusUnauthorized, w.Code)

		w = request{method: "POST", path: "/api/v1/worker/next", body: `{"worker": "x"}`, headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusUnauthorized, w.Code, "a session is no worker")
	})
}
