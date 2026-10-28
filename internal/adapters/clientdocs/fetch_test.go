package clientdocs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/clientdocs"
	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const doc = `{"client_id": "x", "redirect_uris": ["http://127.0.0.1/cb"]}`

func server(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/client.json", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Polythought-OAuth/1", r.Header.Get("User-Agent"))
		assert.Equal(t, "application/json", r.Header.Get("Accept"))
		_, _ = w.Write([]byte(doc))
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/client.json", http.StatusFound)
	})
	mux.HandleFunc("/large", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("a", 16<<10+1)))
	})

	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

// fetcher trusts the test server's certificate, private addresses allowed or not.
func fetcher(srv *httptest.Server, private bool) *clientdocs.Fetcher {
	return clientdocs.New(func() bool { return private }, nil, srv.Client().Transport.(*http.Transport).TLSClientConfig)
}

func TestFetch(t *testing.T) {
	ctx := context.Background()
	srv := server(t)
	f := fetcher(srv, true)

	body, err := f.Fetch(ctx, srv.URL+"/client.json")
	require.NoError(t, err)
	assert.JSONEq(t, doc, string(body))

	_, err = f.Fetch(ctx, srv.URL+"/moved")
	require.ErrorIs(t, err, domain.ErrUnavailable, "the document is at its ID: no redirect")

	_, err = f.Fetch(ctx, srv.URL+"/none")
	require.ErrorIs(t, err, domain.ErrUnavailable)

	_, err = f.Fetch(ctx, srv.URL+"/large")
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	for _, u := range []string{strings.Replace(srv.URL, "https://", "http://", 1) + "/client.json", "https:///client.json", "client.json"} {
		_, err = f.Fetch(ctx, u)
		require.ErrorIs(t, err, domain.ErrInvalidInput, u)
	}
}

// The server's own network is refused unless an admin allowed private addresses.
func TestFetchRefusesInternal(t *testing.T) {
	srv := server(t)

	_, err := fetcher(srv, false).Fetch(context.Background(), srv.URL+"/client.json")
	require.ErrorIs(t, err, domain.ErrUnavailable)
	assert.Contains(t, err.Error(), webhook.ErrForbiddenAddress.Error(), "refused by the guard")
}
