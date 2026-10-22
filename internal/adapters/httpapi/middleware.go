package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	sessionCookie = "polythought_session"
	// secureCookie is its name over https: the prefix has browsers refuse one set for a
	// domain, or by a page that is not secure, so a sibling subdomain cannot plant it
	// (R18).
	secureCookie = "__Host-" + sessionCookie
	principalKey = "principal"
)

var errServer = errors.New("server error")

// observe wraps each request in a span (continuing an incoming traceparent), records RED
// metrics per route and writes an access log line.
func (s *Server) observe(ctx context.Context, c *app.RequestContext) {
	headers := make(map[string]string)
	c.Request.Header.VisitAll(func(k, v []byte) { headers[strings.ToLower(string(k))] = string(v) })
	ctx = oteltracing.ExtractMap(ctx, headers)

	route := c.FullPath()
	if route == "" {
		route = "unmatched"
	}

	op := string(c.Method()) + " " + route
	start := time.Now()

	done := s.kit.Start(ctx, op)

	_ = s.scope.WithSpan(ctx, op, func(ctx context.Context, span trace.Span) error {
		c.Next(ctx)

		status := c.Response.StatusCode()
		span.SetAttributes(
			attribute.String("http.request.method", string(c.Method())),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
			attribute.String("client.address", c.ClientIP()),
		)

		if status >= http.StatusInternalServerError {
			return fmt.Errorf("%w: status %d", errServer, status)
		}

		return nil
	}, trace.WithSpanKind(trace.SpanKindServer))

	status := c.Response.StatusCode()
	done(status < http.StatusInternalServerError)

	s.log.Info(ctx, "HTTP request",
		"method", string(c.Method()),
		"route", route,
		"status", status,
		"duration", time.Since(start),
		"ip", c.ClientIP(),
	)
}

// authenticate resolves the caller from a bearer token or the session cookie. A bearer
// token that fails is rejected outright instead of falling back to the cookie.
func (s *Server) authenticate(ctx context.Context, c *app.RequestContext) {
	if header := string(c.GetHeader("Authorization")); header != "" {
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok {
			s.fail(ctx, c, domain.ErrUnauthenticated)
			c.Abort()

			return
		}

		p, err := s.auth.AuthenticateToken(ctx, strings.TrimSpace(token))
		if err != nil {
			s.fail(ctx, c, err)
			c.Abort()

			return
		}

		c.Set(principalKey, p)
		c.Next(ctx)

		return
	}

	if token := string(c.Cookie(s.cookieName())); token != "" {
		p, err := s.auth.AuthenticateSession(ctx, token)
		switch {
		case err == nil:
			c.Set(principalKey, p)
		case errors.Is(err, domain.ErrUnauthenticated):
			s.clearSessionCookie(c) // stale cookie: drop it, carry on anonymously
		default:
			s.fail(ctx, c, err)
			c.Abort()

			return
		}
	}

	c.Next(ctx)
}

func (s *Server) requireAuth(ctx context.Context, c *app.RequestContext) {
	if _, ok := principal(c); !ok {
		s.fail(ctx, c, domain.ErrUnauthenticated)
		c.Abort()

		return
	}

	c.Next(ctx)
}

// checkOrigin blocks cross-site state changes on requests a browser could send with the
// session cookie. Requests authenticated by bearer token are exempt: browsers never add
// that header on their own. When a browser omits both Origin and Referer, SameSite=Lax
// on the cookie is the remaining guard.
func (s *Server) checkOrigin(ctx context.Context, c *app.RequestContext) {
	if isSafeMethod(string(c.Method())) || len(c.GetHeader("Authorization")) > 0 {
		c.Next(ctx)
		return
	}

	origin := string(c.GetHeader("Origin"))
	if origin == "" {
		if ref := string(c.GetHeader("Referer")); ref != "" {
			origin = originOf(ref)
		}
	}

	if origin != "" && origin != s.origin {
		s.log.Warn(ctx, "Cross-origin request blocked", "origin", origin, "ip", c.ClientIP())
		s.fail(ctx, c, domain.ErrForbidden)
		c.Abort()

		return
	}

	c.Next(ctx)
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func originOf(rawURL string) string {
	scheme, rest, ok := strings.Cut(rawURL, "://")
	if !ok {
		return ""
	}

	host, _, _ := strings.Cut(rest, "/")

	return scheme + "://" + host
}

func principal(c *app.RequestContext) (auth.Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return auth.Principal{}, false
	}

	p, ok := v.(auth.Principal)

	return p, ok
}

func (s *Server) setSessionCookie(c *app.RequestContext, token string, expires time.Time) {
	maxAge := int(time.Until(expires).Seconds())
	c.SetCookie(s.cookieName(), token, maxAge, "/", "", protocolLax, s.secure, true)
}

func (s *Server) clearSessionCookie(c *app.RequestContext) {
	c.SetCookie(s.cookieName(), "", -1, "/", "", protocolLax, s.secure, true)
}

// cookieName is the session cookie's name: with the __Host- prefix when it is secure.
func (s *Server) cookieName() string {
	if s.secure {
		return secureCookie
	}

	return sessionCookie
}

// limitBody holds each request's body to max bytes, as the server would without streaming
// bodies, which it does for archives' uploads alone (archiveRoute): their body goes on to
// the plugins directory, which caps it itself. A body past max is refused, 413.
func (s *Server) limitBody(max int) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		if !c.Request.IsBodyStream() || (string(c.Method()) == http.MethodPost && c.FullPath() == archiveRoute) {
			c.Next(ctx)
			return
		}

		body, err := io.ReadAll(io.LimitReader(c.RequestBodyStream(), int64(max)+1))
		if err != nil || len(body) > max {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, errorBody{Error: errorDetail{
				Code: "too_large", Message: fmt.Sprintf("the request's body is larger than the %d bytes the server takes", max),
			}})

			return
		}

		c.Request.SetBody(body)
		c.Next(ctx)
	}
}
