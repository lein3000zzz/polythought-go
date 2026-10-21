// Package webhook posts JSON to addresses users put in their workflows.
//
// Those addresses are untrusted. Unless the operator allows it, the sender refuses to
// connect to loopback, private, link-local and other internal addresses. It checks the
// address actually dialled, after DNS, so a name that resolves inside the network is
// refused too, on redirects as well.
//
// Extra headers, usually a credential, go only to the host the user named: a redirect to
// another host loses them.
package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"slices"
	"syscall"
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
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	opPost     = "webhook.Post"

	defaultTimeout = 10 * time.Second
	maxRedirects   = 3
	maxResponse    = 64 << 10 // read and dropped, so the connection can be reused
)

var (
	_ ports.WebhookSender = (*Sender)(nil)

	ErrInvalidURL       = errors.New("not an http or https address")
	ErrForbiddenAddress = errors.New("address inside the server's network")
	ErrPublicInClear    = errors.New("a public address in plain http")
	ErrReservedHeader   = errors.New("header set by the sender itself")
)

// reservedHeaders describe the request; extra headers may not replace them.
var reservedHeaders = map[string]bool{
	"Host": true, "Content-Length": true, "Content-Type": true, "Transfer-Encoding": true,
	"Connection": true, "Keep-Alive": true, "Upgrade": true, "Te": true, "Trailer": true,
	"User-Agent": true,
}

// extraHeadersKey carries the names of a request's extra headers to CheckRedirect.
type extraHeadersKey struct{}

type Config struct {
	// AllowPrivate lets workflows post to internal addresses, for self-hosted setups
	// that send to a service on the same machine or network. It is asked on every
	// connection, so a change applies at once; nil means never.
	AllowPrivate func() bool
	Timeout      time.Duration
}

type Sender struct {
	client *http.Client
	kit    *redkit.Kit
	scope  tracing.Scope
	log    logging.Logger
}

func New(util *observability.Utility, cfg Config) (*Sender, error) {
	log := util.Logger.With("component", "webhook")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "webhook",
		Buckets:   redkit.MediumLongBuckets,
		Component: "webhook",
	})
	if err != nil {
		return nil, fmt.Errorf("webhook: register metrics: %w", err)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	dialer := GuardedDialer(cfg.AllowPrivate)

	transport := &http.Transport{
		Proxy:                 nil, // a proxy would dial instead of us and hide the address
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          20,
		IdleConnTimeout:       90 * time.Second,
	}

	return &Sender{
		client: &http.Client{
			Timeout: timeout,
			// No otelhttp: its spans hold the whole URL, and a webhook's often has its
			// token in the path (R19). The kit times each send.
			Transport:     transport,
			CheckRedirect: redirected,
		},
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
	}, nil
}

// Post sends body as JSON, with any extra headers. Errors name only the host: the rest of
// a webhook URL is usually a secret token, and errors end up in logs and executions.
// They never include header values.
func (s *Sender) Post(ctx context.Context, rawURL string, body []byte, header map[string]string) (err error) {
	done := s.kit.Start(ctx, opPost)
	defer func() { done(err == nil) }()

	_, err = tracing.InScope(ctx, s.scope, opPost, func(ctx context.Context, span trace.Span) (struct{}, error) {
		u, err := url.Parse(rawURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return struct{}{}, ErrInvalidURL
		}

		host := u.Hostname()
		span.SetAttributes(attribute.String("server.address", host), attribute.Int("http.request.body.size", len(body)))

		names := make([]string, 0, len(header))
		for name := range header {
			name = textproto.CanonicalMIMEHeaderKey(name)
			if reservedHeaders[name] {
				return struct{}{}, fmt.Errorf("%w: %s", ErrReservedHeader, name)
			}

			names = append(names, name)
		}

		ctx = context.WithValue(ctx, extraHeadersKey{}, names)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
		if err != nil {
			return struct{}{}, ErrInvalidURL
		}

		for name, value := range header {
			req.Header.Set(name, value)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "Polythought-Webhook/1")

		resp, err := s.client.Do(req)
		if err != nil {
			var urlErr *url.Error
			if errors.As(err, &urlErr) {
				err = urlErr.Err // its message repeats the whole URL
			}

			s.log.Warn(ctx, "Webhook not delivered", "host", host, logging.Err(err))

			return struct{}{}, fmt.Errorf("webhook to %s: %w", host, err)
		}

		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponse))

		span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			s.log.Warn(ctx, "Webhook refused", "host", host, "status", resp.StatusCode)
			return struct{}{}, fmt.Errorf("webhook to %s answered %d", host, resp.StatusCode)
		}

		s.log.Debug(ctx, "Webhook delivered", "host", host, "status", resp.StatusCode)

		return struct{}{}, nil
	})

	return err
}

