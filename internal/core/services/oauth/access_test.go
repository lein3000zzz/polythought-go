package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

func TestVerify(t *testing.T) {
	ctx := context.Background()

	t.Run("an agent's token", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		tokens := f.signIn(t, p, c.ID, domain.ScopeRead, domain.ScopeBuild)

		a, err := f.s.Verify(ctx, tokens.Access)
		require.NoError(t, err)
		assert.Equal(t, p.User.ID, a.Principal.User.ID)
		assert.Equal(t, f.grantOf(t, p, c.ID).ID, a.Principal.GrantID)
		assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopeBuild}, a.Principal.Scopes)
		assert.False(t, a.Principal.ViaSession(), "an agent is never at a browser")
		assert.True(t, a.Principal.Can(domain.ScopeBuild))
		assert.False(t, a.Principal.Can(domain.ScopePaper))
		assert.Equal(t, "Agent", a.Client.Name)
		assert.Equal(t, start.Add(time.Hour), a.ExpiresAt)
	})

	// Its last use is noted, a minute apart at most.
	t.Run("its use noted", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		tokens := f.signIn(t, p, c.ID, domain.ScopeRead)

		f.pass(30 * time.Second)
		_, err := f.s.Verify(ctx, tokens.Access)
		require.NoError(t, err)
		assert.Equal(t, start, f.grantOf(t, p, c.ID).LastUsedAt, "used at sign-in, half a minute ago")

		f.pass(time.Minute)
		_, err = f.s.Verify(ctx, tokens.Access)
		require.NoError(t, err)
		assert.Equal(t, f.at, f.grantOf(t, p, c.ID).LastUsedAt)
	})

	// Allowing the agent less narrows the tokens it has at once.
	t.Run("narrowed", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		tokens := f.signIn(t, p, c.ID, domain.ScopeRead, domain.ScopeBuild)

		req := request(c.ID)
		req.Scope = "paper"
		f.code(t, p, req, domain.ScopeRead, domain.ScopePaper)

		a, err := f.s.Verify(ctx, tokens.Access)
		require.NoError(t, err)
		assert.Equal(t, []domain.Scope{domain.ScopeRead}, a.Principal.Scopes)

		req.Scope = "live" // not granted, so the page asks; the user allows paper alone
		f.code(t, p, req, domain.ScopePaper)

		a, err = f.s.Verify(ctx, tokens.Access)
		require.NoError(t, err)
		assert.Empty(t, a.Principal.Scopes)
		assert.False(t, a.Principal.Can(domain.ScopeRead), "none left is none")
	})

	for name, tt := range map[string]func(f *fixture, p auth.Principal, c domain.OAuthClient, tokens Tokens) string{
		"a refresh token": func(_ *fixture, _ auth.Principal, _ domain.OAuthClient, tokens Tokens) string { return tokens.Refresh },
		"an API token":    func(*fixture, auth.Principal, domain.OAuthClient, Tokens) string { return "pt_abc" },
		"unknown":         func(*fixture, auth.Principal, domain.OAuthClient, Tokens) string { return "poa_unknown" },
		"expired": func(f *fixture, _ auth.Principal, _ domain.OAuthClient, tokens Tokens) string {
			f.pass(time.Hour)
			return tokens.Access
		},
		"revoked": func(f *fixture, p auth.Principal, c domain.OAuthClient, tokens Tokens) string {
			require.NoError(t, f.s.RevokeGrant(context.Background(), p, f.grantOf(t, p, c.ID).ID))
			return tokens.Access
		},
		"its user disabled": func(f *fixture, p auth.Principal, _ domain.OAuthClient, tokens Tokens) string {
			u := p.User
			u.DisabledAt = start
			f.users.ExpectedCalls = nil
			f.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)
			return tokens.Access
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			c := f.register(t)
			p := f.user("u1", domain.RoleUser)
			tokens := f.signIn(t, p, c.ID, domain.ScopeRead)

			_, err := f.s.Verify(ctx, tt(f, p, c, tokens))
			require.ErrorIs(t, err, domain.ErrUnauthenticated)
		})
	}
}

