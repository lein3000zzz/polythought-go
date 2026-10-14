package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestNotificationRoutes(t *testing.T) {
	failed := notify.Pref{Kind: notify.Kind{Key: "workflow_failed", Label: "A workflow fails", Description: "When it fails."}, Enabled: true}

	tests := []struct {
		name       string
		req        request
		setup      func(s *httpapimocks.MockNotificationService)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "list",
			req:  request{method: "GET", path: "/api/v1/notifications", headers: cookie},
			setup: func(s *httpapimocks.MockNotificationService) {
				s.EXPECT().Prefs(mock.Anything, mock.Anything).Return([]notify.Pref{failed}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"notifications":[{"key":"workflow_failed","label":"A workflow fails","description":"When it fails.","enabled":true}]}`},
		},
		{
			name: "switch one off",
			req:  request{method: "PUT", path: "/api/v1/notifications/workflow_failed", headers: cookie, body: `{"enabled":false}`},
			setup: func(s *httpapimocks.MockNotificationService) {
				off := failed
				off.Enabled = false
				s.EXPECT().SetPref(mock.Anything, mock.Anything, "workflow_failed", false).Return(off, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"enabled":false`},
		},
		{
			name: "an unknown kind",
			req:  request{method: "PUT", path: "/api/v1/notifications/gossip", headers: cookie, body: `{"enabled":true}`},
			setup: func(s *httpapimocks.MockNotificationService) {
				s.EXPECT().SetPref(mock.Anything, mock.Anything, "gossip", true).Return(notify.Pref{}, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			svc := httpapimocks.NewMockNotificationService(t)
			tt.setup(svc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, Notifications: svc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}
}
