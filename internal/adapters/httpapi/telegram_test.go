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
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestTelegramRoutes(t *testing.T) {
	update := `{"update_id":1}`

	tests := []struct {
		name       string
		req        request
		setup      func(s *httpapimocks.MockTelegramService, w *httpapimocks.MockTelegramWebhook)
		wantStatus int
		wantBody   []string
	}{
		{
			name: "status with a link",
			req:  request{method: "GET", path: "/api/v1/telegram", headers: cookie},
			setup: func(s *httpapimocks.MockTelegramService, _ *httpapimocks.MockTelegramWebhook) {
				s.EXPECT().Status(mock.Anything, mock.Anything).Return(telegram.Status{
					Available: true, BotUsername: "poly_bot",
					Link: &domain.TelegramLink{Username: "alice_tg", FirstName: "Alice", LinkedAt: created, TelegramUserID: 7, ChatID: 7},
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"available":true,"botUsername":"poly_bot","link":{"username":"alice_tg","firstName":"Alice","linkedAt":"2026-10-08T12:00:00Z"}}`},
		},
		{
			name: "status with a problem, for admins",
			req:  request{method: "GET", path: "/api/v1/telegram", headers: cookie},
			setup: func(s *httpapimocks.MockTelegramService, _ *httpapimocks.MockTelegramWebhook) {
				s.EXPECT().Status(mock.Anything, mock.Anything).Return(telegram.Status{Problem: "refused", ProblemAt: created}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`{"available":false,"problem":"refused","problemAt":"2026-10-08T12:00:00Z"}`},
		},
		{
			name: "a link",
			req:  request{method: "POST", path: "/api/v1/telegram/link", headers: cookie},
			setup: func(s *httpapimocks.MockTelegramService, _ *httpapimocks.MockTelegramWebhook) {
				s.EXPECT().CreateLink(mock.Anything, mock.Anything).Return(telegram.LinkURL{
					URL: "https://t.me/poly_bot?start=abc", ExpiresAt: created.Add(10 * time.Minute),
				}, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`{"url":"https://t.me/poly_bot?start=abc","expiresAt":"2026-10-08T12:10:00Z"}`},
		},
		{
			name: "no link without a bot",
			req:  request{method: "POST", path: "/api/v1/telegram/link", headers: cookie},
			setup: func(s *httpapimocks.MockTelegramService, _ *httpapimocks.MockTelegramWebhook) {
				s.EXPECT().CreateLink(mock.Anything, mock.Anything).Return(telegram.LinkURL{}, domain.ErrTelegramUnavailable)
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   []string{`"code":"telegram_unavailable"`},
		},
		{
			name: "unlink",
			req:  request{method: "DELETE", path: "/api/v1/telegram/link", headers: cookie},
			setup: func(s *httpapimocks.MockTelegramService, _ *httpapimocks.MockTelegramWebhook) {
				s.EXPECT().Unlink(mock.Anything, mock.Anything).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "the webhook needs no session, only Telegram's secret",
			req: request{
				method: "POST", path: "/api/v1/telegram/webhook", body: update,
				headers: map[string]string{"X-Telegram-Bot-Api-Secret-Token": "s3cret"},
			},
			setup: func(_ *httpapimocks.MockTelegramService, w *httpapimocks.MockTelegramWebhook) {
				w.EXPECT().HandleWebhook(mock.Anything, "s3cret", []byte(update)).Return(nil)
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "a wrong secret is not found",
			req:  request{method: "POST", path: "/api/v1/telegram/webhook", body: update},
			setup: func(_ *httpapimocks.MockTelegramService, w *httpapimocks.MockTelegramWebhook) {
				w.EXPECT().HandleWebhook(mock.Anything, "", []byte(update)).Return(domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			if tt.req.headers["Cookie"] != "" {
				withSession(authSvc, alice)
			}

			svc := httpapimocks.NewMockTelegramService(t)
			hook := httpapimocks.NewMockTelegramWebhook(t)
			tt.setup(svc, hook)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, Telegram: svc, TelegramWebhook: hook})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}
		})
	}
}
