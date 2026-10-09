package httpapi_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
)

var pathParam = regexp.MustCompile(`:(\w+)`)

// TestSpecMatchesRoutes keeps api/openapi.json honest: every route under /api/v1 is
// documented, and every documented operation is served. After changing either, run
// make api-spec.
func TestSpecMatchesRoutes(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi.json"))
	require.NoError(t, err)

	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(raw, &spec))

	var documented []string

	for path, ops := range spec.Paths {
		for method := range ops {
			documented = append(documented, strings.ToUpper(method)+" "+path)
		}
	}

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{})
	require.NoError(t, err)

	var served []string

	for _, r := range srv.Engine().Routes() {
		if path, ok := strings.CutPrefix(r.Path, "/api/v1"); ok {
			served = append(served, r.Method+" "+pathParam.ReplaceAllString(path, "{$1}"))
		}
	}

	require.NotEmpty(t, served)
	assert.ElementsMatch(t, served, documented)
}
