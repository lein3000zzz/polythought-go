package plugindir_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/plugindir"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestFetch(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/weather.tar.gz":
			_, _ = w.Write([]byte("the archive"))
		case "/big.tar.gz":
			w.Header().Set("Content-Length", "1000")
			_, _ = w.Write([]byte(strings.Repeat("x", 1000)))
		case "/clear":
			http.Redirect(w, r, "http://"+r.Host+"/weather.tar.gz", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tlsConfig := &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}

	private := plugindir.NewFetcher(func() bool { return true }, nil, tlsConfig)
	ctx := context.Background()

	t.Run("an archive over https", func(t *testing.T) {
		body, err := private.Fetch(ctx, srv.URL+"/weather.tar.gz", 100)
		require.NoError(t, err)

		raw, err := io.ReadAll(body)
		require.NoError(t, err)
		require.NoError(t, body.Close())
		assert.Equal(t, "the archive", string(raw))
	})

	t.Run("refused otherwise", func(t *testing.T) {
		for name, at := range map[string]string{
			"plain http":            "http://" + strings.TrimPrefix(srv.URL, "https://") + "/weather.tar.gz",
			"not there":             srv.URL + "/none",
			"larger than it may be": srv.URL + "/big.tar.gz",
			"redirected to http":    srv.URL + "/clear",
			"no URL":                "weather.tar.gz",
		} {
			_, err := private.Fetch(ctx, at, 100)
			assert.ErrorIs(t, err, domain.ErrInvalidInput, name)
		}

		guarded := plugindir.NewFetcher(func() bool { return false }, nil, tlsConfig)
		_, err := guarded.Fetch(ctx, srv.URL+"/weather.tar.gz", 100)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "address inside the server's network", "never inside, unless an admin allows it")
	})
}