// What admins set on the Config page (D398).
func TestPolicy(t *testing.T) {
	ctx := context.Background()

	t.Run("off", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		tokens := f.signIn(t, p, c.ID, domain.ScopeRead)
		f.policy.Enabled = false

		assert.False(t, f.s.Enabled())

		_, err := f.s.Register(ctx, Registration{RedirectURIs: []string{callback}}, "ip")
		require.ErrorIs(t, err, domain.ErrAgentsOff)
		_, err = f.s.Prepare(ctx, p, request(c.ID))
		require.ErrorIs(t, err, domain.ErrAgentsOff)
		_, err = f.s.Approve(ctx, p, request(c.ID), []domain.Scope{domain.ScopeRead})
		require.ErrorIs(t, err, domain.ErrAgentsOff)
		_, err = f.s.Deny(ctx, p, request(c.ID))
		require.ErrorIs(t, err, domain.ErrAgentsOff)
		_, err = f.s.Token(ctx, refresh(c.ID, tokens.Refresh))
		require.ErrorIs(t, err, domain.ErrAgentsOff)
		require.ErrorIs(t, f.s.Revoke(ctx, tokens.Refresh, c.ID), domain.ErrAgentsOff)
		_, err = f.s.Verify(ctx, tokens.Access)
		require.ErrorIs(t, err, domain.ErrAgentsOff)

		grants, err := f.s.Grants(ctx, p)
		require.NoError(t, err)
		assert.Len(t, grants, 1, "grants stay, to revoke")

		f.policy.Enabled = true
		_, err = f.s.Verify(ctx, tokens.Access)
		require.NoError(t, err, "on again, the agent works again")
	})

	t.Run("no registration", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		f.policy.Registration = false

		_, err := f.s.Register(ctx, Registration{RedirectURIs: []string{callback}}, "ip")
		assert.Equal(t, "access_denied", codeOf(err))
		assert.Empty(t, f.s.Metadata().RegistrationEndpoint, "agents are told to use a metadata document")

		_, err = f.s.Prepare(ctx, f.user("u1", domain.RoleUser), request(c.ID))
		require.NoError(t, err, "agents registered before keep working")
	})

	t.Run("asking every time", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		p := f.user("u1", domain.RoleUser)
		f.code(t, p, request(c.ID), domain.ScopeRead, domain.ScopeBuild)
		f.policy.SkipConsent = false

		req := request(c.ID)
		req.Scope = "read"

		consent, err := f.s.Prepare(ctx, p, req)
		require.NoError(t, err)
		assert.False(t, consent.Allowed)
	})

	t.Run("lifetimes", func(t *testing.T) {
		f := newFixture(t)
		c := f.register(t)
		f.policy.AccessTTL, f.policy.RefreshTTL = 10*time.Minute, 2*time.Hour
		tokens := f.signIn(t, f.user("u1", domain.RoleUser), c.ID, domain.ScopeRead)

		assert.Equal(t, 10*time.Minute, tokens.ExpiresIn)

		access, err := f.repo.Token(ctx, hash(tokens.Access))
		require.NoError(t, err)
		assert.Equal(t, start.Add(10*time.Minute), access.ExpiresAt)

		ref, err := f.repo.Token(ctx, hash(tokens.Refresh))
		require.NoError(t, err)
		assert.Equal(t, start.Add(2*time.Hour), ref.ExpiresAt)
	})
}

func TestResourceMetadata(t *testing.T) {
	m := newFixture(t).s.ResourceMetadata()

	assert.Equal(t, resource, m.Resource)
	assert.Equal(t, []string{issuer}, m.AuthorizationServers)
	assert.Equal(t, []string{"read", "build", "settings", "paper", "live", "admin"}, m.ScopesSupported)
	assert.Equal(t, []string{"header"}, m.BearerMethodsSupported)
}
