package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestWorkflowsStats(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	o := stats.Overview{
		Workflows: []stats.Workflow{
			{
				ID: "wf-1", Name: "copy Alice", Active: true,
				Trading: &trading.Result{
					Mode: domain.ModePaper, Profit: decimal.RequireFromString("12.4"), Realized: decimal.RequireFromString("10"),
					Unrealized: decimal.RequireFromString("2.4"), Positions: 3, Volume: decimal.RequireFromString("90"), Orders: 7, Wins: 2, Closed: 4,
				},
				Runs: domain.RunStats{
					WorkflowID: "wf-1", Day: domain.RunCounts{Runs: 4, Failed: 1}, Week: domain.RunCounts{Runs: 30, Failed: 2},
					Last: &domain.LastRun{ID: "ex-9", Status: domain.ExecutionFailed, StartedAt: at},
				},
			},
			{ID: "wf-2", Name: "alert", Runs: domain.RunStats{WorkflowID: "wf-2"}},
		},
		Traders: []trading.TraderResult{{
			Mode: domain.ModePaper, Trader: "0x00000000000000000000000000000000000000a1", Label: "Alice", Workflows: 2,
			Profit: decimal.RequireFromString("3"), Realized: decimal.RequireFromString("2"), Unrealized: decimal.RequireFromString("1"),
			Positions: 1, Volume: decimal.RequireFromString("40"), Orders: 4, Wins: 1, Closed: 2,
		}},
		Paper: stats.Totals{Workflows: 1, Profit: decimal.RequireFromString("12.4")},
		Day:   domain.RunCounts{Runs: 4, Failed: 1}, Week: domain.RunCounts{Runs: 30, Failed: 2}, At: at,
	}

	t.Run("each workflow, and the totals apart", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, alice)

		svc := httpapimocks.NewMockStatsService(t)
		svc.EXPECT().Overview(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })).Return(o, nil)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Stats: svc})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/workflows/stats", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		zero := `{"workflows":0,"profit":"0","realized":"0","unrealized":"0","positions":0,"unpriced":0,"volume":"0","orders":0,"wins":0,"closed":0}`
		assert.JSONEq(t, `{
			"workflows": [
				{"id":"wf-1","name":"copy Alice","active":true,
				 "trading":{"mode":"paper","profit":"12.4","realized":"10","unrealized":"2.4","positions":3,"unpriced":0,"volume":"90","orders":7,"wins":2,"closed":4},
				 "runs":{"day":{"runs":4,"failed":1},"week":{"runs":30,"failed":2},"last":{"id":"ex-9","status":"failed","startedAt":"2026-10-28T12:00:00Z"}}},
				{"id":"wf-2","name":"alert","active":false,"runs":{"day":{"runs":0,"failed":0},"week":{"runs":0,"failed":0}}}
			],
			"traders":[{"mode":"paper","trader":"0x00000000000000000000000000000000000000a1","label":"Alice","workflows":2,
				"profit":"3","realized":"2","unrealized":"1","positions":1,"volume":"40","orders":4,"wins":1,"closed":2}],
			"paper":{"workflows":1,"profit":"12.4","realized":"0","unrealized":"0","positions":0,"unpriced":0,"volume":"0","orders":0,"wins":0,"closed":0},
			"live":`+zero+`,
			"day":{"runs":4,"failed":1},"week":{"runs":30,"failed":2},"at":"2026-10-28T12:00:00Z"
		}`, w.Body.String())
	})

	t.Run("a token with read may ask, and stats is no workflow's ID", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, alice, domain.ScopeRead)

		svc := httpapimocks.NewMockStatsService(t)
		svc.EXPECT().Overview(mock.Anything, mock.Anything).Return(stats.Overview{}, nil)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Stats: svc})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/workflows/stats", headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})
}
