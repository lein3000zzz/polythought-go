package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const (
	issuer       = "https://pt.example"
	resource     = "https://pt.example/mcp"
	callback     = "http://127.0.0.1/callback"
	docURL       = "https://agent.example/client.json"
	pkceVerifier = "a-verifier-of-forty-three-characters-or-more_~."
)

var start = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

type limiter struct {
	refuse bool
	keys   []string
}

func (l *limiter) Allow(key string) bool {
	l.keys = append(l.keys, key)
	return !l.refuse
}

type fixture struct {
	s       *Service
	repo    *fakeRepo
	docs    *portsmocks.MockClientDocuments
	users   *portsmocks.MockUserRepository
	limiter *limiter
	at      time.Time
	policy  Policy
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		repo: newFakeRepo(), docs: portsmocks.NewMockClientDocuments(t), users: portsmocks.NewMockUserRepository(t),
		limiter: &limiter{}, at: start, policy: DefaultPolicy(),
	}

	f.s = New(Config{Issuer: issuer + "/", Resource: resource}, f.repo, f.docs, f.users, f.limiter, logging.Noop(),
		WithPolicy(func() Policy { return f.policy }))
	f.s.now = func() time.Time { return f.at }

	return f
}

func (f *fixture) pass(d time.Duration) { f.at = f.at.Add(d) }

// user is a user the service can look up, signed in at a browser.
func (f *fixture) user(id string, role domain.Role) auth.Principal {
	u := domain.User{ID: domain.UserID(id), Email: id + "@example.com", Role: role}
	f.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil).Maybe()

	return auth.Principal{User: u, SessionID: "sess-" + domain.SessionID(id)}
}

// register registers a client with the callback.
func (f *fixture) register(t *testing.T) domain.OAuthClient {
	t.Helper()

	c, err := f.s.Register(context.Background(), Registration{ClientName: "Agent", RedirectURIs: []string{callback}}, "1.2.3.4")
	require.NoError(t, err)

	return c
}

// signIn allows the client scopes and exchanges the code for tokens.
func (f *fixture) signIn(t *testing.T, p auth.Principal, client string, scopes ...domain.Scope) Tokens {
	t.Helper()

	tokens, err := f.s.Token(context.Background(), exchange(client, f.code(t, p, request(client), scopes...)))
	require.NoError(t, err)

	return tokens
}

// code allows the request and gives the code its redirect carries.
func (f *fixture) code(t *testing.T, p auth.Principal, req AuthRequest, scopes ...domain.Scope) string {
	t.Helper()

	to, err := f.s.Approve(context.Background(), p, req, scopes)
	require.NoError(t, err)

	u, err := url.Parse(to)
	require.NoError(t, err)

	return u.Query().Get("code")
}

func (f *fixture) grantOf(t *testing.T, p auth.Principal, client string) domain.Grant {
	t.Helper()

	g, err := f.repo.GrantOf(context.Background(), p.User.ID, client)
	require.NoError(t, err)

	return g
}

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func request(client string) AuthRequest {
	return AuthRequest{
		ResponseType: "code", ClientID: client, RedirectURI: callback, CodeChallenge: challengeOf(pkceVerifier),
		CodeChallengeMethod: "S256", State: "st-1", Resource: resource,
	}
}

func exchange(client, code string) TokenRequest {
	return TokenRequest{
		GrantType: "authorization_code", ClientID: client, Code: code, RedirectURI: callback,
		CodeVerifier: pkceVerifier, Resource: resource, IP: "1.2.3.4",
	}
}

func refresh(client, token string) TokenRequest {
	return TokenRequest{GrantType: "refresh_token", ClientID: client, RefreshToken: token, IP: "1.2.3.4"}
}

// codeOf is an OAuth error's code, "" for any other error.
func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}

	return ""
}

func TestMetadata(t *testing.T) {
	m := newFixture(t).s.Metadata()

	assert.Equal(t, issuer, m.Issuer, "the issuer has no trailing slash")
	assert.Equal(t, issuer+"/oauth/authorize", m.AuthorizationEndpoint)
	assert.Equal(t, issuer+"/api/v1/oauth/token", m.TokenEndpoint)
	assert.Equal(t, issuer+"/api/v1/oauth/register", m.RegistrationEndpoint)
	assert.Equal(t, issuer+"/api/v1/oauth/revoke", m.RevocationEndpoint)
	assert.Equal(t, []string{"read", "build", "tasks", "settings", "paper", "live", "admin"}, m.ScopesSupported)
	assert.Equal(t, []string{"S256"}, m.CodeChallengeMethods)
	assert.Equal(t, []string{"none"}, m.TokenAuthMethods)
	assert.True(t, m.ClientIDDocuments)
	assert.True(t, m.AuthorizationIssuer)
}

