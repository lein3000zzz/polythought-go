package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublic(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "8.8.8.8", want: true},
		{addr: "2606:4700:4700::1111", want: true},
		{addr: "127.0.0.1"},
		{addr: "::1"},
		{addr: "10.1.2.3"},
		{addr: "172.18.0.2"}, // a Docker network: the API and the database live here
		{addr: "192.168.1.10"},
		{addr: "169.254.169.254"}, // cloud metadata services
		{addr: "100.100.1.1"},
		{addr: "0.0.0.0"},
		{addr: "0.1.2.3"},
		{addr: "fc00::1"},
		{addr: "fe80::1"},
		{addr: "224.0.0.1"},
		{addr: "::ffff:127.0.0.1"},
		{addr: "::ffff:8.8.8.8", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			assert.Equal(t, tt.want, public(netip.MustParseAddr(tt.addr)))
		})
	}
}

func newSender(t *testing.T, allowPrivate bool) *Sender {
	t.Helper()

	s, err := New(observability.NoopUtility(), Config{AllowPrivate: func() bool { return allowPrivate }})
	require.NoError(t, err)

	return s
}

func TestPost(t *testing.T) {
	var got struct {
		body        string
		contentType string
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.body, got.contentType = string(b), r.Header.Get("Content-Type")

		if strings.HasSuffix(r.URL.Path, "/gone") {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	body, _ := json.Marshal(map[string]string{"content": "hi"})

	t.Run("internal addresses are refused by default", func(t *testing.T) {
		err := newSender(t, false).Post(ctx, srv.URL+"/hook", body, nil)
		require.ErrorIs(t, err, ErrForbiddenAddress)
	})

	t.Run("allowed when the operator says so", func(t *testing.T) {
		require.NoError(t, newSender(t, true).Post(ctx, srv.URL+"/hook", body, nil))
		assert.JSONEq(t, `{"content":"hi"}`, got.body)
		assert.Equal(t, "application/json", got.contentType)
	})

	t.Run("an error status fails, without the secret part of the URL", func(t *testing.T) {
		err := newSender(t, true).Post(ctx, srv.URL+"/s3cr3t-token/gone", body, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "answered 404")
		assert.NotContains(t, err.Error(), "s3cr3t")
	})

	t.Run("only web addresses", func(t *testing.T) {
		for _, u := range []string{"ftp://example.com/x", "file:///etc/passwd", "not a url", "https://"} {
			assert.ErrorIs(t, newSender(t, true).Post(ctx, u, body, nil), ErrInvalidURL, u)
		}
	})

	t.Run("extra headers go along, but not in place of the sender's own", func(t *testing.T) {
		var header http.Header

		hook := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { header = r.Header }))
		t.Cleanup(hook.Close)

		require.NoError(t, newSender(t, true).Post(ctx, hook.URL, body, map[string]string{"x-api-key": "k-1"}))
		assert.Equal(t, "k-1", header.Get("X-Api-Key"))
		assert.Equal(t, "application/json", header.Get("Content-Type"))

		err := newSender(t, true).Post(ctx, hook.URL, body, map[string]string{"content-type": "text/plain"})
		require.ErrorIs(t, err, ErrReservedHeader)
	})
}

func TestPostRedirects(t *testing.T) {
	ctx := context.Background()
	extra := map[string]string{"Authorization": "Bearer t-1", "X-Api-Key": "k-1"}

	var seen []http.Header

	record := func(w http.ResponseWriter, r *http.Request) { seen = append(seen, r.Header.Clone()) }

	elsewhere := httptest.NewServer(http.HandlerFunc(record))
	t.Cleanup(elsewhere.Close)

	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
		case "/away":
			http.Redirect(w, r, elsewhere.URL+"/final", http.StatusTemporaryRedirect)
		default:
			record(w, r)
		}
	}))
	t.Cleanup(hook.Close)

	t.Run("to the same host, the headers follow", func(t *testing.T) {
		seen = nil
		require.NoError(t, newSender(t, true).Post(ctx, hook.URL+"/same", []byte(`{}`), extra))
		require.Len(t, seen, 1)
		assert.Equal(t, "Bearer t-1", seen[0].Get("Authorization"))
		assert.Equal(t, "k-1", seen[0].Get("X-Api-Key"))
	})

	t.Run("to another host, they stay behind", func(t *testing.T) {
		seen = nil
		require.NoError(t, newSender(t, true).Post(ctx, hook.URL+"/away", []byte(`{}`), extra))
		require.Len(t, seen, 1)
		assert.Empty(t, seen[0].Get("Authorization"))
		assert.Empty(t, seen[0].Get("X-Api-Key"))
		assert.Equal(t, "application/json", seen[0].Get("Content-Type"))
	})
}
