package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

var waiting = domain.Confirmation{
	ID: "c-1", OwnerID: "u-1", Kind: domain.ConfirmRun, WorkflowID: "wf-1", GrantID: "g-1", Agent: "Claude Code",
	What: `Run "Spread watch" by hand, which trades live`, Status: domain.ConfirmPending,
	CreatedAt: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC),
}

func confirmationServer(t *testing.T) (*httpapi.Server, *httpapimocks.MockAuthService, *httpapimocks.MockConfirmationService) {
	t.Helper()

	authSvc, svc := httpapimocks.NewMockAuthService(t), httpapimocks.NewMockConfirmationService(t)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Confirmations: svc})
	require.NoError(t, err)

	return srv, authSvc, svc
}

// What waits for the user is theirs to see; confirming and refusing are theirs alone, at a
// browser (D406).
func TestConfirmationRoutes(t *testing.T) {
	t.Run("listed, for a token too", func(t *testing.T) {
		srv, authSvc, svc := confirmationServer(t)
		withToken(authSvc, alice)
		svc.EXPECT().Recent(mock.Anything, mock.Anything).Return([]domain.Confirmation{waiting}, nil)

		w := request{method: http.MethodGet, path: "/api/v1/me/confirmations", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"confirmations": [{
			"id": "c-1", "kind": "run", "workflowId": "wf-1", "asker": "Claude Code",
			"what": "Run \"Spread watch\" by hand, which trades live", "status": "pending",
			"createdAt": "2026-10-24T12:00:00Z", "expiresAt": "2026-10-24T13:00:00Z"
		}]}`, w.Body.String())
	})

	t.Run("confirmed at a browser", func(t *testing.T) {
		srv, authSvc, svc := confirmationServer(t)
		withSession(authSvc, alice)

		confirmed := waiting
		confirmed.Status = domain.ConfirmConfirmed
		svc.EXPECT().Confirm(mock.Anything, mock.Anything, domain.ConfirmationID("c-1")).Return(confirmed, nil)

		w := request{method: http.MethodPost, path: "/api/v1/me/confirmations/c-1/confirm", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"status":"confirmed"`)
	})

	t.Run("never by a token, nor its switches", func(t *testing.T) {
		for _, r := range []request{
			{method: http.MethodPost, path: "/api/v1/me/confirmations/c-1/confirm"},
			{method: http.MethodPost, path: "/api/v1/me/confirmations/c-1/refuse"},
			{method: http.MethodPut, path: "/api/v1/me/live-confirm", body: `{"run": false}`},
		} {
			srv, authSvc, _ := confirmationServer(t)
			withToken(authSvc, alice)

			r.headers = bearer
			w := r.do(t, srv)
			assert.Equal(t, http.StatusForbidden, w.Code, "%s %s, refused before the service", r.method, r.path)
		}
	})

	t.Run("its switches, those named changing", func(t *testing.T) {
		srv, authSvc, svc := confirmationServer(t)

		lets := alice
		lets.Unconfirmed.KillOff = true
		withSession(authSvc, lets)
		svc.EXPECT().SetUnconfirmed(mock.Anything, mock.Anything, domain.Unconfirmed{Run: true, KillOff: false}).Return(nil)

		w := request{method: http.MethodPut, path: "/api/v1/me/live-confirm", body: `{"run": false, "killOff": true}`, headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	})
}

// A live action that waits is not done: a token hears so, with the confirmation (D406).
func TestAwaitingConfirmation(t *testing.T) {
	authSvc, svc := httpapimocks.NewMockAuthService(t), httpapimocks.NewMockWorkflowService(t)
	withToken(authSvc, alice)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, Workflows: svc, NodeTypes: httpapimocks.NewMockNodeTypes(t)})
	require.NoError(t, err)

	svc.EXPECT().Run(mock.Anything, mock.Anything, domain.WorkflowID("wf-1"), domain.NodeID("")).
		Return(domain.Execution{}, &domain.AwaitingError{Confirmation: waiting})

	w := request{method: http.MethodPost, path: "/api/v1/workflows/wf-1/run", headers: bearer}.do(t, srv)
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())

	var body struct {
		Error struct{ Code, Message string }
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "awaits_confirmation", body.Error.Code)
	assert.Contains(t, body.Error.Message, "confirmation c-1")
}