func TestRegister(t *testing.T) {
	ctx := context.Background()

	t.Run("a client", func(t *testing.T) {
		f := newFixture(t)

		c, err := f.s.Register(ctx, Registration{
			ClientName: "  Claude\u202e Code\x07 ", RedirectURIs: []string{callback, "cursor://anysphere.cursor/callback"},
			GrantTypes: []string{"authorization_code", "refresh_token"}, ResponseTypes: []string{"code"},
			ClientURI: "https://agent.example",
		}, "1.2.3.4")
		require.NoError(t, err)

		assert.True(t, strings.HasPrefix(c.ID, "pc_"), c.ID)
		assert.Equal(t, domain.ClientRegistered, c.Kind)
		assert.Equal(t, "Claude Code", c.Name, "no controls or bidi overrides")
		assert.Equal(t, "https://agent.example", c.ClientURI)
		assert.Equal(t, start, c.CreatedAt)
		assert.Equal(t, []string{"oauth-register:ip:1.2.3.4"}, f.limiter.keys)

		kept, err := f.repo.Client(ctx, c.ID)
		require.NoError(t, err)
		assert.Equal(t, c, kept)
	})

	t.Run("names", func(t *testing.T) {
		f := newFixture(t)

		c, err := f.s.Register(ctx, Registration{RedirectURIs: []string{callback}, ClientURI: "http://agent.example"}, "ip")
		require.NoError(t, err)
		assert.Equal(t, "An unnamed agent", c.Name)
		assert.Empty(t, c.ClientURI, "a homepage only over https")

		c, err = f.s.Register(ctx, Registration{RedirectURIs: []string{callback}, ClientURI: "https://agent.example/" + strings.Repeat("a", 1<<20)}, "ip")
		require.NoError(t, err)
		assert.Empty(t, c.ClientURI, "nor one too long to be one (A13)")

		c, err = f.s.Register(ctx, Registration{ClientName: strings.Repeat("é", 150), RedirectURIs: []string{callback}}, "ip")
		require.NoError(t, err)
		assert.Equal(t, strings.Repeat("é", 100), c.Name)
	})

	t.Run("rate limited", func(t *testing.T) {
		f := newFixture(t)
		f.limiter.refuse = true

		_, err := f.s.Register(ctx, Registration{RedirectURIs: []string{callback}}, "ip")
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})

	for name, tt := range map[string]struct {
		reg  Registration
		code string
	}{
		"no redirects":        {Registration{}, "invalid_redirect_uri"},
		"too many redirects":  {Registration{RedirectURIs: slices.Repeat([]string{callback}, 11)}, "invalid_redirect_uri"},
		"http elsewhere":      {Registration{RedirectURIs: []string{"http://agent.example/cb"}}, "invalid_redirect_uri"},
		"a script":            {Registration{RedirectURIs: []string{"javascript:alert(1)"}}, "invalid_redirect_uri"},
		"another script":      {Registration{RedirectURIs: []string{"JavaScript:alert(1)"}}, "invalid_redirect_uri"},
		"data":                {Registration{RedirectURIs: []string{"data:text/html,hi"}}, "invalid_redirect_uri"},
		"a fragment":          {Registration{RedirectURIs: []string{"https://agent.example/cb#x"}}, "invalid_redirect_uri"},
		"a user":              {Registration{RedirectURIs: []string{"https://me@agent.example/cb"}}, "invalid_redirect_uri"},
		"no host":             {Registration{RedirectURIs: []string{"https:///cb"}}, "invalid_redirect_uri"},
		"too long":            {Registration{RedirectURIs: []string{"https://agent.example/" + strings.Repeat("a", 2048)}}, "invalid_redirect_uri"},
		"no scheme":           {Registration{RedirectURIs: []string{"/callback"}}, "invalid_redirect_uri"},
		"implicit only":       {Registration{RedirectURIs: []string{callback}, GrantTypes: []string{"implicit"}}, "invalid_client_metadata"},
		"token response type": {Registration{RedirectURIs: []string{callback}, ResponseTypes: []string{"token"}}, "invalid_client_metadata"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newFixture(t).s.Register(ctx, tt.reg, "ip")
			assert.Equal(t, tt.code, codeOf(err), err)
		})
	}
}

