package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

const (
	maxResponseBytes = 32 << 20
	maxErrorBodyLen  = 512
	maxRetryWait     = 30 * time.Second
	userAgent        = "polythought-go"
)

// httpCore is the transport shared by the REST clients: rate limiting, retries and
// JSON decoding. Metrics and spans belong to the client methods that call it.
type httpCore struct {
	baseURL    string
	client     *http.Client
	limiter    *rate.Limiter
	maxRetries int
	log        logging.Logger
	now        func() time.Time
}

func newHTTPCore(log logging.Logger, cfg HTTPConfig) *httpCore {
	return &httpCore{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: otelhttp.NewTransport(cfg.Transport),
		},
		limiter:    rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst),
		maxRetries: cfg.MaxRetries,
		log:        log,
		now:        time.Now,
	}
}

// withLimiter is c on a budget of its own.
func (c *httpCore) withLimiter(l *rate.Limiter) *httpCore {
	other := *c
	other.limiter = l

	return &other
}

func (c *httpCore) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, nil, out)
}

func (c *httpCore) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, nil, out)
}

// getL2 sends an authenticated GET signed with creds.
func (c *httpCore) getL2(ctx context.Context, path string, query url.Values, creds L2Credentials, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, creds, out)
}

// authorizer signs a request's headers: L2 credentials, or L1's ClobAuth.
type authorizer interface {
	headers(now time.Time, method, path string, body []byte) (http.Header, error)
}

// do retries only on throttling and gateway errors. Reads are safe to retry; so is making
// an API key, which gives the same one again once made. The attempt count and final status
// land on the caller's span.
func (c *httpCore) do(ctx context.Context, method, path string, query url.Values, body any, creds authorizer, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("polymarket: encode %s: %w", path, err)
		}
	}

	span := trace.SpanFromContext(ctx)

	for attempt := 0; ; attempt++ {
		resp, err := c.once(ctx, method, path, query, payload, creds)
		if err != nil {
			return err
		}

		if isRetryable(resp.status) && attempt < c.maxRetries {
			if err := c.backOff(ctx, path, resp, attempt); err != nil {
				return err
			}

			continue
		}

		span.SetAttributes(
			attribute.Int(attrAttempts, attempt+1),
			attribute.Int(attrStatusCode, resp.status),
		)

		return decodeResponse(path, resp.status, resp.body, out)
	}
}

// response is what a request got back.
type response struct {
	status int
	header http.Header
	body   []byte
}

// SendError is a request that went out and got no answer: the server may or may not
// have acted on it.
type SendError struct {
	Method, Path string
	Err          error
}

func (e *SendError) Error() string {
	return fmt.Sprintf("polymarket: %s %s: %v", e.Method, e.Path, e.Err)
}

func (e *SendError) Unwrap() error { return e.Err }

// once sends a request a single time, signed now (a retry needs a fresh timestamp). Its
// error is a *SendError once the request may have gone out.
func (c *httpCore) once(ctx context.Context, method, path string, query url.Values, payload []byte, creds authorizer) (response, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return response{}, err
	}

	var auth http.Header
	if creds != nil {
		var err error
		if auth, err = creds.headers(c.now(), method, path, payload); err != nil {
			return response{}, err
		}
	}

	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	status, header, body, err := c.send(ctx, method, target, payload, auth)
	if err != nil {
		return response{}, &SendError{Method: method, Path: path, Err: err}
	}

	return response{status: status, header: header, body: body}, nil
}

// backOff waits before attempt's retry, as the answer asks or exponentially.
func (c *httpCore) backOff(ctx context.Context, path string, resp response, attempt int) error {
	wait := retryDelay(resp.header, attempt)
	c.log.Warn(ctx, "Retrying polymarket request",
		"path", path,
		"status", resp.status,
		"attempt", attempt+1,
		"wait", wait,
	)

	return sleep(ctx, wait)
}

func (c *httpCore) send(ctx context.Context, method, target string, payload []byte, auth http.Header) (int, http.Header, []byte, error) {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range auth {
		req.Header[k] = v // L2 header names are not canonical HTTP case; keep them as is
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read body: %w", err)
	}

	return resp.StatusCode, resp.Header, respBody, nil
}

func decodeResponse(path string, status int, body []byte, out any) error {
	switch {
	case status == http.StatusNotFound:
		return tracing.Expected(ErrNotFound, path)
	case status == http.StatusUnauthorized:
		return tracing.Expected(fmt.Errorf("%w: %s", ErrUnauthorized, truncate(body, maxErrorBodyLen)), path)
	case status < 200 || status > 299:
		return &APIError{StatusCode: status, Body: truncate(body, maxErrorBodyLen)}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("polymarket: decode %s: %w", path, err)
	}

	return nil
}

func isRetryable(status int) bool {
	switch status {
	case http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// retryDelay honours Retry-After (seconds or HTTP date), falling back to exponential backoff.
func retryDelay(header http.Header, attempt int) time.Duration {
	wait := time.Second << attempt

	if v := header.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil {
			wait = time.Duration(secs) * time.Second
		} else if at, err := http.ParseTime(v); err == nil {
			wait = time.Until(at)
		}
	}

	return min(max(wait, 0), maxRetryWait)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}

	return string(b[:n]) + "..."
}
