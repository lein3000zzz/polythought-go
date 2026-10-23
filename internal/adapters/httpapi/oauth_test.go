package httpapi_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/oauth"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func oauthServer(t *testing.T) (*httpapi.Server, *httpapimocks.MockAuthService, *httpapimocks.MockOAuthService) {
	t.Helper()

	authSvc, svc := httpapimocks.NewMockAuthService(t), httpapimocks.NewMockOAuthService(t)

	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, OAuth: svc})
	require.NoError(t, err)

	return srv, authSvc, svc
}

// postForm posts a form, as agents call the token and revocation endpoints.
func postForm(t *testing.T, srv *httpapi.Server, path string, values url.Values, headers map[string]string) *ut.ResponseRecorder {
	t.Helper()

	body := values.Encode()
	hs := []ut.Header{{Key: "Content-Type", Value: "application/x-www-form-urlencoded"}}

	for k, v := range headers {
		hs = append(hs, ut.Header{Key: k, Value: v})
	}

	return ut.PerformRequest(srv.Engine().Engine, http.MethodPost, path, &ut.Body{Body: bytes.NewBufferString(body), Len: len(body)}, hs...)
}

// Agents' programs call these with no cookie, from no page: another site's Origin and
// an Authorization header that is not a token of ours are both let through.
var agentHeaders = map[string]string{origin: "https://elsewhere.example", "Authorization": "Basic cGM6"}

func TestOAuthMetadataRoute(t *testing.T) {
	srv, _, svc := oauthServer(t)
	svc.EXPECT().Metadata().Return(oauth.Metadata{Issuer: publicURL, TokenEndpoint: publicURL + "/api/v1/oauth/token"})

	w := request{method: http.MethodGet, path: "/.well-known/oauth-authorization-server", headers: agentHeaders}.do(t, srv)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "max-age=3600", string(w.Header().Peek("Cache-Control")))

	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, publicURL, got["issuer"])
	assert.Equal(t, publicURL+"/api/v1/oauth/token", got["token_endpoint"])
}