// A loopback redirect may name any port, as a CLI agent picks one as it starts; nothing
// else may differ.
func TestMatchesRedirect(t *testing.T) {
	c := domain.OAuthClient{RedirectURIs: []string{"http://127.0.0.1/callback", "http://localhost:3000/cb?x=1", "https://agent.example/cb"}}

	for asked, want := range map[string]bool{
		"http://127.0.0.1/callback":       true,
		"http://127.0.0.1:53682/callback": true,
		"http://localhost:9999/cb?x=1":    true,
		"https://agent.example/cb":        true,
		"http://127.0.0.1:53682/other":    false,
		"http://localhost:9999/cb":        false,
		"http://[::1]:53682/callback":     false,
		"https://agent.example:8443/cb":   false,
		"https://agent.example/cb/":       false,
		"http://agent.example/cb":         false,
	} {
		assert.Equal(t, want, matchesRedirect(c, asked), asked)
	}
}

// A client may be the metadata document at its URL: fetched, checked, kept a day.
func TestClientDocument(t *testing.T) {
	ctx := context.Background()
	doc := `{"client_id": "` + docURL + `", "client_name": "Doc Agent", "redirect_uris": ["` + callback + `"], "client_uri": "https://agent.example"}`

	t.Run("fetched and kept a day", func(t *testing.T) {
		f := newFixture(t)
		p := f.user("u1", domain.RoleUser)
		f.docs.EXPECT().Fetch(mock.Anything, docURL).Return([]byte(doc), nil).Once()

		c, err := f.s.Prepare(ctx, p, request(docURL))
		require.NoError(t, err)
		assert.True(t, c.Verified)
		assert.Equal(t, "Doc Agent", c.Client.Name)
		assert.Equal(t, "https://agent.example", c.Client.ClientURI)

		f.pass(23 * time.Hour)
		_, err = f.s.Prepare(ctx, p, request(docURL))
		require.NoError(t, err, "kept, not fetched")

		f.pass(2 * time.Hour)
		f.docs.EXPECT().Fetch(mock.Anything, docURL).Return([]byte(strings.Replace(doc, "Doc Agent", "Renamed", 1)), nil).Once()

		c, err = f.s.Prepare(ctx, p, request(docURL))
		require.NoError(t, err)
		assert.Equal(t, "Renamed", c.Client.Name)
		assert.Equal(t, start, c.Client.CreatedAt, "still the client it was")
		assert.Equal(t, f.at, c.Client.FetchedAt)
	})

	for name, tt := range map[string]struct {
		id    string
		body  string
		fails bool
	}{
		"another client's":   {id: docURL, body: strings.Replace(doc, docURL, "https://evil.example/client.json", 1)},
		"not JSON":           {id: docURL, body: "<html>"},
		"no redirects":       {id: docURL, body: `{"client_id": "` + docURL + `"}`},
		"not fetched":        {id: docURL, fails: true},
		"no such registered": {id: "pc_unknown"},
		"not https":          {id: "http://agent.example/client.json"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			p := f.user("u1", domain.RoleUser)

			switch {
			case tt.fails:
				f.docs.EXPECT().Fetch(mock.Anything, tt.id).Return(nil, errDown).Once()
			case tt.body != "":
				f.docs.EXPECT().Fetch(mock.Anything, tt.id).Return([]byte(tt.body), nil).Once()
			}

			_, err := f.s.Prepare(ctx, p, request(tt.id))

			var e *Error
			require.True(t, errors.As(err, &e), err)
			assert.Contains(t, []string{"invalid_client", "invalid_redirect_uri"}, e.Code)
			assert.Empty(t, e.Redirect, "shown on the page, never sent to the client")
		})
	}

	t.Run("the database down", func(t *testing.T) {
		f := newFixture(t)
		p := f.user("u1", domain.RoleUser)
		f.repo.fail = errDown

		_, err := f.s.Prepare(ctx, p, request(docURL))
		require.ErrorIs(t, err, errDown)
	})

	t.Run("fetched only so often, per user and per host, and not again soon after failing (A12)", func(t *testing.T) {
		f := newFixture(t)
		p := f.user("u1", domain.RoleUser)
		f.docs.EXPECT().Fetch(mock.Anything, docURL).Return(nil, errDown).Once()

		_, err := f.s.Prepare(ctx, p, request(docURL))
		require.Error(t, err)
		assert.Equal(t, []string{"oauth-fetch:user:u1", "oauth-fetch:host:agent.example"}, f.limiter.keys)

		_, err = f.s.Prepare(ctx, p, request(docURL))
		require.ErrorContains(t, err, "could not be fetched a moment ago", "not fetched again")

		f.pass(6 * time.Minute)
		f.docs.EXPECT().Fetch(mock.Anything, docURL).Return([]byte(doc), nil).Once()

		_, err = f.s.Prepare(ctx, p, request(docURL))
		require.NoError(t, err, "tried again a while after")

		f.limiter.refuse = true
		_, err = f.s.Prepare(ctx, p, request("https://other.example/client.json"))
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})
}

