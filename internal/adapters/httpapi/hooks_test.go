package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/hooks"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestHooks(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	address := hooks.Address{URL: publicURL + "/hooks/s3cr3t", CreatedAt: at, Enabled: true, Listening: true}

	server := func(t *testing.T, authSvc *httpapimocks.MockAuthService, svc *httpapimocks.MockHookService) *httpapi.Server {
		t.Helper()

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Hooks: svc})
		require.NoError(t, err)

		return srv
	}

	t.Run("a call starts a run, answered 202 with its ID, from any origin, no session asked", func(t *testing.T) {
		svc := httpapimocks.NewMockHookService(t)
		svc.EXPECT().Call(mock.Anything, "s3cr3t", mock.Anything, []byte(`{"price":0.53}`), map[string][]string{"city": {"nyc"}, "tag": {"a", "b"}}).
			Return("ex-1", nil).Once()

		// The auth service is asked nothing: a hook's secret is its only proof.
		w := request{
			method: "POST", path: "/hooks/s3cr3t?city=nyc&tag=a&tag=b", body: `{"price":0.53}`,
			headers: map[string]string{"Origin": "https://alerts.example", "Cookie": "__Host-polythought_session=sess-token"},
		}.do(t, server(t, httpapimocks.NewMockAuthService(t), svc))

		require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
		assert.JSONEq(t, `{"executionId":"ex-1"}`, w.Body.String())
	})

	t.Run("why a call is refused", func(t *testing.T) {
		for _, tc := range []struct {
			err  error
			want int
			code string
		}{
			{err: domain.ErrNotFound, want: http.StatusNotFound, code: "not_found"},
			{err: domain.ErrHooksOff, want: http.StatusServiceUnavailable, code: "hooks_off"},
			{err: domain.ErrHookIdle, want: http.StatusConflict, code: "hook_idle"},
			{err: fmt.Errorf("%w: this hook takes no more calls this minute", domain.ErrRateLimited), want: http.StatusTooManyRequests, code: "rate_limited"},
			{err: fmt.Errorf("%w: the body is neither JSON nor text", domain.ErrInvalidInput), want: http.StatusBadRequest, code: "invalid_input"},
		} {
			t.Run(tc.code, func(t *testing.T) {
				svc := httpapimocks.NewMockHookService(t)
				svc.EXPECT().Call(mock.Anything, "s3cr3t", mock.Anything, mock.Anything, mock.Anything).Return("", tc.err).Once()

				w := request{method: "POST", path: "/hooks/s3cr3t", body: `{}`}.do(t, server(t, httpapimocks.NewMockAuthService(t), svc))
				assert.Equal(t, tc.want, w.Code, w.Body.String())
				assert.Contains(t, w.Body.String(), `"code":"`+tc.code+`"`)
			})
		}
	})

	t.Run("only POST", func(t *testing.T) {
		w := request{method: "GET", path: "/hooks/s3cr3t"}.do(t, server(t, httpapimocks.NewMockAuthService(t), httpapimocks.NewMockHookService(t)))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("its owner reads the address and renews it, at a browser", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		withSession(authSvc, alice)

		svc := httpapimocks.NewMockHookService(t)
		svc.EXPECT().Address(mock.Anything, alice.ID, domain.WorkflowID("wf-1"), domain.NodeID("hook")).Return(address, nil).Once()

		renewed := address
		renewed.URL = publicURL + "/hooks/n3w"
		svc.EXPECT().Renew(mock.Anything, alice.ID, domain.WorkflowID("wf-1"), domain.NodeID("hook")).Return(renewed, nil).Once()

		srv := server(t, authSvc, svc)

		w := request{method: "GET", path: "/api/v1/workflows/wf-1/hooks/hook", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"url":"`+publicURL+`/hooks/s3cr3t","createdAt":"2026-11-24T12:00:00Z","enabled":true,"listening":true}`, w.Body.String())

		w = request{method: "POST", path: "/api/v1/workflows/wf-1/hooks/hook/renew", headers: cookie}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `/hooks/n3w"`)
	})

	t.Run("no token reads it, whatever its scopes", func(t *testing.T) {
		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, alice, domain.ScopeRead, domain.ScopeBuild, domain.ScopeSettings)

		w := request{method: "GET", path: "/api/v1/workflows/wf-1/hooks/hook", headers: bearer}.do(t, server(t, authSvc, httpapimocks.NewMockHookService(t)))
		assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	})
}
