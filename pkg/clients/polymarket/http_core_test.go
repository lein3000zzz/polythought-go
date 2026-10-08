package polymarket

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type reply struct {
	status     int
	retryAfter string
	body       string
}

// scriptedServer answers with replies in order, repeating the last one.
func scriptedServer(t *testing.T, replies []reply, onRequest func(*http.Request, string)) (string, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(hits.Add(1)) - 1
		rep := replies[min(n, len(replies)-1)]

		if onRequest != nil {
			body, _ := io.ReadAll(r.Body)
			onRequest(r, string(body))
		}

		if rep.retryAfter != "" {
			w.Header().Set("Retry-After", rep.retryAfter)
		}

		w.WriteHeader(rep.status)
		_, _ = w.Write([]byte(rep.body))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, &hits
}

func TestCoreDo(t *testing.T) {
	ok := reply{status: http.StatusOK, body: `{"ok":true}`}

	tests := []struct {
		name      string
		replies   []reply
		cfg       HTTPConfig
		method    string
		body      any
		onRequest func(t *testing.T, r *http.Request, body string)
		wantHits  int32
		check     func(t *testing.T, err error, out map[string]any)
	}{
		{
			name:     "success",
			replies:  []reply{ok},
			wantHits: 1,
			check: func(t *testing.T, err error, out map[string]any) {
				require.NoError(t, err)
				assert.Equal(t, true, out["ok"])
			},
		},
		{
			name: "retries 429 then succeeds",
			replies: []reply{
				{status: http.StatusTooManyRequests, retryAfter: "0"},
				{status: http.StatusTooManyRequests, retryAfter: "0"},
				ok,
			},
			wantHits: 3,
			check: func(t *testing.T, err error, out map[string]any) {
				require.NoError(t, err)
				assert.Equal(t, true, out["ok"])
			},
		},
		{
			name: "retries 425 and gateway errors",
			replies: []reply{
				{status: http.StatusTooEarly, retryAfter: "0"},
				{status: http.StatusBadGateway, retryAfter: "0"},
				{status: http.StatusGatewayTimeout, retryAfter: "0"},
				ok,
			},
			wantHits: 4,
			check: func(t *testing.T, err error, _ map[string]any) {
				require.NoError(t, err)
			},
		},
		{
			name:     "gives up after max retries",
			replies:  []reply{{status: http.StatusServiceUnavailable, retryAfter: "0", body: "down"}},
			cfg:      HTTPConfig{MaxRetries: 2},
			wantHits: 3,
			check: func(t *testing.T, err error, _ map[string]any) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusServiceUnavailable, apiErr.StatusCode)
				assert.Equal(t, "down", apiErr.Body)
			},
		},
		{
			name:     "retries disabled",
			replies:  []reply{{status: http.StatusTooManyRequests}},
			cfg:      HTTPConfig{MaxRetries: -1},
			wantHits: 1,
			check: func(t *testing.T, err error, _ map[string]any) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
			},
		},
		{
			name:     "client errors are not retried",
			replies:  []reply{{status: http.StatusBadRequest, body: `{"error":"bad"}`}},
			wantHits: 1,
			check: func(t *testing.T, err error, _ map[string]any) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.False(t, succeeded(err))
			},
		},
		{
			name:     "not found is an expected error",
			replies:  []reply{{status: http.StatusNotFound}},
			wantHits: 1,
			check: func(t *testing.T, err error, _ map[string]any) {
				require.ErrorIs(t, err, ErrNotFound)

				var expected *tracing.ExpectedError
				require.ErrorAs(t, err, &expected)
				assert.True(t, succeeded(err))
			},
		},
		{
			name:     "error body is truncated",
			replies:  []reply{{status: http.StatusInternalServerError, body: strings.Repeat("x", 2000)}},
			cfg:      HTTPConfig{MaxRetries: -1},
			wantHits: 1,
			check: func(t *testing.T, err error, _ map[string]any) {
				var apiErr *APIError
				require.ErrorAs(t, err, &apiErr)
				assert.Len(t, apiErr.Body, maxErrorBodyLen+len("..."))
			},
		},
		{
			name:     "undecodable body",
			replies:  []reply{{status: http.StatusOK, body: `{not json`}},
			wantHits: 1,
			check: func(t *testing.T, err error, _ map[string]any) {
				assert.ErrorContains(t, err, "decode /x")
			},
		},
		{
			name:     "post sends compact json",
			replies:  []reply{ok},
			method:   http.MethodPost,
			body:     []tokenRequest{{TokenID: "1"}, {TokenID: "2"}},
			wantHits: 1,
			onRequest: func(t *testing.T, r *http.Request, body string) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				assert.Equal(t, `[{"token_id":"1"},{"token_id":"2"}]`, body)
			},
			check: func(t *testing.T, err error, _ map[string]any) {
				require.NoError(t, err)
			},
		},
		{
			name:     "get sends headers and no body",
			replies:  []reply{ok},
			wantHits: 1,
			onRequest: func(t *testing.T, r *http.Request, body string) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
				assert.Equal(t, "application/json", r.Header.Get("Accept"))
				assert.Empty(t, r.Header.Get("Content-Type"))
				assert.Empty(t, body)
			},
			check: func(t *testing.T, err error, _ map[string]any) {
				require.NoError(t, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var onRequest func(*http.Request, string)
			if tt.onRequest != nil {
				onRequest = func(r *http.Request, body string) { tt.onRequest(t, r, body) }
			}

			base, hits := scriptedServer(t, tt.replies, onRequest)

			cfg := tt.cfg
			cfg.BaseURL = base
			core := newHTTPCore(logging.Noop(), cfg.withDefaults("", 1000))

			var (
				out map[string]any
				err error
			)

			if tt.method == http.MethodPost {
				err = core.post(context.Background(), "/x", tt.body, &out)
			} else {
				err = core.get(context.Background(), "/x", nil, &out)
			}

			assert.Equal(t, tt.wantHits, hits.Load(), "attempts")
			tt.check(t, err, out)
		})
	}
}