func TestPrepare(t *testing.T) {
	ctx := context.Background()

	t.Run("what is offered", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)

		offered := func(p auth.Principal, scope string) []Offer {
			req := request(c.ID)
			req.Scope = scope

			consent, err := f.s.Prepare(ctx, p, req)
			require.NoError(t, err)
			assert.False(t, consent.Verified, "a registered client's name is its own word")
			assert.Equal(t, callback, consent.RedirectURI)
			assert.False(t, consent.Allowed)

			return consent.Offered
		}

		user, admin := f.user("u1", domain.RoleUser), f.user("a1", domain.RoleAdmin)

		assert.Equal(t, []Offer{
			{domain.ScopeRead, true}, {domain.ScopeBuild, true}, {domain.ScopeTasks, false}, {domain.ScopeSettings, false},
			{domain.ScopePaper, true}, {domain.ScopeLive, false},
		}, offered(user, ""), "everything but tasks, settings and live, no admin for a user")

		assert.Equal(t, []Offer{
			{domain.ScopeRead, true}, {domain.ScopeBuild, false}, {domain.ScopeTasks, false}, {domain.ScopeSettings, false},
			{domain.ScopePaper, false}, {domain.ScopeLive, false},
		}, offered(user, "read live settings tasks"), "what it asks for, live, settings and tasks never ticked")

		assert.Equal(t, []Offer{
			{domain.ScopeRead, false}, {domain.ScopeBuild, false}, {domain.ScopeTasks, false}, {domain.ScopeSettings, false},
			{domain.ScopePaper, false}, {domain.ScopeLive, false}, {domain.ScopeAdmin, false},
		}, offered(admin, "admin"), "admin offered to an admin, unticked")
	})

	t.Run("allowed before", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		f.code(t, p, request(c.ID), domain.ScopeRead, domain.ScopeBuild)

		prepare := func(scope string) Consent {
			req := request(c.ID)
			req.Scope = scope

			consent, err := f.s.Prepare(ctx, p, req)
			require.NoError(t, err)

			return consent
		}

		got := prepare("")
		assert.True(t, got.Allowed)
		assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopeBuild}, got.Granted, "all it was granted")

		got = prepare("build")
		assert.True(t, got.Allowed)
		assert.Equal(t, []domain.Scope{domain.ScopeBuild}, got.Granted)

		assert.False(t, prepare("read paper").Allowed, "more than was granted")

		require.NoError(t, f.s.RevokeGrant(ctx, p, f.grantOf(t, p, c.ID).ID))
		assert.False(t, prepare("read").Allowed, "revoked")
	})

	t.Run("a grant for another resource", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)

		_, err := f.repo.SaveGrant(ctx, domain.Grant{ID: "g-old", UserID: p.User.ID, Client: c, Scopes: []domain.Scope{domain.ScopeRead}, Resource: "https://old.example/mcp"})
		require.NoError(t, err)

		req := request(c.ID)
		req.Scope = "read"

		consent, err := f.s.Prepare(ctx, p, req)
		require.NoError(t, err)
		assert.False(t, consent.Allowed)
	})

	t.Run("errors shown on the page", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)

		for name, change := range map[string]func(*AuthRequest){
			"no client":         func(r *AuthRequest) { r.ClientID = "" },
			"no redirect":       func(r *AuthRequest) { r.RedirectURI = "" },
			"another redirect":  func(r *AuthRequest) { r.RedirectURI = "https://evil.example/cb" },
			"another loopback":  func(r *AuthRequest) { r.RedirectURI = "http://127.0.0.1:8080/other" },
			"unknown client ID": func(r *AuthRequest) { r.ClientID = "pc_nobody" },
		} {
			req := request(c.ID)
			change(&req)

			_, err := f.s.Prepare(ctx, p, req)

			var e *Error
			require.True(t, errors.As(err, &e), name)
			assert.Empty(t, e.Redirect, name)
		}
	})

	t.Run("errors sent back to the client", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)

		for name, tt := range map[string]struct {
			change func(*AuthRequest)
			code   string
		}{
			"a token response":   {func(r *AuthRequest) { r.ResponseType = "token" }, "unsupported_response_type"},
			"no PKCE":            {func(r *AuthRequest) { r.CodeChallenge, r.CodeChallengeMethod = "", "" }, "invalid_request"},
			"plain PKCE":         {func(r *AuthRequest) { r.CodeChallengeMethod = "plain" }, "invalid_request"},
			"a short challenge":  {func(r *AuthRequest) { r.CodeChallenge = "abc" }, "invalid_request"},
			"another resource":   {func(r *AuthRequest) { r.Resource = "https://other.example/mcp" }, "invalid_target"},
			"an unknown scope":   {func(r *AuthRequest) { r.Scope = "read everything" }, "invalid_scope"},
			"a loopback on port": {func(r *AuthRequest) { r.RedirectURI, r.ResponseType = "http://127.0.0.1:4242/callback", "x" }, "unsupported_response_type"},
		} {
			req := request(c.ID)
			tt.change(&req)

			_, err := f.s.Prepare(ctx, p, req)

			var e *Error
			require.True(t, errors.As(err, &e), name)
			assert.Equal(t, tt.code, e.Code, name)

			back, err := url.Parse(e.Redirect)
			require.NoError(t, err, name)
			assert.Equal(t, strings.Split(req.RedirectURI, "?")[0], back.Scheme+"://"+back.Host+back.Path, name)
			assert.Equal(t, tt.code, back.Query().Get("error"), name)
			assert.Equal(t, "st-1", back.Query().Get("state"), name)
			assert.Equal(t, issuer, back.Query().Get("iss"), name)
		}
	})

	t.Run("a trailing slash on the resource", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		req := request(c.ID)
		req.Resource = resource + "/"

		_, err := f.s.Prepare(ctx, f.user("u1", domain.RoleUser), req)
		require.NoError(t, err)
	})
}