func TestOAuthRegisterRoute(t *testing.T) {
	const body = `{"client_name": "Agent", "redirect_uris": ["http://127.0.0.1/cb"], "grant_types": ["authorization_code"]}`

	t.Run("registered", func(t *testing.T) {
		srv, _, svc := oauthServer(t)
		svc.EXPECT().Register(mock.Anything, oauth.Registration{
			ClientName: "Agent", RedirectURIs: []string{"http://127.0.0.1/cb"}, GrantTypes: []string{"authorization_code"},
		}, mock.Anything).Return(domain.OAuthClient{ID: "pc_1", Name: "Agent", RedirectURIs: []string{"http://127.0.0.1/cb"}, CreatedAt: created}, nil)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/register", body: body, headers: agentHeaders}.do(t, srv)

		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		assert.JSONEq(t, `{
			"client_id": "pc_1", "client_id_issued_at": 1791460800, "client_name": "Agent",
			"redirect_uris": ["http://127.0.0.1/cb"], "grant_types": ["authorization_code", "refresh_token"],
			"response_types": ["code"], "token_endpoint_auth_method": "none"
		}`, w.Body.String())
	})

	for name, tt := range map[string]struct {
		err    error
		body   string
		status int
		want   string
	}{
		"refused":      {err: &oauth.Error{Code: "invalid_redirect_uri", Description: "no"}, status: http.StatusBadRequest, want: `{"error": "invalid_redirect_uri", "error_description": "no"}`},
		"rate limited": {err: domain.ErrRateLimited, status: http.StatusTooManyRequests, want: `{"error": "slow_down", "error_description": "too many attempts"}`},
		"broken":       {err: errors.New("database down"), status: http.StatusInternalServerError, want: `{"error": "server_error"}`},
		"not JSON":     {body: "{", status: http.StatusBadRequest, want: `{"error": "invalid_client_metadata", "error_description": "the body is not a client's metadata"}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, svc := oauthServer(t)

			b := body
			if tt.body != "" {
				b = tt.body
			} else {
				svc.EXPECT().Register(mock.Anything, mock.Anything, mock.Anything).Return(domain.OAuthClient{}, tt.err)
			}

			w := request{method: http.MethodPost, path: "/api/v1/oauth/register", body: b, headers: agentHeaders}.do(t, srv)

			assert.Equal(t, tt.status, w.Code)
			assert.JSONEq(t, tt.want, w.Body.String())
		})
	}
}

func TestOAuthTokenRoute(t *testing.T) {
	form := url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"pc_1"}, "code": {"c0de"}, "redirect_uri": {"http://127.0.0.1/cb"},
		"code_verifier": {"v"}, "resource": {publicURL + "/mcp"},
	}

	t.Run("tokens", func(t *testing.T) {
		srv, _, svc := oauthServer(t)
		svc.EXPECT().Token(mock.Anything, mock.MatchedBy(func(r oauth.TokenRequest) bool {
			return r.GrantType == "authorization_code" && r.ClientID == "pc_1" && r.Code == "c0de" &&
				r.RedirectURI == "http://127.0.0.1/cb" && r.CodeVerifier == "v" && r.Resource == publicURL+"/mcp" && r.IP != ""
		})).Return(oauth.Tokens{Access: "poa_a", Refresh: "por_r", ExpiresIn: time.Hour, Scopes: []domain.Scope{domain.ScopeRead, domain.ScopeBuild}}, nil)

		w := postForm(t, srv, "/api/v1/oauth/token", form, agentHeaders)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "no-store", string(w.Header().Peek("Cache-Control")))
		assert.JSONEq(t, `{"access_token": "poa_a", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "por_r", "scope": "read build"}`, w.Body.String())
	})

	t.Run("refused", func(t *testing.T) {
		srv, _, svc := oauthServer(t)
		svc.EXPECT().Token(mock.Anything, mock.Anything).Return(oauth.Tokens{}, &oauth.Error{Code: "invalid_grant", Description: "the code was used already"})

		w := postForm(t, srv, "/api/v1/oauth/token", form, nil)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, "no-store", string(w.Header().Peek("Cache-Control")))
		assert.JSONEq(t, `{"error": "invalid_grant", "error_description": "the code was used already"}`, w.Body.String())
	})

	t.Run("an unknown client", func(t *testing.T) {
		srv, _, svc := oauthServer(t)
		svc.EXPECT().Token(mock.Anything, mock.Anything).Return(oauth.Tokens{}, &oauth.Error{Code: "invalid_client", Description: "no such client"})

		assert.Equal(t, http.StatusUnauthorized, postForm(t, srv, "/api/v1/oauth/token", form, nil).Code)
	})
}

func TestOAuthRevokeRoute(t *testing.T) {
	srv, _, svc := oauthServer(t)
	svc.EXPECT().Revoke(mock.Anything, "por_r", "pc_1").Return(nil)

	w := postForm(t, srv, "/api/v1/oauth/revoke", url.Values{"token": {"por_r"}, "client_id": {"pc_1"}}, agentHeaders)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
}

const authRequestJSON = `{"response_type": "code", "client_id": "pc_1", "redirect_uri": "http://127.0.0.1:4000/cb", "code_challenge": "ch", "code_challenge_method": "S256", "scope": "read", "state": "st"}`

var authRequest = oauth.AuthRequest{
	ResponseType: "code", ClientID: "pc_1", RedirectURI: "http://127.0.0.1:4000/cb", CodeChallenge: "ch",
	CodeChallengeMethod: "S256", Scope: "read", State: "st",
}

func TestOAuthConsentRoute(t *testing.T) {
	t.Run("signed in", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().Prepare(mock.Anything, mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID }), authRequest).Return(oauth.Consent{
			Client:      domain.OAuthClient{ID: "https://agent.example/client.json", Kind: domain.ClientDocument, Name: "Doc Agent"},
			Verified:    true,
			RedirectURI: "http://127.0.0.1:4000/cb",
			Offered:     []oauth.Offer{{Scope: domain.ScopeRead, Ticked: true}, {Scope: domain.ScopeLive}},
			Allowed:     true,
			Granted:     []domain.Scope{domain.ScopeRead},
		}, nil)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization", body: authRequestJSON, headers: cookie}.do(t, srv)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{
			"client": {"id": "https://agent.example/client.json", "name": "Doc Agent", "verified": true, "site": "agent.example"},
			"redirectHost": "http://127.0.0.1:4000",
			"offered": [{"scope": "read", "ticked": true}, {"scope": "live", "ticked": false}],
			"allowed": true, "granted": ["read"]
		}`, w.Body.String())
	})

	t.Run("a problem", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().Prepare(mock.Anything, mock.Anything, mock.Anything).Return(oauth.Consent{}, &oauth.Error{
			Code: "invalid_scope", Description: "no scope x", Redirect: "http://127.0.0.1:4000/cb?error=invalid_scope",
		})

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization", body: authRequestJSON, headers: cookie}.do(t, srv)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"allowed": false, "problem": {"code": "invalid_scope", "description": "no scope x", "redirect": "http://127.0.0.1:4000/cb?error=invalid_scope"}}`, w.Body.String())
	})

	t.Run("signed out", func(t *testing.T) {
		srv, _, _ := oauthServer(t)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization", body: authRequestJSON}.do(t, srv)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	// The page's own requests are held to the origin check like any other.
	t.Run("from another site", func(t *testing.T) {
		srv, authSvc, _ := oauthServer(t)
		withSession(authSvc, alice)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization", body: authRequestJSON, headers: map[string]string{
			"Cookie": cookie["Cookie"], origin: "https://elsewhere.example",
		}}.do(t, srv)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestOAuthDecisionRoutes(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().Approve(mock.Anything, mock.Anything, authRequest, []domain.Scope{domain.ScopeRead, domain.ScopePaper}).
			Return("http://127.0.0.1:4000/cb?code=c&state=st", nil)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization/approve", body: `{"request": ` + authRequestJSON + `, "scopes": ["read", "paper"]}`, headers: cookie}.do(t, srv)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"redirect": "http://127.0.0.1:4000/cb?code=c&state=st"}`, w.Body.String())
	})

	t.Run("not at a browser", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withToken(authSvc, alice)
		svc.EXPECT().Approve(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", domain.ErrForbidden)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization/approve", body: `{"request": ` + authRequestJSON + `, "scopes": ["read"]}`, headers: bearer}.do(t, srv)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("denied", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().Deny(mock.Anything, mock.Anything, authRequest).Return("http://127.0.0.1:4000/cb?error=access_denied", nil)

		w := request{method: http.MethodPost, path: "/api/v1/oauth/authorization/deny", body: authRequestJSON, headers: cookie}.do(t, srv)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"redirect": "http://127.0.0.1:4000/cb?error=access_denied"}`, w.Body.String())
	})
}

func TestGrantRoutes(t *testing.T) {
	t.Run("listed", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().Grants(mock.Anything, mock.Anything).Return([]domain.Grant{{
			ID: "g-1", Client: domain.OAuthClient{ID: "pc_1", Kind: domain.ClientRegistered, Name: "Agent", ClientURI: "https://agent.example/home"},
			Scopes: []domain.Scope{domain.ScopeRead}, CreatedAt: created, LastUsedAt: created.Add(time.Hour),
		}}, nil)

		w := request{method: http.MethodGet, path: "/api/v1/me/grants", headers: cookie}.do(t, srv)

		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"grants": [{
			"id": "g-1", "client": {"id": "pc_1", "name": "Agent", "verified": false, "site": "agent.example"},
			"scopes": ["read"], "createdAt": "2026-10-08T12:00:00Z", "lastUsedAt": "2026-10-08T13:00:00Z"
		}]}`, w.Body.String())
	})

	t.Run("revoked", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().RevokeGrant(mock.Anything, mock.Anything, domain.GrantID("g-1")).Return(nil)

		w := request{method: http.MethodDelete, path: "/api/v1/me/grants/g-1", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
	})

	t.Run("not found", func(t *testing.T) {
		srv, authSvc, svc := oauthServer(t)
		withSession(authSvc, alice)
		svc.EXPECT().RevokeGrant(mock.Anything, mock.Anything, domain.GrantID("g-2")).Return(domain.ErrNotFound)

		w := request{method: http.MethodDelete, path: "/api/v1/me/grants/g-2", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
