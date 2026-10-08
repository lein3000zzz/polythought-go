package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

const tracerName = "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"

const (
	maxResponseBytes = 32 << 20
	maxErrorBodyLen  = 512
	maxRetryWait     = 30 * time.Second
	userAgent        = "polythought-go"
)

// httpCore is the transport shared by the REST clients: rate limiting, retries,
// RED metrics, one span per operation and JSON decoding.
type httpCore struct {
	baseURL    string
	client     *http.Client
	limiter    *rate.Limiter
	maxRetries int
	kit        *redkit.Kit
	scope      tracing.Scope
	log        logging.Logger
}

func newHTTPCore(util *observability.Utility, name string, cfg HTTPConfig) (*httpCore, error) {
	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    util.Logger,
		Subsystem: "polymarket_" + name,
		Buckets:   redkit.MediumLongBuckets,
		Component: "polymarket " + name,
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register %s metrics: %w", name, err)
	}

	return &httpCore{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		client: &http.Client{
			Timeout:   cfg.Timeout,
			Transport: otelhttp.NewTransport(cfg.Transport),
		},
		limiter:    rate.NewLimiter(rate.Limit(cfg.RPS), cfg.Burst),
		maxRetries: cfg.MaxRetries,
		kit:        kit,
		scope:      tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:        util.Logger.With("component", "polymarket."+name),
	}, nil
}

func (c *httpCore) get(ctx context.Context, op, path string, query url.Values, out any) error {
	return c.do(ctx, op, http.MethodGet, path, query, nil, out)
}

func (c *httpCore) post(ctx context.Context, op, path string, body, out any) error {
	return c.do(ctx, op, http.MethodPost, path, nil, body, out)
}

// do retries only on throttling and gateway errors. Every MVP endpoint is a read, so retries are safe.
func (c *httpCore) do(ctx context.Context, op, method, path string, query url.Values, body, out any) (err error) {
	done := c.kit.Start(ctx, op)
	defer func() { done(err == nil || errors.Is(err, ErrNotFound)) }()

	return c.scope.WithSpan(ctx, op, func(ctx context.Context, _ trace.Span) error {
		var payload []byte
		if body != nil {
			var err error
			payload, err = json.Marshal(body)
			if err != nil {
				return fmt.Errorf("polymarket: encode %s: %w", op, err)
			}
		}

		target := c.baseURL + path
		if len(query) > 0 {
			target += "?" + query.Encode()
		}

		for attempt := 0; ; attempt++ {
			if err := c.limiter.Wait(ctx); err != nil {
				return err
			}

			status, header, respBody, err := c.send(ctx, method, target, payload)
			if err != nil {
				return fmt.Errorf("polymarket: %s: %w", op, err)
			}

			if isRetryable(status) && attempt < c.maxRetries {
				wait := retryDelay(header, attempt)
				c.log.Warn(ctx, "Retrying polymarket request",
					"operation", op,
					"status", status,
					"attempt", attempt+1,
					"wait", wait,
				)

				if err := sleep(ctx, wait); err != nil {
					return err
				}

				continue
			}

			return decodeResponse(op, status, respBody, out)
		}
	})
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

func decodeResponse(op string, status int, body []byte, out any) error {
	switch {
	case status == http.StatusNotFound:
		return tracing.Expected(ErrNotFound, op)
	case status < 200 || status > 299:
		return &APIError{StatusCode: status, Body: truncate(body, maxErrorBodyLen)}
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("polymarket: decode %s: %w", op, err)
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