func TestApprove(t *testing.T) {
	ctx := context.Background()

	t.Run("allowed", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)

		to, err := f.s.Approve(ctx, p, request(c.ID), []domain.Scope{domain.ScopePaper, domain.ScopeRead, domain.ScopePaper})
		require.NoError(t, err)

		u, err := url.Parse(to)
		require.NoError(t, err)
		assert.Equal(t, callback, u.Scheme+"://"+u.Host+u.Path)
		assert.NotEmpty(t, u.Query().Get("code"))
		assert.Equal(t, "st-1", u.Query().Get("state"))
		assert.Equal(t, issuer, u.Query().Get("iss"))

		g := f.grantOf(t, p, c.ID)
		assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopePaper}, g.Scopes, "in order, once each")
		assert.Equal(t, resource, g.Resource)
		assert.True(t, g.Active())
	})

	t.Run("only at a browser", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		p.SessionID, p.TokenID = "", "tok-1"

		_, err := f.s.Approve(ctx, p, request(c.ID), []domain.Scope{domain.ScopeRead})
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("scopes", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)

		_, err := f.s.Approve(ctx, p, request(c.ID), nil)
		assert.Equal(t, "invalid_scope", codeOf(err), "none")

		_, err = f.s.Approve(ctx, p, request(c.ID), []domain.Scope{domain.ScopeRead, domain.ScopeAdmin})
		assert.Equal(t, "invalid_scope", codeOf(err), "admin, for a user")

		_, err = f.s.Approve(ctx, p, request(c.ID), []domain.Scope{"root"})
		assert.Equal(t, "invalid_scope", codeOf(err))

		_, err = f.repo.GrantOf(ctx, p.User.ID, c.ID)
		require.ErrorIs(t, err, domain.ErrNotFound, "nothing granted")
	})

	// An agent asking for less than it was granted gets that, and its grant keeps the rest:
	// another of the user's machines may be using it.
	t.Run("allowed before, asking for less", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		f.code(t, p, request(c.ID), domain.ScopeRead, domain.ScopeBuild, domain.ScopePaper)

		req := request(c.ID)
		req.Scope = "read"

		consent, err := f.s.Prepare(ctx, p, req)
		require.NoError(t, err)
		require.True(t, consent.Allowed)

		tokens, err := f.s.Token(ctx, exchange(c.ID, f.code(t, p, req, consent.Granted...)))
		require.NoError(t, err)
		assert.Equal(t, []domain.Scope{domain.ScopeRead}, tokens.Scopes)
		assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopeBuild, domain.ScopePaper}, f.grantOf(t, p, c.ID).Scopes)
	})

	// What the user ticks on the page replaces what they allowed before.
	t.Run("allowed again on the page", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		f.code(t, p, request(c.ID), domain.ScopeRead, domain.ScopeBuild)

		req := request(c.ID)
		req.Scope = "paper"
		f.code(t, p, req, domain.ScopeRead, domain.ScopePaper)

		assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopePaper}, f.grantOf(t, p, c.ID).Scopes)
	})

	t.Run("denied", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)

		to, err := f.s.Deny(ctx, f.user("u1", domain.RoleUser), request(c.ID))
		require.NoError(t, err)

		u, err := url.Parse(to)
		require.NoError(t, err)
		assert.Equal(t, "access_denied", u.Query().Get("error"))
		assert.Equal(t, "st-1", u.Query().Get("state"))
		assert.Equal(t, issuer, u.Query().Get("iss"))
		assert.Empty(t, u.Query().Get("code"))

		req := request(c.ID)
		req.RedirectURI = "https://evil.example/cb"
		_, err = f.s.Deny(ctx, f.user("u1", domain.RoleUser), req)
		assert.Equal(t, "invalid_request", codeOf(err), "never sent to a redirect the client did not register")
	})
}