func TestCoreStopsWaitingOnCancellation(t *testing.T) {
	base, _ := scriptedServer(t, []reply{{status: http.StatusTooManyRequests, retryAfter: "10"}}, nil)
	core := newHTTPCore(logging.Noop(), HTTPConfig{BaseURL: base}.withDefaults("", 1000))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := core.get(ctx, "/x", nil, nil)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second)
}

func TestRetryDelay(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter string
		attempt    int
		want       time.Duration
	}{
		{name: "seconds", retryAfter: "3", want: 3 * time.Second},
		{name: "zero", retryAfter: "0", want: 0},
		{name: "fallback first attempt", want: time.Second},
		{name: "fallback backs off", attempt: 2, want: 4 * time.Second},
		{name: "fallback capped", attempt: 10, want: maxRetryWait},
		{name: "header capped", retryAfter: "120", want: maxRetryWait},
		{name: "date in the past", retryAfter: "Mon, 02 Jan 2006 15:04:05 GMT", want: 0},
		{name: "garbage falls back", retryAfter: "soon", attempt: 1, want: 2 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := http.Header{}
			if tt.retryAfter != "" {
				header.Set("Retry-After", tt.retryAfter)
			}

			assert.Equal(t, tt.want, retryDelay(header, tt.attempt))
		})
	}
}

func TestHTTPConfigDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   HTTPConfig
		want HTTPConfig
	}{
		{
			name: "zero value",
			want: HTTPConfig{BaseURL: "https://base", Timeout: defaultTimeout, RPS: 30, Burst: 30, MaxRetries: defaultMaxRetries},
		},
		{
			name: "negative retries disable retrying",
			in:   HTTPConfig{MaxRetries: -1},
			want: HTTPConfig{BaseURL: "https://base", Timeout: defaultTimeout, RPS: 30, Burst: 30, MaxRetries: 0},
		},
		{
			name: "explicit values win",
			in:   HTTPConfig{BaseURL: "https://other", Timeout: time.Second, RPS: 0.5, Burst: 4, MaxRetries: 7},
			want: HTTPConfig{BaseURL: "https://other", Timeout: time.Second, RPS: 0.5, Burst: 4, MaxRetries: 7},
		},
		{
			name: "fractional rate keeps a burst of one",
			in:   HTTPConfig{RPS: 0.5},
			want: HTTPConfig{BaseURL: "https://base", Timeout: defaultTimeout, RPS: 0.5, Burst: 1, MaxRetries: defaultMaxRetries},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.withDefaults("https://base", 30)

			assert.Equal(t, http.DefaultTransport, got.Transport)

			got.Transport = nil
			assert.Equal(t, tt.want, got)
		})
	}
}
