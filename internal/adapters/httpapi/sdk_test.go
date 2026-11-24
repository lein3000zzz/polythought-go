package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
	"github.com/lein3000zzz/polythought-go/sdk"
)

// A server built from the code has no wheel: its index says so. The image's build puts one
// in, which the e2e tests fetch.
func TestPythonSDK(t *testing.T) {
	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: httpapimocks.NewMockAuthService(t)})
	require.NoError(t, err)

	name, _, ok := sdk.PythonWheel()
	if ok {
		w := request{method: "GET", path: "/api/v1/sdk/python/"}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `<a href="`+name+`">`)

		w = request{method: "GET", path: "/api/v1/sdk/python/" + name}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code)

		return
	}

	w := request{method: "GET", path: "/api/v1/sdk/python/"}.do(t, srv)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "built without the Python SDK")

	w = request{method: "GET", path: "/api/v1/sdk/python/polythought-1.0.0-py3-none-any.whl"}.do(t, srv)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
