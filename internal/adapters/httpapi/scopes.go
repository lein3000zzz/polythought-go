package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// access is what an API token needs on a route (D398, D399).
type access struct {
	scopes  []domain.Scope // one of them; none, any token, the service deciding the rest
	browser bool           // a person at a browser only (D383): no token
}

func need(scopes ...domain.Scope) access { return access{scopes: scopes} }

var (
	anyToken    = access{}
	browserOnly = access{browser: true}
)

// routeScopes are the routes of /api/v1 that change something, with what a token needs
// there. Reads need read, which every scope brings, and the admin's routes admin; a route
// that changes something and is missing here takes no token, and a test keeps the two in
// step. Paper and live inside one route are its service's to tell apart.
var routeScopes = map[string]access{
	// Agents' and newcomers' own: no one is signed in, or no token is asked for.
	"POST /api/v1/oauth/register":              anyToken,
	"POST /api/v1/oauth/token":                 anyToken,
	"POST /api/v1/oauth/revoke":                anyToken,
	"POST /api/v1/oauth/authorization":         anyToken, // it only reads the request
	"POST /api/v1/oauth/authorization/approve": browserOnly,
	"POST /api/v1/oauth/authorization/deny":    browserOnly,
	"POST /api/v1/auth/register":               anyToken,
	"POST /api/v1/auth/login":                  anyToken,
	"POST /api/v1/auth/verify-email":           anyToken,
	"POST /api/v1/auth/verification":           anyToken,
	"POST /api/v1/auth/password-reset":         anyToken,
	"POST /api/v1/auth/password-reset/confirm": anyToken,
	"POST /api/v1/auth/invite":                 anyToken,
	"POST /api/v1/auth/requests":               anyToken,
	"POST /api/v1/auth/requests/confirm":       anyToken,
	"POST /api/v1/auth/logout":                 anyToken,
	"POST /api/v1/telegram/webhook":            anyToken,               // Telegram proves itself with a secret
	"POST /api/v1/code/workflow":               need(domain.ScopeRead), // it translates, saving nothing

	// The account.
	"PUT /api/v1/me/password":      browserOnly,
	"PUT /api/v1/me/kill-switch":   anyToken, // on with any scope, off with live: the service's
	"PUT /api/v1/me/agents":        need(domain.ScopeSettings),
	"DELETE /api/v1/me/grants/:id": need(domain.ScopeSettings),
	"PUT /api/v1/me/agent-access":  browserOnly, // what agents may see (D401)
	// Confirming tokens' and agents' live actions is the user's, at a browser (D406).
	"POST /api/v1/me/confirmations/:id/confirm": browserOnly,
	"POST /api/v1/me/confirmations/:id/refuse":  browserOnly,
	"PUT /api/v1/me/live-confirm":               browserOnly,
	"POST /api/v1/tokens":                       browserOnly,
	"DELETE /api/v1/tokens/:id":                 browserOnly,
	"PUT /api/v1/notifications/:key":            need(domain.ScopeSettings),
	// The linked chat confirms what tokens and agents ask for: linking it is the user's (A1).
	"POST /api/v1/telegram/link":   browserOnly,
	"DELETE /api/v1/telegram/link": browserOnly,

	// Wallets are for trading live; their keys and approvals only at a browser.
	"POST /api/v1/wallets":                                      need(domain.ScopeLive),
	"DELETE /api/v1/wallets/:id":                                need(domain.ScopeLive),
	"PUT /api/v1/wallets/:id/heartbeat":                         need(domain.ScopeLive),
	"PUT /api/v1/wallets/:id/credentials":                       browserOnly,
	"DELETE /api/v1/wallets/:id/credentials":                    need(domain.ScopeLive),
	"PUT /api/v1/wallets/:id/key":                               browserOnly,
	"POST /api/v1/wallets/:id/session-key":                      browserOnly,
	"DELETE /api/v1/wallets/:id/session-key":                    browserOnly,
	"POST /api/v1/wallets/:id/session-key/signature":            browserOnly,
	"POST /api/v1/wallets/:id/session-key/revocation":           browserOnly,
	"POST /api/v1/wallets/:id/session-key/revocation/signature": browserOnly,
	"POST /api/v1/wallets/:id/approvals/setup":                  browserOnly,
	"POST /api/v1/wallets/:id/approvals/signing":                browserOnly,
	"POST /api/v1/wallets/:id/approvals/signature":              browserOnly,

	// Building: credentials, nodes, workflows, the library. A workflow that places orders
	// needs its mode's scope too, which the workflows service asks.
	"POST /api/v1/credentials":                          need(domain.ScopeBuild),
	"PATCH /api/v1/credentials/:id":                     need(domain.ScopeBuild),
	"DELETE /api/v1/credentials/:id":                    need(domain.ScopeBuild),
	"POST /api/v1/credentials/:id/test":                 need(domain.ScopeBuild),
	"POST /api/v1/custom-nodes":                         need(domain.ScopeBuild),
	"PUT /api/v1/custom-nodes/:id":                      need(domain.ScopeBuild),
	"DELETE /api/v1/custom-nodes/:id":                   need(domain.ScopeBuild),
	"POST /api/v1/workflows":                            need(domain.ScopeBuild),
	"PUT /api/v1/workflows/:id":                         need(domain.ScopeBuild),
	"PUT /api/v1/workflows/:id/pin":                     need(domain.ScopeBuild),
	"PUT /api/v1/workflows/pins":                        need(domain.ScopeBuild),
	"DELETE /api/v1/workflows/:id":                      need(domain.ScopeBuild),
	"POST /api/v1/workflows/:id/activate":               need(domain.ScopeBuild),
	"POST /api/v1/workflows/:id/deactivate":             need(domain.ScopeBuild),
	"POST /api/v1/workflows/:id/run":                    need(domain.ScopeBuild),
	"POST /api/v1/workflows/:id/executions/:eid/cancel": need(domain.ScopeBuild),
	// A run's task is the user's to hand back or answer at a browser; agents answer theirs
	// over MCP (D413).
	"POST /api/v1/workflows/:id/executions/:eid/task/hand-back": browserOnly,
	"POST /api/v1/workflows/:id/executions/:eid/task/answer":    browserOnly,
	"DELETE /api/v1/workflows/:id/nodes/:node/memory":           need(domain.ScopeBuild),
	"PUT /api/v1/workflows/:id/share":                           need(domain.ScopeBuild),
	"POST /api/v1/library/:id/use":                              need(domain.ScopeBuild),
	"DELETE /api/v1/library/:id":                                need(domain.ScopeBuild),

	// Trading: paper or live, which the trading service tells apart.
	"POST /api/v1/workflows/:id/paper-account/reset":  need(domain.ScopePaper),
	"PUT /api/v1/workflows/:id/trading/setup":         need(domain.ScopePaper, domain.ScopeLive),
	"POST /api/v1/workflows/:id/orders/:order/cancel": need(domain.ScopePaper, domain.ScopeLive),
}

