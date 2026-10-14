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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestSettingsRoutes(t *testing.T) {
	overview := settings.Overview{
		RestartPending: true,
		Entries: []settings.Entry{
			{
				Spec: settings.Spec{
					Key: "auth.session_idle_ttl", Group: "Accounts", Label: "Idle sign-out", Description: "Signs out.",
					Kind: settings.KindDuration, Default: 168 * time.Hour, Apply: settings.ApplyLive,
				},
				Value: 24 * time.Hour, Source: settings.SourceDatabase,
			},
			{
				Spec: settings.Spec{
					Key: "auth.registration", Label: "Registration", Kind: settings.KindEnum, Default: "invite",
					Options: []string{"open", "invite"}, Apply: settings.ApplyLive, Env: "POLYTHOUGHT_REGISTRATION",
				},
				Value: "open", Source: settings.SourceEnvironment,
			},
			{
				Spec:  settings.Spec{Key: "telegram.token", Label: "Bot token", Kind: settings.KindString, Secret: true, Default: "", Apply: settings.ApplyRestart},
				IsSet: true, Source: settings.SourceDatabase, Pending: true,
			},
			{
				Spec:  settings.Spec{Key: "webhook.allow_private", Label: "Private", Kind: settings.KindBool, Default: false, Apply: settings.ApplyLive},
				Value: false, Source: settings.SourceDefault,
			},
		},
	}

	tests := []struct {
		name       string
		req        request
		setup      func(s *httpapimocks.MockSettingsService)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "list types each value and hides secrets",
			req:  request{method: "GET", path: "/api/v1/admin/settings", headers: cookie},
			setup: func(s *httpapimocks.MockSettingsService) {
				s.EXPECT().List(mock.Anything, mock.Anything).Return(overview, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"restartPending":true`,
				`{"key":"auth.session_idle_ttl","group":"Accounts","label":"Idle sign-out","description":"Signs out.","kind":"duration","secret":false,"isSet":false,"apply":"live","value":"24h","default":"168h","source":"database","env":"POLYTHOUGHT_AUTH_SESSION_IDLE_TTL","pending":false}`,
				`"options":["open","invite"]`, `"source":"environment","env":"POLYTHOUGHT_REGISTRATION"`,
				`"key":"telegram.token","group":"","label":"Bot token","description":"","kind":"string","secret":true,"isSet":true,"apply":"restart","value":null,"default":null`,
				`"value":false,"default":false,"source":"default"`,
			},
		},
		{
			name: "list is admin only",
			req:  request{method: "GET", path: "/api/v1/admin/settings", headers: cookie},
			setup: func(s *httpapimocks.MockSettingsService) {
				s.EXPECT().List(mock.Anything, mock.Anything).Return(settings.Overview{}, domain.ErrForbidden)
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name: "update passes each value through as sent and answers with the new list",
			req: request{
				method: "PATCH", path: "/api/v1/admin/settings", headers: cookie,
				body: `{"values":{"auth.registration":"open","workflows.max_nodes":50,"limits.ratio":0.10,"log.level":null,"http.trusted_proxies":["10.0.0.0/8"]}}`,
			},
			setup: func(s *httpapimocks.MockSettingsService) {
				s.EXPECT().Update(mock.Anything, mock.Anything, map[string]json.RawMessage{
					"auth.registration":    json.RawMessage(`"open"`),
					"workflows.max_nodes":  json.RawMessage(`50`),
					"limits.ratio":         json.RawMessage(`0.10`),
					"log.level":            json.RawMessage(`null`),
					"http.trusted_proxies": json.RawMessage(`["10.0.0.0/8"]`),
				}).Return(nil)
				s.EXPECT().List(mock.Anything, mock.Anything).Return(overview, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"restartPending":true`},
		},
		{
			name: "invalid changes name their settings",
			req:  request{method: "PATCH", path: "/api/v1/admin/settings", headers: cookie, body: `{"values":{"workflows.max_nodes":0}}`},
			setup: func(s *httpapimocks.MockSettingsService) {
				v := domain.ValidationError{Base: domain.ErrInvalidInput}
				v.Add(domain.Problem{Param: "workflows.max_nodes", Message: "must be between 2 and 10000"})
				s.EXPECT().Update(mock.Anything, mock.Anything, mock.Anything).Return(v.Err())
			},
			wantStatus: http.StatusBadRequest,
			wantBody: []string{
				`"code":"invalid_input"`, `"message":"invalid input: must be between 2 and 10000"`,
				`"problems":[{"param":"workflows.max_nodes","message":"must be between 2 and 10000"}]`,
			},
		},
		{
			name:       "unknown fields are rejected",
			req:        request{method: "PATCH", path: "/api/v1/admin/settings", headers: cookie, body: `{"value":{}}`},
			setup:      func(*httpapimocks.MockSettingsService) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "history pages through changes",
			req:  request{method: "GET", path: "/api/v1/admin/settings/history?limit=2&cursor=c-1", headers: cookie},
			setup: func(s *httpapimocks.MockSettingsService) {
				s.EXPECT().History(mock.Anything, mock.Anything, ports.PageFilter{Limit: 2, Cursor: "c-1"}).Return(ports.Page[ports.SettingAudit]{
					Items: []ports.SettingAudit{
						{ID: "a-2", Key: "telegram.token", Secret: true, At: created},
						{ID: "a-1", Key: "auth.registration", OldValue: json.RawMessage(`"invite"`), NewValue: json.RawMessage(`"open"`), ActorEmail: "root@example.com", At: created},
					},
					NextCursor: "a-1",
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`{"id":"a-2","key":"telegram.token","oldValue":null,"newValue":null,"secret":true,"at":"2026-10-08T12:00:00Z"}`,
				`{"id":"a-1","key":"auth.registration","oldValue":"invite","newValue":"open","secret":false,"actorEmail":"root@example.com","at":"2026-10-08T12:00:00Z"}`,
				`"nextCursor":"a-1"`,
			},
		},
		{
			name: "restart is accepted",
			req:  request{method: "POST", path: "/api/v1/admin/restart", headers: cookie},
			setup: func(s *httpapimocks.MockSettingsService) {
				s.EXPECT().Restart(mock.Anything, mock.Anything).Return(nil)
			},
			wantStatus: http.StatusAccepted,
		},
		{
			name: "restart is admin only",
			req:  request{method: "POST", path: "/api/v1/admin/restart", headers: cookie},
			setup: func(s *httpapimocks.MockSettingsService) {
				s.EXPECT().Restart(mock.Anything, mock.Anything).Return(domain.ErrForbidden)
			},
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			svc := httpapimocks.NewMockSettingsService(t)
			tt.setup(svc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, Settings: svc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}
}