func TestExchange(t *testing.T) {
	ctx := context.Background()

	t.Run("tokens", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)

		tokens := f.signIn(t, p, c.ID, domain.ScopeRead, domain.ScopeBuild)

		assert.True(t, strings.HasPrefix(tokens.Access, "poa_"), tokens.Access)
		assert.True(t, strings.HasPrefix(tokens.Refresh, "por_"), tokens.Refresh)
		assert.Equal(t, time.Hour, tokens.ExpiresIn)
		assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopeBuild}, tokens.Scopes)

		access, err := f.repo.Token(ctx, hash(tokens.Access))
		require.NoError(t, err)
		assert.Equal(t, domain.AccessToken, access.Kind)
		assert.Equal(t, start.Add(time.Hour), access.ExpiresAt)

		ref, err := f.repo.Token(ctx, hash(tokens.Refresh))
		require.NoError(t, err)
		assert.Equal(t, domain.RefreshToken, ref.Kind)
		assert.Equal(t, start.Add(30*24*time.Hour), ref.ExpiresAt)

		assert.Equal(t, start, f.grantOf(t, p, c.ID).LastUsedAt)
		assert.Contains(t, f.limiter.keys, "oauth-token:ip:1.2.3.4")
	})

	// A code used twice was copied: the grant goes, with the tokens the first use got.
	t.Run("a code used twice", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		code := f.code(t, p, request(c.ID), domain.ScopeRead)

		_, err := f.s.Token(ctx, exchange(c.ID, code))
		require.NoError(t, err)

		_, err = f.s.Token(ctx, exchange(c.ID, code))
		assert.Equal(t, "invalid_grant", codeOf(err))

		g := f.grantOf(t, p, c.ID)
		assert.False(t, g.Active())
		assert.Zero(t, f.repo.tokensOf(g.ID, domain.AccessToken))
		assert.Zero(t, f.repo.tokensOf(g.ID, domain.RefreshToken))
	})

	for name, tt := range map[string]struct {
		change func(*TokenRequest)
		code   string
	}{
		"no verifier":       {func(r *TokenRequest) { r.CodeVerifier = "" }, "invalid_request"},
		"no redirect":       {func(r *TokenRequest) { r.RedirectURI = "" }, "invalid_request"},
		"another verifier":  {func(r *TokenRequest) { r.CodeVerifier = strings.Repeat("b", 43) }, "invalid_grant"},
		"a short verifier":  {func(r *TokenRequest) { r.CodeVerifier = "short" }, "invalid_grant"},
		"another redirect":  {func(r *TokenRequest) { r.RedirectURI = "http://127.0.0.1:9/callback" }, "invalid_grant"},
		"an unknown code":   {func(r *TokenRequest) { r.Code = "nope" }, "invalid_grant"},
		"no client":         {func(r *TokenRequest) { r.ClientID = "" }, "invalid_request"},
		"another resource":  {func(r *TokenRequest) { r.Resource = "https://other.example/mcp" }, "invalid_target"},
		"an unknown grant":  {func(r *TokenRequest) { r.GrantType = "password" }, "unsupported_grant_type"},
		"another client":    {func(r *TokenRequest) { r.ClientID = "pc_other" }, "invalid_grant"},
		"no resource is ok": {func(r *TokenRequest) { r.Resource = "" }, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			c := f.register(t)
			req := exchange(c.ID, f.code(t, f.user("u1", domain.RoleUser), request(c.ID), domain.ScopeRead))
			tt.change(&req)

			_, err := f.s.Token(ctx, req)
			if tt.code == "" {
				require.NoError(t, err)
				return
			}

			assert.Equal(t, tt.code, codeOf(err), err)
		})
	}

	t.Run("an expired code", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		code := f.code(t, f.user("u1", domain.RoleUser), request(c.ID), domain.ScopeRead)
		f.pass(10 * time.Minute)

		_, err := f.s.Token(ctx, exchange(c.ID, code))
		assert.Equal(t, "invalid_grant", codeOf(err))
	})

	t.Run("a disabled user", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		u := domain.User{ID: "u1", Role: domain.RoleUser}
		code := f.code(t, auth.Principal{User: u, SessionID: "s"}, request(c.ID), domain.ScopeRead)

		u.DisabledAt = start
		f.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil).Once()

		_, err := f.s.Token(ctx, exchange(c.ID, code))
		assert.Equal(t, "invalid_grant", codeOf(err))
	})

	t.Run("a revoked grant", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		code := f.code(t, p, request(c.ID), domain.ScopeRead)
		require.NoError(t, f.s.RevokeGrant(ctx, p, f.grantOf(t, p, c.ID).ID))

		_, err := f.s.Token(ctx, exchange(c.ID, code))
		assert.Equal(t, "invalid_grant", codeOf(err), "its codes went with it")
	})

	t.Run("rate limited", func(t *testing.T) {
		f := newFixture(t)
		f.limiter.refuse = true

		_, err := f.s.Token(ctx, exchange("pc_x", "code"))
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})
}

