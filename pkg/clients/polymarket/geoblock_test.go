package polymarket

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeoblock(t *testing.T) {
	var path string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = w.Write([]byte(`{"blocked":true,"ip":"203.0.113.42","country":"DE","region":"HE"}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewGeoblockClient(observability.NoopUtility(), HTTPConfig{BaseURL: srv.URL})
	require.NoError(t, err)

	got, err := c.Geoblock(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Geoblock{Blocked: true, IP: "203.0.113.42", Country: "DE", Region: "HE"}, got)
	assert.Equal(t, "/api/geoblock", path)
}
