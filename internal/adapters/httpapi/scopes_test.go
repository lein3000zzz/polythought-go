package httpapi_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

// Every route of /api/v1 that changes something says what a token needs there, and the
// list names no route that is gone (D399): a new route has to be given its scope.
func TestRouteScopesCoverRoutes(t *testing.T) {
	srv, _, _ := oauthServer(t)

	var routes []string

	listed := httpapi.RouteScopes()

	for _, r := range srv.Engine().Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/") || strings.HasPrefix(r.Path, "/api/v1/admin/") {
			continue // the admin's need admin, all of them
		}

		if (r.Method == http.MethodGet || r.Method == http.MethodHead) && !slices.Contains(listed, r.Method+" "+r.Path) {
			continue // reads need read, but those that need more
		}

		routes = append(routes, r.Method+" "+r.Path)
	}

	slices.Sort(routes)
	slices.Sort(listed)

	for _, r := range routes {
		assert.Contains(t, listed, r, "a route with no scope: add it to routeScopes")
	}

	for _, r := range listed {
		assert.Contains(t, routes, r, "routeScopes names a route that is gone")
	}
}

// scoped signs requests in with an API token that has only scopes.
func scoped(svc *httpapimocks.MockAuthService, u domain.User, scopes ...domain.Scope) {
	svc.EXPECT().AuthenticateToken(mock.Anything, "pt_secret").
		Return(auth.Principal{User: u, TokenID: "t-1", Scopes: scopes}, nil)
}

func TestScopesOnRoutes(t *testing.T) {
	revoke := request{method: http.MethodDelete, path: "/api/v1/me/grants/g-1", headers: bearer}

	t.Run("with the scope", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		scoped(authSvc, alice, domain.ScopeSettings)
		svc.EXPECT().RevokeGrant(mock.Anything, mock.Anything, domain.GrantID("g-1")).Return(nil)

		assert.Equal(t, http.StatusNoContent, revoke.do(t, srv).Code)
	})

	t.Run("a token from before scopes", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withToken(authSvc, alice)
		svc.EXPECT().RevokeGrant(mock.Anything, mock.Anything, domain.GrantID("g-1")).Return(nil)

		assert.Equal(t, http.StatusNoContent, revoke.do(t, srv).Code)
	})

	t.Run("a session", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().RevokeGrant(mock.Anything, mock.Anything, domain.GrantID("g-1")).Return(nil)

		w := request{method: http.MethodDelete, path: "/api/v1/me/grants/g-1", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	// Refused before the handler: the service mocks are asked nothing.
	for name, tt := range map[string]struct {
		req    request
		user   domain.User
		scopes []domain.Scope
		status int
		code   string
	}{
		"settings, without it": {revoke, alice, []domain.Scope{domain.ScopeBuild}, http.StatusForbidden, "insufficient_scope"},
		"building, with read":  {request{method: http.MethodPost, path: "/api/v1/workflows", body: `{}`, headers: bearer}, alice, []domain.Scope{domain.ScopeRead}, http.StatusForbidden, "insufficient_scope"},
		"a wallet, without live": {
			request{method: http.MethodPost, path: "/api/v1/wallets", body: `{}`, headers: bearer}, alice,
			[]domain.Scope{domain.ScopeBuild, domain.ScopePaper}, http.StatusForbidden, "insufficient_scope",
		},
		"trading, with build alone": {
			request{method: http.MethodPut, path: "/api/v1/workflows/w-1/trading/setup", body: `{}`, headers: bearer}, alice,
			[]domain.Scope{domain.ScopeBuild}, http.StatusForbidden, "insufficient_scope",
		},
		"admin's, without admin": {request{method: http.MethodGet, path: "/api/v1/admin/users", headers: bearer}, root, []domain.Scope{domain.ScopeRead}, http.StatusForbidden, "insufficient_scope"},
		"only at a browser":      {request{method: http.MethodPost, path: "/api/v1/tokens", body: `{}`, headers: bearer}, alice, domain.Scopes, http.StatusForbidden, "forbidden"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, authSvc, _ := oauthServer(t)
			scoped(authSvc, tt.user, tt.scopes...)

			w := tt.req.do(t, srv)

			require.Equal(t, tt.status, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"`+tt.code+`"`)
		})
	}

	// Reads need read, which every scope brings.
	t.Run("reading, with paper", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		scoped(authSvc, alice, domain.ScopePaper)
		svc.EXPECT().Grants(mock.Anything, mock.Anything).Return(nil, nil)

		w := request{method: http.MethodGet, path: "/api/v1/me/grants", headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("the message names the scope", func(t *testing.T) {
		srv, authSvc, _ := oauthServer(t)
		scoped(authSvc, alice, domain.ScopeRead)

		w := request{method: http.MethodPost, path: "/api/v1/workflows/w-1/orders/o-1/cancel", headers: bearer}.do(t, srv)
		assert.Contains(t, w.Body.String(), "it needs the paper or live scope")
	})
}
