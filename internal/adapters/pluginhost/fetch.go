package pluginhost

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

	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

const (
	maxRedirects = 3
	maxHeaders   = 100 // of a request, and of a response passed back
)

var methods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	http.MethodOptions,
}

// reservedHeaders describe the request, which the client sets itself.
var reservedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Keep-Alive": true,
	"Upgrade": true, "Te": true, "Trailer": true, "Proxy-Authorization": true, "Proxy-Connection": true,
}

// errNotPermitted is a redirect to a host the permissions do not name.
var errNotPermitted = errors.New("its permissions do not name it")

// fetchHeadersKey carries the names of the headers a plugin set to CheckRedirect.
type fetchHeadersKey struct{}

// fetchClient makes plugins' requests: to public addresses only, unless an admin allows
// internal ones, checked as each connection is made, after DNS; following redirects only
// to hosts the plugin may fetch from, leaving its headers behind on another host.
func (a *api) fetchClient() *http.Client {
	dialer := webhook.GuardedDialer(a.deps.AllowPrivate)

	return &http.Client{
		// No otelhttp: its spans hold whole URLs, which may carry a token. The span of
		// each fetch has its host.
		Transport: &http.Transport{
			Proxy:               nil, // a proxy would dial instead of us and hide the address
			DialContext:         dialer.DialContext,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 5 * time.Second,
			MaxIdleConns:        20,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("more than %d redirects", maxRedirects)
			}

			if !a.perms.fetches(req.URL.Hostname()) {
				return fmt.Errorf("redirected to %s: %w", req.URL.Hostname(), errNotPermitted)
			}

			if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
				names, _ := req.Context().Value(fetchHeadersKey{}).([]string)
				for _, name := range names {
					req.Header.Del(name)
				}
			}

			return nil
		},
	}
}

// Fetch makes an HTTP request for a run, to a host its permissions name (D287).
func (a *api) Fetch(ctx context.Context, req *pluginv1.FetchRequest) (_ *pluginv1.FetchResponse, err error) {
	s, err := a.session(req.GetSession())
	if err != nil {
		return nil, err
	}

	if err := s.count(); err != nil {
		return nil, err
	}

	ctx, done := s.work(ctx)
	defer done()

	finish := a.kit.Start(ctx, opFetch)
	defer func() { finish(err == nil) }()

	return tracing.InScope(ctx, a.scope, opFetch, func(ctx context.Context, span trace.Span) (*pluginv1.FetchResponse, error) {
		span.SetAttributes(attribute.String("plugin.id", a.id))

		ctx, cancel := context.WithTimeout(ctx, s.limits.FetchTime)
		defer cancel()

		hreq, err := a.request(ctx, s.limits, req)
		if err != nil {
			return nil, err
		}

		host := hreq.URL.Hostname()
		span.SetAttributes(attribute.String("http.request.method", hreq.Method), attribute.String("server.address", host))

		resp, err := a.http.Do(hreq)
		if err != nil {
			return nil, a.fetchError(ctx, s, host, err)
		}

		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(io.LimitReader(resp.Body, int64(s.limits.FetchBody)+1))
		if err != nil {
			return nil, a.fetchError(ctx, s, host, err)
		}

		if len(body) > s.limits.FetchBody {
			return nil, status.Errorf(codes.OutOfRange, "the response from %s is larger than the %d bytes a fetch may take", host, s.limits.FetchBody)
		}

		span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode), attribute.Int("http.response.body.size", len(body)))
		a.log.Debug(ctx, "Plugin fetched", append(s.logArgs(), "host", host, "status", resp.StatusCode)...)

		out := &pluginv1.FetchResponse{Status: int32(resp.StatusCode), Body: body} //nolint:gosec // an HTTP status
		for _, name := range slices.Sorted(maps.Keys(resp.Header)) {
			for _, v := range resp.Header[name] {
				if len(out.Headers) == maxHeaders {
					break
				}

				out.Headers = append(out.Headers, &pluginv1.Header{Name: name, Value: v})
			}
		}

		return out, nil
	})
}

// request is the plugin's request, held to its permissions and the run's limits.
func (a *api) request(ctx context.Context, limits Limits, req *pluginv1.FetchRequest) (*http.Request, error) {
	method := req.GetMethod()
	if method == "" {
		method = http.MethodGet
	}

	if !slices.Contains(methods, method) {
		return nil, status.Errorf(codes.InvalidArgument, "method %q: one of %v", method, methods)
	}

	u, err := url.Parse(req.GetUrl())
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
		return nil, status.Error(codes.InvalidArgument, "the URL is not an http or https address")
	}

	if !a.perms.fetches(u.Hostname()) {
		return nil, status.Errorf(codes.PermissionDenied, "fetch from %s: %v (%s%s, or %s)",
			u.Hostname(), errNotPermitted, plugin.PermFetch, u.Hostname(), plugin.PermFetchAny)
	}

	if len(req.GetBody()) > limits.FetchBody {
		return nil, status.Errorf(codes.OutOfRange, "the body is larger than the %d bytes a fetch may send", limits.FetchBody)
	}

	if len(req.GetHeaders()) > maxHeaders {
		return nil, status.Errorf(codes.InvalidArgument, "more than %d headers", maxHeaders)
	}

	hreq, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(req.GetBody()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "the request is not one: "+err.Error())
	}

	var names []string

	for _, h := range req.GetHeaders() {
		name := textproto.CanonicalMIMEHeaderKey(h.GetName())
		if reservedHeaders[name] {
			return nil, status.Errorf(codes.InvalidArgument, "header %s: the host sets it", name)
		}

		hreq.Header.Add(name, h.GetValue())
		names = append(names, name)
	}

	if hreq.Header.Get("User-Agent") == "" {
		hreq.Header.Set("User-Agent", "Polythought-Plugin/1 ("+a.id+")")
	}

	return hreq.WithContext(context.WithValue(ctx, fetchHeadersKey{}, names)), nil
}

// fetchError is why a fetch failed, as the plugin hears it: never the whole URL, which
// may carry a token, and the guard's or the permissions' refusals as refusals.
func (a *api) fetchError(ctx context.Context, s *session, host string, err error) error {
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		err = urlErr.Err // its message repeats the whole URL
	}

	a.log.Debug(ctx, "Plugin's fetch failed", append(s.logArgs(), "host", host, "error", err.Error())...)

	switch {
	case errors.Is(err, errNotPermitted), errors.Is(err, webhook.ErrForbiddenAddress):
		return status.Errorf(codes.PermissionDenied, "fetch from %s: %v", host, err)
	case errors.Is(err, context.DeadlineExceeded) && s.ctx.Err() == nil:
		return status.Errorf(codes.DeadlineExceeded, "fetch from %s: no answer within %s", host, s.limits.FetchTime)
	default:
		return status.Errorf(codes.Unavailable, "fetch from %s: %v", host, err)
	}
}