// carrierGradeNAT is shared address space (RFC 6598), internal like the private ranges.
var carrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// GuardedDialer dials public addresses only, checked as each connection is made, after
// DNS, so a name cannot resolve its way inside (redirects included); allowPrivate, asked
// on every connection, lets internal ones through too. For any request to an address a
// user chose: webhooks, and OpenAI-compatible base URLs (phase 4).
func GuardedDialer(allowPrivate func() bool) *net.Dialer {
	return &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			if allowPrivate != nil && allowPrivate() {
				return nil
			}

			return refuseInternal(network, address, c)
		},
	}
}

// InternalDialer dials addresses inside the server's network only, and only while
// allowPrivate says: for plain http to a server there, which must never carry a key
// across the internet in clear.
func InternalDialer(allowPrivate func() bool) *net.Dialer {
	return &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			addr, err := dialed(address)
			if err != nil {
				return err
			}

			switch {
			case allowPrivate == nil || !allowPrivate():
				return fmt.Errorf("%w: %s", ErrForbiddenAddress, addr)
			case public(addr):
				return fmt.Errorf("%w: %s", ErrPublicInClear, addr)
			}

			return nil
		},
	}
}

// refuseInternal runs as each connection is made, with the resolved address.
func refuseInternal(_, address string, _ syscall.RawConn) error {
	addr, err := dialed(address)
	if err != nil {
		return err
	}

	if !public(addr) {
		return fmt.Errorf("%w: %s", ErrForbiddenAddress, addr)
	}

	return nil
}

// dialed is the address a connection is being made to, resolved.
func dialed(address string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return netip.Addr{}, err
	}

	return netip.ParseAddr(host)
}

// redirected follows a redirect, at most maxRedirects of them, leaving the extra headers
// behind once it leaves the first URL's host or scheme: Go drops Authorization and cookies
// on another host, but not headers like X-Api-Key, nor any on one from https to http
// (R20).
func redirected(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("more than %d redirects", maxRedirects)
	}

	if req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		names, _ := req.Context().Value(extraHeadersKey{}).([]string)
		for _, name := range names {
			req.Header.Del(name)
		}
	}

	return nil
}

func public(addr netip.Addr) bool {
	addr = addr.Unmap()

	thisNetwork := addr.Is4() && addr.As4()[0] == 0 // 0.0.0.0/8

	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !carrierGradeNAT.Contains(addr) && !thisNetwork &&
		!slices.ContainsFunc(reserved, func(p netip.Prefix) bool { return p.Contains(addr) })
}

// reserved are the ranges netip counts as global that are not, or that carry an IPv4
// address inside an IPv6 one, which a gateway may reach inside the network (R13).
var reserved = []netip.Prefix{
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"), // NAT64, local
	netip.MustParsePrefix("2002::/16"),      // 6to4
	netip.MustParsePrefix("2001::/32"),      // Teredo
	netip.MustParsePrefix("192.0.0.0/24"),   // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"),  // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),    // reserved, and broadcast
}