func TestRefresh(t *testing.T) {
	ctx := context.Background()

	t.Run("rotated", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		first := f.signIn(t, p, c.ID, domain.ScopeRead, domain.ScopeBuild)
		f.pass(time.Hour)

		second, err := f.s.Token(ctx, refresh(c.ID, first.Refresh))
		require.NoError(t, err)
		assert.NotEqual(t, first.Access, second.Access)
		assert.NotEqual(t, first.Refresh, second.Refresh)
		assert.Equal(t, first.Scopes, second.Scopes)
		assert.Equal(t, start.Add(time.Hour), f.grantOf(t, p, c.ID).LastUsedAt)

		third, err := f.s.Token(ctx, refresh(c.ID, second.Refresh))
		require.NoError(t, err)
		assert.NotEqual(t, second.Refresh, third.Refresh)
	})

	// A refresh token used twice was copied: the grant goes, with every token of it.
	t.Run("used twice", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		first := f.signIn(t, p, c.ID, domain.ScopeRead)

		second, err := f.s.Token(ctx, refresh(c.ID, first.Refresh))
		require.NoError(t, err)

		_, err = f.s.Token(ctx, refresh(c.ID, first.Refresh))
		assert.Equal(t, "invalid_grant", codeOf(err))

		g := f.grantOf(t, p, c.ID)
		assert.False(t, g.Active())

		_, err = f.s.Token(ctx, refresh(c.ID, second.Refresh))
		assert.Equal(t, "invalid_grant", codeOf(err), "the newer one went too")
	})

	t.Run("asking for less", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		first := f.signIn(t, f.user("u1", domain.RoleUser), c.ID, domain.ScopeRead, domain.ScopeBuild)

		req := refresh(c.ID, first.Refresh)
		req.Scope = "read"

		less, err := f.s.Token(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, []domain.Scope{domain.ScopeRead}, less.Scopes)

		req = refresh(c.ID, less.Refresh)
		req.Scope = "read build"

		_, err = f.s.Token(ctx, req)
		assert.Equal(t, "invalid_scope", codeOf(err), "never more than its token had")
	})

	t.Run("the grant narrowed since", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		first := f.signIn(t, p, c.ID, domain.ScopeRead, domain.ScopeBuild)

		req := request(c.ID)
		req.Scope = "paper"
		f.code(t, p, req, domain.ScopeRead, domain.ScopePaper) // the user allowed less

		got, err := f.s.Token(ctx, refresh(c.ID, first.Refresh))
		require.NoError(t, err)
		assert.Equal(t, []domain.Scope{domain.ScopeRead}, got.Scopes)
	})

	t.Run("nothing left", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		first := f.signIn(t, p, c.ID, domain.ScopeBuild)

		req := request(c.ID)
		req.Scope = "paper"
		f.code(t, p, req, domain.ScopePaper)

		_, err := f.s.Token(ctx, refresh(c.ID, first.Refresh))
		assert.Equal(t, "invalid_scope", codeOf(err))
	})

	for name, tt := range map[string]struct {
		change func(f *fixture, r *TokenRequest, tokens Tokens)
		code   string
	}{
		"expired":          {func(f *fixture, _ *TokenRequest, _ Tokens) { f.pass(30 * 24 * time.Hour) }, "invalid_grant"},
		"no token":         {func(_ *fixture, r *TokenRequest, _ Tokens) { r.RefreshToken = "" }, "invalid_request"},
		"unknown":          {func(_ *fixture, r *TokenRequest, _ Tokens) { r.RefreshToken = "por_nope" }, "invalid_grant"},
		"another client":   {func(_ *fixture, r *TokenRequest, _ Tokens) { r.ClientID = "pc_other" }, "invalid_grant"},
		"an access token":  {func(_ *fixture, r *TokenRequest, t Tokens) { r.RefreshToken = t.Access }, "invalid_grant"},
		"an unknown scope": {func(_ *fixture, r *TokenRequest, _ Tokens) { r.Scope = "root" }, "invalid_scope"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			c := f.register(t)
			tokens := f.signIn(t, f.user("u1", domain.RoleUser), c.ID, domain.ScopeRead)

			req := refresh(c.ID, tokens.Refresh)
			tt.change(f, &req, tokens)

			_, err := f.s.Token(ctx, req)
			assert.Equal(t, tt.code, codeOf(err), err)
		})
	}
}

