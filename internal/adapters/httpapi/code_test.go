package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestCodeTypes(t *testing.T) {
	authSvc := httpapimocks.NewMockAuthService(t)
	withSession(authSvc, alice)

	types := httpapimocks.NewMockCodeTypes(t)
	types.EXPECT().Declarations(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })).
		Return([]ports.CodeFile{{Path: "index.d.ts", Content: `export * from "./sdk";`}}, nil)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, CodeTypes: types})
	require.NoError(t, err)

	w := request{method: "GET", path: "/api/v1/code/types", headers: cookie}.do(t, srv)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"files":[{"path":"index.d.ts","content":"export * from \"./sdk\";"}]}`, w.Body.String())

	w = request{method: "GET", path: "/api/v1/code/types"}.do(t, srv)
	assert.Equal(t, http.StatusUnauthorized, w.Code, "signed in only")
}
