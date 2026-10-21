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
	"github.com/lein3000zzz/polythought-go/internal/core/services/spend"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestSpendRoutes(t *testing.T) {
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	dollars, total, mine := decimal.RequireFromString("20"), int64(20_000_000), int64(1_000)

	month := spend.Spend{
		Usage: domain.AgentUsage{
			Month: october, InputTokens: 1_200, OutputTokens: 300, Dollars: decimal.RequireFromString("4.2"), Calls: 7, CostedCalls: 2,
		},
		Caps:   domain.AgentCaps{Dollars: &dollars, TotalTokens: &total, InputTokens: &mine},
		Own:    domain.AgentCaps{InputTokens: &mine},
		Resets: october.AddDate(0, 1, 0),
	}

	tests := []struct {
		name       string
		user       domain.User
		req        request
		setup      func(s *httpapimocks.MockSpendService)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "your spend this month, and the caps on it",
			user: alice,
			req:  request{method: "GET", path: "/api/v1/me/agent-spend", headers: cookie},
			setup: func(s *httpapimocks.MockSpendService) {
				s.EXPECT().Spend(mock.Anything, alice.ID).Return(month, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"month":"2026-10-01T00:00:00Z","resets":"2026-11-01T00:00:00Z"`,
				`"inputTokens":1200,"outputTokens":300,"totalTokens":1500,"dollars":"4.20","calls":7,"costedCalls":2`,
				`"caps":{"dollars":"20.00","inputTokens":1000,"totalTokens":20000000}`, `"own":{"inputTokens":1000}`,
				`"killStopsAgents":false`,
			},
		},
		{
			name: "your choice that your kill switch stops your agents too (D276)",
			user: alice,
			req:  request{method: "PUT", path: "/api/v1/me/agents", body: `{"killStopsAgents":true}`, headers: cookie},
			setup: func(s *httpapimocks.MockSpendService) {
				chosen := month
				chosen.KillStopsAgents = true

				s.EXPECT().SetKillStopsAgents(mock.Anything, alice.ID, true).Return(nil).Once()
				s.EXPECT().Spend(mock.Anything, alice.ID).Return(chosen, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"killStopsAgents":true`},
		},
		{
			name:       "a choice in no shape",
			user:       alice,
			req:        request{method: "PUT", path: "/api/v1/me/agents", body: `{"stop":true}`, headers: cookie},
			setup:      func(*httpapimocks.MockSpendService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "an admin sees a user's",
			user: root,
			req:  request{method: "GET", path: "/api/v1/admin/users/u-1/agent-spend", headers: cookie},
			setup: func(s *httpapimocks.MockSpendService) {
				s.EXPECT().Spend(mock.Anything, domain.UserID("u-1")).Return(month, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"dollars":"4.20"`},
		},
		{
			name:       "nobody else does",
			user:       alice,
			req:        request{method: "GET", path: "/api/v1/admin/users/u-0/agent-spend", headers: cookie},
			setup:      func(*httpapimocks.MockSpendService) {},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, tt.user)

			svc := httpapimocks.NewMockSpendService(t)
			tt.setup(svc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Spend: svc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)
			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}

	t.Run("not there without agents", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, alice)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/me/agent-spend", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
