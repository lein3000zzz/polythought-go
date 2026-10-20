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
	"github.com/lein3000zzz/polythought-go/internal/core/services/geoblock"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestGeoblockRoutes(t *testing.T) {
	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	blocked := geoblock.Status{Last: &domain.Geoblock{Blocked: true, IP: "203.0.113.42", Country: "DE", Region: "HE", CheckedAt: at}, At: at}

	tests := []struct {
		name       string
		req        request
		setup      func(g *httpapimocks.MockGeoblockService)
		wantStatus int
		wantBody   string
	}{
		{
			name: "not checked yet",
			req:  request{method: "GET", path: "/api/v1/admin/geoblock", headers: cookie},
			setup: func(g *httpapimocks.MockGeoblockService) {
				g.EXPECT().Status(mock.Anything, mock.Anything).Return(geoblock.Status{}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"checked":false,"blocked":false}`,
		},
		{
			name: "blocked",
			req:  request{method: "GET", path: "/api/v1/admin/geoblock", headers: cookie},
			setup: func(g *httpapimocks.MockGeoblockService) {
				g.EXPECT().Status(mock.Anything, mock.Anything).Return(blocked, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"checked":true,"blocked":true,"ip":"203.0.113.42","country":"DE","region":"HE","checkedAt":"2026-10-20T09:00:00Z"}`,
		},
		{
			name: "checked now, failing",
			req:  request{method: "POST", path: "/api/v1/admin/geoblock/check", headers: cookie},
			setup: func(g *httpapimocks.MockGeoblockService) {
				failed := blocked
				failed.Error = "upstream service unavailable"
				g.EXPECT().CheckNow(mock.Anything, mock.Anything).Return(failed, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `"error":"upstream service unavailable"`,
		},
		{
			name: "admins only",
			req:  request{method: "GET", path: "/api/v1/admin/geoblock", headers: cookie},
			setup: func(g *httpapimocks.MockGeoblockService) {
				g.EXPECT().Status(mock.Anything, mock.Anything).Return(geoblock.Status{}, domain.ErrForbidden)
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			g := httpapimocks.NewMockGeoblockService(t)
			tt.setup(g)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Geoblock: g})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), tt.wantBody)
		})
	}
}
