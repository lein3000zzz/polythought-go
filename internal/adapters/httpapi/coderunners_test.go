package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestCodeRunners(t *testing.T) {
	runners := func() ports.CodeRunners {
		return ports.CodeRunners{
			InRunners: true, Size: 8, Running: 7, Why: "only Linux has the sandbox", Restarts: 1, LastError: "it ran past its job's time",
		}
	}

	for _, tt := range []struct {
		name   string
		user   domain.User
		status func() ports.CodeRunners
		code   int
		body   string
	}{
		{
			name: "in runner processes, not sandboxed (D357)", user: root, status: runners, code: http.StatusOK,
			body: `{"inRunners":true,"size":8,"running":7,"sandboxed":false,"why":"only Linux has the sandbox","restarts":1,"lastError":"it ran past its job's time","failed":""}`,
		},
		{
			name: "in the server's process", user: root, code: http.StatusOK,
			body: `{"inRunners":false,"size":0,"running":0,"sandboxed":false,"why":"","restarts":0,"lastError":"","failed":""}`,
		},
		{name: "admins only", user: alice, status: runners, code: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, tt.user)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
				httpapi.Services{Auth: authSvc, CodeRunners: tt.status})
			require.NoError(t, err)

			w := request{method: "GET", path: "/api/v1/admin/code-runners", headers: cookie}.do(t, srv)
			assert.Equal(t, tt.code, w.Code, w.Body.String())

			if tt.body != "" {
				assert.JSONEq(t, tt.body, w.Body.String())
			}
		})
	}
}