func TestRevoke(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := f.register(t)
	p := f.user("u1", domain.RoleUser)
	tokens := f.signIn(t, p, c.ID, domain.ScopeRead)

	require.NoError(t, f.s.Revoke(ctx, "poa_unknown", c.ID), "the same answer")
	require.NoError(t, f.s.Revoke(ctx, tokens.Access, "pc_other"))
	assert.True(t, f.grantOf(t, p, c.ID).Active(), "not another client's")

	require.NoError(t, f.s.Revoke(ctx, tokens.Refresh, c.ID))

	g := f.grantOf(t, p, c.ID)
	assert.False(t, g.Active())
	assert.Zero(t, f.repo.tokensOf(g.ID, domain.AccessToken), "the access token went too")
}

func TestGrants(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	c := f.register(t)
	p, other := f.user("u1", domain.RoleUser), f.user("u2", domain.RoleUser)
	tokens := f.signIn(t, p, c.ID, domain.ScopeRead)
	g := f.grantOf(t, p, c.ID)

	list, err := f.s.Grants(ctx, p)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "Agent", list[0].Client.Name)

	list, err = f.s.Grants(ctx, other)
	require.NoError(t, err)
	assert.Empty(t, list)

	require.ErrorIs(t, f.s.RevokeGrant(ctx, other, g.ID), domain.ErrNotFound, "another user's")
	require.NoError(t, f.s.RevokeGrant(ctx, p, g.ID))

	_, err = f.s.Token(ctx, refresh(c.ID, tokens.Refresh))
	assert.Equal(t, "invalid_grant", codeOf(err), "its tokens stop working at once")
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	used, unused := f.register(t), f.register(t)
	f.signIn(t, f.user("u1", domain.RoleUser), used.ID, domain.ScopeRead)

	f.pass(25 * time.Hour) // the access token ended; the refresh token has not

	n, err := f.s.Prune(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "the used code, the access token, the unused client")

	_, err = f.repo.Client(ctx, unused.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)

	_, err = f.repo.Client(ctx, used.ID)
	require.NoError(t, err)
}
