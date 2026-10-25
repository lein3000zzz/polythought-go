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
	"github.com/lein3000zzz/polythought-go/internal/core/services/updates"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestUpdateRoutes(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	newer := updates.Status{
		Current: "0.0.1", On: true, Newer: true, At: at, Updater: true,
		Latest: &domain.Release{Version: "0.0.2", URL: "https://github.com/o/r/releases/tag/v0.0.2", PublishedAt: at},
		Update: &domain.UpdaterStatus{State: domain.UpdaterIdle},
	}

	tests := []struct {
		name       string
		req        request
		setup      func(u *httpapimocks.MockUpdateService)
		wantStatus int
		wantBody   string
	}{
		{
			name: "a newer release, and an updater",
			req:  request{method: "GET", path: "/api/v1/admin/updates", headers: cookie},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().Status(mock.Anything, mock.Anything).Return(newer, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: `{"current":"0.0.1","on":true,"prereleases":false,"latest":{"version":"0.0.2","prerelease":false,` +
				`"url":"https://github.com/o/r/releases/tag/v0.0.2","publishedAt":"2026-10-25T09:00:00Z"},"newer":true,` +
				`"checkedAt":"2026-10-25T09:00:00Z","updater":true,"update":{"state":"idle"}}`,
		},
		{
			name: "off",
			req:  request{method: "GET", path: "/api/v1/admin/updates", headers: cookie},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().Status(mock.Anything, mock.Anything).Return(updates.Status{Current: "dev"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"current":"dev","on":false,"prereleases":false,"newer":false,"updater":false}`,
		},
		{
			name: "checked now",
			req:  request{method: "POST", path: "/api/v1/admin/updates/check", headers: cookie},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().CheckNow(mock.Anything, mock.Anything).Return(updates.Status{Current: "0.0.1", On: true, Error: "GitHub shows no release of Polythought yet"}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"current":"0.0.1","on":true,"prereleases":false,"newer":false,"error":"GitHub shows no release of Polythought yet","updater":false}`,
		},
		{
			name: "updating",
			req:  request{method: "POST", path: "/api/v1/admin/updates/apply", headers: cookie, body: `{"version":"0.0.2"}`},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().Apply(mock.Anything, mock.Anything, "0.0.2").Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "no updater",
			req:  request{method: "POST", path: "/api/v1/admin/updates/apply", headers: cookie, body: `{"version":"0.0.2"}`},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().Apply(mock.Anything, mock.Anything, "0.0.2").Return(domain.ErrNoUpdater)
			},
			wantStatus: http.StatusConflict,
			wantBody:   `{"error":{"code":"no_updater","message":"no updater is set up: see Updates in the install docs"}}`,
		},
		{
			name: "under way",
			req:  request{method: "POST", path: "/api/v1/admin/updates/apply", headers: cookie, body: `{"version":"0.0.2"}`},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().Apply(mock.Anything, mock.Anything, "0.0.2").Return(domain.ErrUpdating)
			},
			wantStatus: http.StatusConflict,
			wantBody:   `{"error":{"code":"updating","message":"an update is under way already"}}`,
		},
		{
			name:       "a strict body",
			req:        request{method: "POST", path: "/api/v1/admin/updates/apply", headers: cookie, body: `{"version":"0.0.2","force":true}`},
			setup:      func(*httpapimocks.MockUpdateService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "admins only",
			req:  request{method: "GET", path: "/api/v1/admin/updates", headers: cookie},
			setup: func(u *httpapimocks.MockUpdateService) {
				u.EXPECT().Status(mock.Anything, mock.Anything).Return(updates.Status{}, domain.ErrForbidden)
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, root)

			u := httpapimocks.NewMockUpdateService(t)
			tt.setup(u)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Updates: u})
			require.NoError(t, err)

			w := tt.req.do(t, srv)
			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			if tt.wantBody != "" {
				assert.JSONEq(t, tt.wantBody, w.Body.String())
			}
		})
	}

	t.Run("no such pages without the service", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, root)

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc})
		require.NoError(t, err)

		w := request{method: "GET", path: "/api/v1/admin/updates", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
