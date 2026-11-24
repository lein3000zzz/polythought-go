package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

// An admin's oversight of every user's workflows (D435).
func TestOversight(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	hold := &domain.Hold{By: root.ID, Reason: "3,000 runs an hour", At: at}

	serve := func(t *testing.T) (*httpapi.Server, *httpapimocks.MockWorkflowService, *httpapimocks.MockStatsService, *httpapimocks.MockHistoryService) {
		t.Helper()

		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, root)

		wf, st, hist := httpapimocks.NewMockWorkflowService(t), httpapimocks.NewMockStatsService(t), httpapimocks.NewMockHistoryService(t)
		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
			httpapi.Services{Auth: authSvc, Workflows: wf, Stats: st, History: hist})
		require.NoError(t, err)

		return srv, wf, st, hist
	}

	t.Run("every user's, the busiest first", func(t *testing.T) {
		srv, _, st, _ := serve(t)
		st.EXPECT().Workflows(mock.Anything, mock.Anything).Return([]stats.AdminWorkflow{{
			Owner: alice, ID: "wf-1", Name: "busy", Active: true, Triggers: []string{"trigger.schedule"},
			Runs: domain.RunStats{WorkflowID: "wf-1", Hour: domain.RunCounts{Runs: 3000, Failed: 2}, Day: domain.RunCounts{Runs: 9000}},
		}, {Owner: alice, ID: "wf-2", Name: "held", Hold: hold, Runs: domain.RunStats{WorkflowID: "wf-2"}}}, at, nil)

		w := request{method: "GET", path: "/api/v1/admin/workflows", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		zero := `{"runs":0,"failed":0}`
		assert.JSONEq(t, `{"workflows":[
			{"id":"wf-1","name":"busy","ownerId":"u-1","ownerEmail":"alice@example.com","active":true,"triggers":["trigger.schedule"],
			 "hour":{"runs":3000,"failed":2},"day":{"runs":9000,"failed":0},"week":`+zero+`},
			{"id":"wf-2","name":"held","ownerId":"u-1","ownerEmail":"alice@example.com","active":false,"triggers":[],
			 "hold":{"reason":"3,000 runs an hour","at":"2026-11-24T12:00:00Z"},"hour":`+zero+`,"day":`+zero+`,"week":`+zero+`}
		],"at":"2026-11-24T12:00:00Z"}`, w.Body.String())
	})

	t.Run("one read, its runs and history", func(t *testing.T) {
		srv, wf, _, hist := serve(t)
		wf.EXPECT().AdminGet(mock.Anything, mock.Anything, alice.ID, domain.WorkflowID("wf-1")).
			Return(domain.Workflow{ID: "wf-1", Name: "busy", Hold: hold}, nil)
		wf.EXPECT().AdminExecutions(mock.Anything, mock.Anything, alice.ID, domain.WorkflowID("wf-1"), ports.PageFilter{Limit: 5}).
			Return(ports.Page[domain.Execution]{Items: []domain.Execution{{ID: "ex-1", WorkflowID: "wf-1", Status: domain.ExecutionSucceeded}}}, nil)
		hist.EXPECT().AdminList(mock.Anything, mock.Anything, alice.ID, domain.WorkflowID("wf-1"), []domain.SwitchGroup{domain.GroupOnOff}, ports.PageFilter{}).
			Return(ports.Page[domain.Switch]{Items: []domain.Switch{{ID: "sw-1", Kind: domain.SwitchHold, Admin: true, Reason: "3,000 runs an hour", At: at}}}, nil)

		w := request{method: "GET", path: "/api/v1/admin/users/u-1/workflows/wf-1", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"hold":{"reason":"3,000 runs an hour","at":"2026-11-24T12:00:00Z"}`)

		w = request{method: "GET", path: "/api/v1/admin/users/u-1/workflows/wf-1/executions?limit=5", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"id":"ex-1"`)

		w = request{method: "GET", path: "/api/v1/admin/users/u-1/workflows/wf-1/history?kinds=on_off", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"switches":[{"id":"sw-1","kind":"hold","polythought":false,"admin":true,"reason":"3,000 runs an hour","at":"2026-11-24T12:00:00Z"}]}`, w.Body.String())
	})

	t.Run("switched off and held, then released", func(t *testing.T) {
		srv, wf, _, _ := serve(t)
		wf.EXPECT().AdminSwitchOff(mock.Anything, mock.Anything, alice.ID, domain.WorkflowID("wf-1"), "3,000 runs an hour", true).
			Return(domain.Workflow{ID: "wf-1", Hold: hold}, nil)
		wf.EXPECT().AdminRelease(mock.Anything, mock.Anything, alice.ID, domain.WorkflowID("wf-1")).Return(domain.Workflow{ID: "wf-1"}, nil)

		w := request{method: "POST", path: "/api/v1/admin/users/u-1/workflows/wf-1/switch-off", headers: cookie,
			body: `{"reason":"3,000 runs an hour","hold":true}`}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"hold":`)

		w = request{method: "POST", path: "/api/v1/admin/users/u-1/workflows/wf-1/release", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.NotContains(t, w.Body.String(), `"hold":`)
	})

	t.Run("held, its owner is told why switching on is refused", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, alice)

		wf := httpapimocks.NewMockWorkflowService(t)
		wf.EXPECT().Activate(mock.Anything, mock.Anything, domain.WorkflowID("wf-1")).
			Return(domain.Workflow{}, domain.ErrHeld)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Workflows: wf})
		require.NoError(t, err)

		w := request{method: "POST", path: "/api/v1/workflows/wf-1/activate", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"held"`)
	})

	t.Run("a token needs the admin scope", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, root, domain.ScopeRead)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
			httpapi.Services{Auth: authSvc, Workflows: httpapimocks.NewMockWorkflowService(t), Stats: httpapimocks.NewMockStatsService(t)})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/admin/workflows", headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	})
}
