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

	s, err := New(observability.NoopUtility(), Config{AllowPrivate: allowPrivate})
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
		err := newSender(t, false).Post(ctx, srv.URL+"/hook", body)
		require.ErrorIs(t, err, ErrForbiddenAddress)
	})

	t.Run("allowed when the operator says so", func(t *testing.T) {
		require.NoError(t, newSender(t, true).Post(ctx, srv.URL+"/hook", body))
		assert.JSONEq(t, `{"content":"hi"}`, got.body)
		assert.Equal(t, "application/json", got.contentType)
	})

	t.Run("an error status fails, without the secret part of the URL", func(t *testing.T) {
		err := newSender(t, true).Post(ctx, srv.URL+"/s3cr3t-token/gone", body)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "answered 404")
		assert.NotContains(t, err.Error(), "s3cr3t")
	})

	t.Run("only web addresses", func(t *testing.T) {
		for _, u := range []string{"ftp://example.com/x", "file:///etc/passwd", "not a url", "https://"} {
			assert.ErrorIs(t, newSender(t, true).Post(ctx, u, body), ErrInvalidURL, u)
		}
	})
}
