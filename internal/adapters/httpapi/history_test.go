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
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestWorkflowHistory(t *testing.T) {
	at := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	t.Run("who switched it and why, some kinds alone, a page at a time", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, alice)

		hist := httpapimocks.NewMockHistoryService(t)
		hist.EXPECT().List(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID }), domain.WorkflowID("wf-1"),
			[]domain.SwitchGroup{domain.GroupOnOff, domain.GroupTrading}, ports.PageFilter{Limit: 2, Cursor: "c-1"}).
			Return(ports.Page[domain.Switch]{Items: []domain.Switch{
				{ID: "sw-2", Kind: domain.SwitchLive, By: domain.Actor{GrantID: "g-1", Name: "Claude Code", Confirmed: true},
					Reason: "the user asked", WalletID: "w-1", WalletAddress: "0x5f3C00000000000000000000000000000000A1b2", At: at},
				{ID: "sw-1", Kind: domain.SwitchOff, Polythought: true, Reason: "the plugin weather is disabled", At: at.Add(-time.Hour)},
			}, NextCursor: "sw-1"}, nil)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, History: hist})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/workflows/wf-1/history?kinds=on_off,trading&limit=2&cursor=c-1", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"switches":[
			{"id":"sw-2","kind":"live","by":{"kind":"agent","id":"g-1","name":"Claude Code","confirmed":true},"polythought":false,
			 "reason":"the user asked","wallet":{"id":"w-1","address":"0x5f3C00000000000000000000000000000000A1b2"},"at":"2026-10-29T12:00:00Z"},
			{"id":"sw-1","kind":"off","polythought":true,"reason":"the plugin weather is disabled","at":"2026-10-29T11:00:00Z"}
		],"nextCursor":"sw-1"}`, w.Body.String())
	})

	t.Run("a token says why it switches", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, alice, domain.ScopeBuild)

		wfSvc := httpapimocks.NewMockWorkflowService(t)
		wfSvc.EXPECT().SwitchOff(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.Reason == "the drawdown is too deep" }),
			domain.WorkflowID("wf-1"), false).Return(domain.Workflow{ID: "wf-1"}, nil)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Workflows: wfSvc})
		require.NoError(t, err)

		w := request{method: "POST", path: "/api/v1/workflows/wf-1/deactivate?reason=the+drawdown+is+too+deep", headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})
}
