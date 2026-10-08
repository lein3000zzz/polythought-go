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
	}
}

func (c *httpCore) get(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

func (c *httpCore) post(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// do retries only on throttling and gateway errors. Every MVP endpoint is a read, so retries are safe.
// The attempt count and final status land on the caller's span.
func (c *httpCore) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("polymarket: encode %s: %w", path, err)
		}
	}

	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	span := trace.SpanFromContext(ctx)

	for attempt := 0; ; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		status, header, respBody, err := c.send(ctx, method, target, payload)
		if err != nil {
			return fmt.Errorf("polymarket: %s %s: %w", method, path, err)
		}

		if isRetryable(status) && attempt < c.maxRetries {
			wait := retryDelay(header, attempt)
			c.log.Warn(ctx, "Retrying polymarket request",
				"path", path,
				"status", status,
				"attempt", attempt+1,
				"wait", wait,
			)

			if err := sleep(ctx, wait); err != nil {
				return err
			}

			continue
		}

		span.SetAttributes(
			attribute.Int(attrAttempts, attempt+1),
			attribute.Int(attrStatusCode, status),
		)

		return decodeResponse(path, status, respBody, out)
	}
}

func (c *httpCore) send(ctx context.Context, method, target string, payload []byte) (int, http.Header, []byte, error) {
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

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()

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
