package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func newRequester(t *testing.T, allowPrivate bool) *Requester {
	t.Helper()

	r, err := NewRequester(observability.NoopUtility(), Config{AllowPrivate: func() bool { return allowPrivate }})
	require.NoError(t, err)

	return r
}

func TestRequester(t *testing.T) {
	type seen struct {
		method, path, query, body string
		header                    http.Header
	}

	var got seen

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(b), header: r.Header.Clone()}

		switch r.URL.Path {
		case "/missing":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"no such market"}`))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", MaxResponse+1)))
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		default:
			w.Header().Add("X-Rate", "1")
			w.Header().Add("X-Rate", "2")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(srv.Close)

	ctx := context.Background()

	t.Run("internal addresses are refused by default", func(t *testing.T) {
		_, err := newRequester(t, false).Do(ctx, ports.HTTPRequest{Method: "GET", URL: srv.URL})
		require.ErrorIs(t, err, ErrForbiddenAddress)
	})

	t.Run("a request and its answer, whatever the status", func(t *testing.T) {
		res, err := newRequester(t, true).Do(ctx, ports.HTTPRequest{
			Method: "PATCH", URL: srv.URL + "/notes?a=1", Body: []byte(`{"q":1}`),
			Header: map[string]string{"content-type": "application/json", "X-Api-Key": "k-1"},
		})
		require.NoError(t, err)

		assert.Equal(t, 200, res.Status)
		assert.JSONEq(t, `{"ok":true}`, string(res.Body))
		assert.Equal(t, []string{"1", "2"}, res.Header["X-Rate"])

		assert.Equal(t, seen{method: "PATCH", path: "/notes", query: "a=1", body: `{"q":1}`, header: got.header}, got)
		assert.Equal(t, "application/json", got.header.Get("Content-Type"))
		assert.Equal(t, "k-1", got.header.Get("X-Api-Key"))
		assert.Equal(t, "Polythought-HTTP/1", got.header.Get("User-Agent"))

		res, err = newRequester(t, true).Do(ctx, ports.HTTPRequest{Method: "GET", URL: srv.URL + "/missing"})
		require.NoError(t, err, "a 404 is an answer")
		assert.Equal(t, 404, res.Status)
		assert.JSONEq(t, `{"error":"no such market"}`, string(res.Body))
	})

	t.Run("its own User-Agent, if it gives one", func(t *testing.T) {
		_, err := newRequester(t, true).Do(ctx, ports.HTTPRequest{Method: "GET", URL: srv.URL, Header: map[string]string{"User-Agent": "me@example.com"}})
		require.NoError(t, err)
		assert.Equal(t, "me@example.com", got.header.Get("User-Agent"))
	})

	t.Run("what it refuses", func(t *testing.T) {
		r := newRequester(t, true)

		for _, tc := range []struct {
			name string
			req  ports.HTTPRequest
			want error
		}{
			{name: "a method of its own", req: ports.HTTPRequest{Method: "TRACE", URL: srv.URL}, want: ErrMethod},
			{name: "not a web address", req: ports.HTTPRequest{Method: "GET", URL: "file:///etc/passwd"}, want: ErrInvalidURL},
			{name: "a header the client sets", req: ports.HTTPRequest{Method: "GET", URL: srv.URL, Header: map[string]string{"host": "a"}}, want: ErrReservedHeader},
			{name: "a line break in a value", req: ports.HTTPRequest{Method: "GET", URL: srv.URL, Header: map[string]string{"X-A": "1\r\nX-B: 2"}}, want: ErrBadHeader},
			{name: "a space in a name", req: ports.HTTPRequest{Method: "GET", URL: srv.URL, Header: map[string]string{"X A": "1"}}, want: ErrBadHeader},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := r.Do(ctx, tc.req)
				assert.ErrorIs(t, err, tc.want)
			})
		}
	})

	t.Run("a response over 1 MiB fails", func(t *testing.T) {
		_, err := newRequester(t, true).Do(ctx, ports.HTTPRequest{Method: "GET", URL: srv.URL + "/big"})
		require.ErrorIs(t, err, ErrTooLarge)
	})

	t.Run("no answer in time, the URL's secrets left out", func(t *testing.T) {
		_, err := newRequester(t, true).Do(ctx, ports.HTTPRequest{
			Method: "GET", URL: srv.URL + "/slow?apikey=s3cr3t", Timeout: 50 * time.Millisecond,
		})
		require.ErrorIs(t, err, ErrNoAnswer)
		assert.Contains(t, err.Error(), "GET 127.0.0.1")
		assert.NotContains(t, err.Error(), "s3cr3t")
	})
}

func TestRequesterRedirects(t *testing.T) {
	ctx := context.Background()
	header := map[string]string{"Authorization": "Bearer t-1", "X-Api-Key": "k-1", "Accept": "application/json"}

	var seen []http.Header

	record := func(_ http.ResponseWriter, r *http.Request) { seen = append(seen, r.Header.Clone()) }

	elsewhere := httptest.NewServer(http.HandlerFunc(record))
	t.Cleanup(elsewhere.Close)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/away":
			http.Redirect(w, r, elsewhere.URL+"/final", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		default:
			record(w, r)
		}
	}))
	t.Cleanup(api.Close)

	r := newRequester(t, true)

	t.Run("to the same host, the headers follow", func(t *testing.T) {
		seen = nil
		_, err := r.Do(ctx, ports.HTTPRequest{Method: "GET", URL: api.URL + "/same", Header: header})
		require.NoError(t, err)
		require.Len(t, seen, 1)
		assert.Equal(t, "Bearer t-1", seen[0].Get("Authorization"))
		assert.Equal(t, "k-1", seen[0].Get("X-Api-Key"))
	})

	t.Run("to another host, all but the harmless stay behind", func(t *testing.T) {
		seen = nil
		_, err := r.Do(ctx, ports.HTTPRequest{Method: "GET", URL: api.URL + "/away", Header: header})
		require.NoError(t, err)
		require.Len(t, seen, 1)
		assert.Empty(t, seen[0].Get("Authorization"))
		assert.Empty(t, seen[0].Get("X-Api-Key"))
		assert.Equal(t, "application/json", seen[0].Get("Accept"))
	})

	t.Run("at most three", func(t *testing.T) {
		_, err := r.Do(ctx, ports.HTTPRequest{Method: "GET", URL: api.URL + "/loop"})
		require.ErrorContains(t, err, "more than 3 redirects")
	})
}
