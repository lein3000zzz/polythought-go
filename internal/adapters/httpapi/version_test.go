package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/buildinfo"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestServerVersion(t *testing.T) {
	build := buildinfo.Info{Version: "0.1.2", Commit: "0123456789abcdef0123456789abcdef01234567", Go: "go1.27.3"}

	for _, tt := range []struct {
		name string
		user domain.User
		code int
		body string
	}{
		{
			name: "the build, to an admin", user: root, code: http.StatusOK,
			body: `{"version":"0.1.2","commit":"0123456789abcdef0123456789abcdef01234567","modified":false,"go":"go1.27.3"}`,
		},
		{name: "admins only", user: alice, code: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, tt.user)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL, Build: build}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc})
			require.NoError(t, err)

			w := request{method: "GET", path: "/api/v1/admin/version", headers: cookie}.do(t, srv)
			assert.Equal(t, tt.code, w.Code, w.Body.String())

			if tt.body != "" {
				assert.JSONEq(t, tt.body, w.Body.String())
			}
		})
	}
}
