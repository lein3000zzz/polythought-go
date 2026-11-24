package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	opDo = "http.Do"

	// MaxResponse is the most of a response body a request reads (D438).
	MaxResponse = 1 << 20

	// maxResponseHeaders bounds the header values a response hands on.
	maxResponseHeaders = 100
)

var _ ports.HTTPRequester = (*Requester)(nil)

var (
	ErrMethod      = errors.New("not a method requests use")
	ErrTooLarge    = errors.New("larger than the 1 MiB a response may be")
	ErrBadHeader   = errors.New("not a header")
	ErrNoAnswer    = errors.New("no answer in time")
	requestMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
)

// requestReserved describe the request, which the client sets itself.
var requestReserved = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Keep-Alive": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Proxy-Authorization": true, "Proxy-Connection": true,
}

// kept go on with a redirect to another host or scheme: they say nothing of the user.
// Any other, a key in X-Api-Key say, stays behind (R20).
var kept = map[string]bool{"Accept": true, "Accept-Language": true, "Content-Type": true, "User-Agent": true}

// Requester makes HTTP request nodes' requests (D438): any of five methods, to public
// addresses only unless the operator allows internal ones, checked as each connection is
// made, after DNS, redirects included; at most 3 redirects; a response read to 1 MiB.
type Requester struct {
	client  *http.Client
	timeout time.Duration
	kit     *redkit.Kit
	scope   tracing.Scope
	log     logging.Logger
}

func NewRequester(util *observability.Utility, cfg Config) (*Requester, error) {
	log := util.Logger.With("component", "http_request")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "http_request",
		Buckets:   redkit.MediumLongBuckets,
		Component: "http_request",
	})
	if err != nil {
		return nil, fmt.Errorf("http requests: register metrics: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	dialer := GuardedDialer(cfg.AllowPrivate, cfg.Proxy)

	return &Requester{
		client: &http.Client{
			// No otelhttp: its spans hold the whole URL, which may carry a key (R19). The
			// kit times each request; each request's context bounds it.
			Transport: &http.Transport{
				Proxy:               nil, // the dialer goes through one, not the transport, which would hide the address
				DialContext:         dialer.DialContext,
				ForceAttemptHTTP2:   true,
				TLSHandshakeTimeout: 5 * time.Second,
				MaxIdleConns:        20,
				IdleConnTimeout:     90 * time.Second,
			},
			CheckRedirect: redirected,
		},
		timeout: timeout,
		kit:     kit,
		scope:   tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:     log,
	}, nil
}

// Do makes the request and reads its answer, whatever the status. Errors name only the
// host and never a header's value.
func (r *Requester) Do(ctx context.Context, req ports.HTTPRequest) (resp ports.HTTPResponse, err error) {
	done := r.kit.Start(ctx, opDo)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, r.scope, opDo, func(ctx context.Context, span trace.Span) (ports.HTTPResponse, error) {
		timeout := req.Timeout
		if timeout <= 0 {
			timeout = r.timeout
		}

		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		hreq, err := r.request(ctx, req)
		if err != nil {
			return ports.HTTPResponse{}, err
		}

		host := hreq.URL.Hostname()
		span.SetAttributes(attribute.String("http.request.method", hreq.Method), attribute.String("server.address", host),
			attribute.Int("http.request.body.size", len(req.Body)))

		got, err := r.client.Do(hreq)
		if err != nil {
			return ports.HTTPResponse{}, r.failed(ctx, req.Method, host, timeout, err)
		}

		defer func() { _ = got.Body.Close() }()

		body, err := io.ReadAll(io.LimitReader(got.Body, MaxResponse+1))
		if err != nil {
			return ports.HTTPResponse{}, r.failed(ctx, req.Method, host, timeout, err)
		}

		if len(body) > MaxResponse {
			return ports.HTTPResponse{}, fmt.Errorf("%s %s: the response is %w", req.Method, host, ErrTooLarge)
		}

		span.SetAttributes(attribute.Int("http.response.status_code", got.StatusCode), attribute.Int("http.response.body.size", len(body)))
		r.log.Debug(ctx, "HTTP request answered", "method", req.Method, "host", host, "status", got.StatusCode)

		return ports.HTTPResponse{Status: got.StatusCode, Header: headersOf(got.Header), Body: body}, nil
	})
}

// request is req as one to send, its headers checked.
func (r *Requester) request(ctx context.Context, req ports.HTTPRequest) (*http.Request, error) {
	if !slices.Contains(requestMethods, req.Method) {
		return nil, fmt.Errorf("%q: %w, one of %v", req.Method, ErrMethod, requestMethods)
	}

	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, ErrInvalidURL
	}

	var (
		body  io.Reader
		names []string
	)

	if len(req.Body) > 0 {
		body = bytes.NewReader(req.Body)
	}

	hreq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), body)
	if err != nil {
		return nil, ErrInvalidURL
	}

	for name, value := range req.Header {
		name = textproto.CanonicalMIMEHeaderKey(name)

		switch {
		case requestReserved[name]:
			return nil, fmt.Errorf("%w: %s", ErrReservedHeader, name)
		case !validHeader(name, value):
			return nil, fmt.Errorf("%w: %s", ErrBadHeader, name)
		}

		hreq.Header.Set(name, value)

		if !kept[name] {
			names = append(names, name)
		}
	}

	if hreq.Header.Get("User-Agent") == "" {
		hreq.Header.Set("User-Agent", "Polythought-HTTP/1")
	}

	return hreq.WithContext(context.WithValue(ctx, extraHeadersKey{}, names)), nil
}

// failed is why a request got no answer, naming the host only: url.Error repeats the
// whole URL.
func (r *Requester) failed(ctx context.Context, method, host string, timeout time.Duration, err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		err = urlErr.Err
	}

	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%w: none within %s", ErrNoAnswer, timeout)
	}

	r.log.Debug(ctx, "HTTP request failed", "method", method, "host", host, logging.Err(err))

	return fmt.Errorf("%s %s: %w", method, host, err)
}

// headersOf are a response's headers, at most maxResponseHeaders values of them.
func headersOf(h http.Header) map[string][]string {
	out := make(map[string][]string, len(h))
	n := 0

	for _, name := range slices.Sorted(maps.Keys(h)) {
		for _, v := range h[name] {
			if n == maxResponseHeaders {
				return out
			}

			out[name] = append(out[name], v)
			n++
		}
	}

	return out
}

// validHeader reports whether name is a header name and value one line of text.
func validHeader(name, value string) bool {
	if name == "" {
		return false
	}

	for _, c := range name {
		if c <= ' ' || c >= 0x7f || c == ':' {
			return false
		}
	}

	for _, c := range value {
		if c < ' ' && c != '\t' || c == 0x7f {
			return false
		}
	}

	return true
}