// accessOf is what a token needs for a request to the route.
func accessOf(method, route string) (access, bool) {
	if strings.HasPrefix(route, "/api/v1/admin/") {
		return need(domain.ScopeAdmin), true
	}

	if a, ok := routeScopes[method+" "+route]; ok {
		return a, true
	}

	if method == http.MethodGet || method == http.MethodHead {
		return need(domain.ScopeRead), true
	}

	return access{}, false
}

func (a access) allows(p auth.Principal) error {
	switch {
	case a.browser:
		return fmt.Errorf("%w: only at a browser", domain.ErrForbidden)
	case len(a.scopes) == 0:
		return nil
	}

	return p.Need(a.scopes...)
}

// checkScope holds an API token to its scopes on /api/v1 (D398, D399); a session is the
// user, with every scope.
func (s *Server) checkScope(ctx context.Context, c *app.RequestContext) {
	p, ok := principal(c)
	route := c.FullPath()

	if !ok || p.TokenID == "" || !strings.HasPrefix(route, "/api/v1/") {
		c.Next(ctx)
		return
	}

	a, listed := accessOf(string(c.Method()), route)

	err := a.allows(p)
	if !listed {
		err = fmt.Errorf("%w: this takes no token", domain.ErrInsufficientScope)
	}

	if err != nil {
		s.fail(ctx, c, err)
		c.Abort()

		return
	}

	c.Next(ctx)
}
