package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

func TestLogin(t *testing.T) {
	u := user("u1", domain.RoleUser)
	disabled := u
	disabled.DisabledAt = now.Add(-time.Hour)

	login := auth.LoginInput{Email: " U1@Example.com ", Password: goodPass, IP: "1.2.3.4", UserAgent: "test"}

	tests := []struct {
		name    string
		cfg     func(*auth.Config)
		limited bool
		setup   func(d deps)
		wantErr error
		check   func(t *testing.T, res auth.LoginResult)
	}{
		{
			name: "opens a session",
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, "u1@example.com").Return(u, userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, false, nil)
				d.sessions.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
					RunAndReturn(func(_ context.Context, s domain.Session, hash []byte) error {
						assert.Len(t, hash, 32, "only the token's hash is stored")
						return nil
					})
			},
			check: func(t *testing.T, res auth.LoginResult) {
				assert.Equal(t, u, res.User)
				assert.Len(t, res.Token, 43, "256 bits, base64url")
				assert.Equal(t, now.Add(7*24*time.Hour), res.Session.ExpiresAt)
				assert.Equal(t, now.Add(30*24*time.Hour), res.Session.AbsoluteExpiresAt)
				assert.Equal(t, "1.2.3.4", res.Session.IP)
			},
		},
		{
			name: "idle expiry never passes the absolute one",
			cfg: func(c *auth.Config) {
				c.SessionIdleTTL = 48 * time.Hour
				c.SessionAbsoluteTTL = 24 * time.Hour
			},
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(u, userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, false, nil)
				d.sessions.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil)
			},
			check: func(t *testing.T, res auth.LoginResult) {
				assert.Equal(t, res.Session.AbsoluteExpiresAt, res.Session.ExpiresAt)
			},
		},
		{
			name: "unknown email costs a hash and says nothing more",
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
				d.hasher.EXPECT().Verify(goodPass, dummyHash).Return(false, false, nil).Once()
			},
			wantErr: domain.ErrInvalidCredentials,
		},
		{
			name: "wrong password",
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(u, userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(false, false, nil)
			},
			wantErr: domain.ErrInvalidCredentials,
		},
		{
			name: "disabled account with the right password",
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(disabled, userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, false, nil)
			},
			wantErr: domain.ErrUserDisabled,
		},
		{
			name: "disabled account with a wrong password is not revealed",
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(disabled, userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(false, false, nil)
			},
			wantErr: domain.ErrInvalidCredentials,
		},
		{
			name: "outdated hash is upgraded",
			setup: func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(u, userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, true, nil)
				d.hasher.EXPECT().Hash(goodPass).Return("$stronger", nil)
				d.users.EXPECT().SetPasswordHash(mock.Anything, u.ID, "$stronger", now).Return(nil)
				d.sessions.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil)
			},
		},
		{
			name:    "rate limited",
			limited: true,
			setup:   func(deps) {},
			wantErr: domain.ErrRateLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			d.allow(!tt.limited)
			tt.setup(d)

			cfg := config(domain.RegistrationOpen)
			if tt.cfg != nil {
				tt.cfg(&cfg)
			}

			res, err := newService(t, d, cfg).Login(context.Background(), login)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			if tt.check != nil {
				tt.check(t, res)
			}
		})
	}
}

func TestLoginLimits(t *testing.T) {
	ctx := context.Background()
	login := auth.LoginInput{Email: "Bob@Example.com", Password: "pw", IP: "1.2.3.4"}

	t.Run("an IP stopped charges nothing else (R4)", func(t *testing.T) {
		d := newDeps(t) // the mock fails the test on any other key
		d.limiter.EXPECT().Allow("login:ip:1.2.3.4").Return(false).Once()

		_, err := newService(t, d, config(domain.RegistrationOpen)).Login(ctx, login)
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})

	t.Run("an address longer than any account's is refused before it is charged (R4)", func(t *testing.T) {
		d := newDeps(t)
		d.limiter.EXPECT().Allow("login:ip:1.2.3.4").Return(true).Once()

		long := login
		long.Email = strings.Repeat("a", 300) + "@example.com"

		_, err := newService(t, d, config(domain.RegistrationOpen)).Login(ctx, long)
		require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	})

	t.Run("few guesses at an address from one IP", func(t *testing.T) {
		d := newDeps(t)
		d.limiter.EXPECT().Allow("login:ip:1.2.3.4").Return(true).Once()
		d.limiter.EXPECT().Allow("login:ip-email:1.2.3.4 bob@example.com").Return(false).Once()

		_, err := newService(t, d, config(domain.RegistrationOpen)).Login(ctx, login)
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})

	t.Run("and more from all IPs together, with a limiter of its own (R9)", func(t *testing.T) {
		d := newDeps(t)
		d.limiter.EXPECT().Allow(mock.Anything).Return(true).Twice()

		accounts := portsmocks.NewMockRateLimiter(t)
		accounts.EXPECT().Allow("login:email:bob@example.com").Return(false).Once()

		_, err := newService(t, d, config(domain.RegistrationOpen), auth.WithAccountLimiter(accounts)).Login(ctx, login)
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})
}

func TestAuthenticateSession(t *testing.T) {
	u := user("u1", domain.RoleUser)
	disabled := u
	disabled.DisabledAt = now

	session := func(lastSeen, expires, absolute time.Duration) domain.Session {
		return domain.Session{
			ID: "sess-1", UserID: u.ID,
			LastSeenAt: now.Add(lastSeen), ExpiresAt: now.Add(expires), AbsoluteExpiresAt: now.Add(absolute),
		}
	}

	tests := []struct {
		name    string
		token   string
		setup   func(d deps)
		wantErr error
	}{
		{
			name:  "fresh session is not touched",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, sha("tok")).Return(session(-10*time.Second, time.Hour, 24*time.Hour), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)
			},
		},
		{
			name:  "stale session slides forward",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(session(-time.Hour, time.Hour, 30*24*time.Hour), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)
				d.sessions.EXPECT().Touch(mock.Anything, domain.SessionID("sess-1"), now, now.Add(7*24*time.Hour)).Return(nil)
			},
		},
		{
			name:  "sliding stops at the absolute expiry",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(session(-time.Hour, time.Hour, 2*time.Hour), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)
				d.sessions.EXPECT().Touch(mock.Anything, domain.SessionID("sess-1"), now, now.Add(2*time.Hour)).Return(nil)
			},
		},
		{
			name:    "no token",
			setup:   func(deps) {},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:  "unknown token",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(domain.Session{}, domain.ErrNotFound)
			},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:  "idle expiry passed",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(session(-8*24*time.Hour, -time.Second, time.Hour), nil)
				d.sessions.EXPECT().Delete(mock.Anything, domain.SessionID("sess-1")).Return(nil)
			},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:  "absolute expiry passed",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(session(-time.Minute, time.Hour, 0), nil)
				d.sessions.EXPECT().Delete(mock.Anything, domain.SessionID("sess-1")).Return(nil)
			},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:  "user disabled since login",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(session(0, time.Hour, time.Hour), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(disabled, nil)
			},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:  "user deleted since login",
			token: "tok",
			setup: func(d deps) {
				d.sessions.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(session(0, time.Hour, time.Hour), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(domain.User{}, domain.ErrNotFound)
			},
			wantErr: domain.ErrUnauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			p, err := newService(t, d, config(domain.RegistrationOpen)).AuthenticateSession(context.Background(), tt.token)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, u, p.User)
			assert.True(t, p.ViaSession())
		})
	}
}

func TestAuthenticateToken(t *testing.T) {
	u := user("u1", domain.RoleUser)

	token := func(lastUsed time.Duration, expires time.Time) domain.APIToken {
		return domain.APIToken{ID: "tok-1", UserID: u.ID, LastUsedAt: now.Add(lastUsed), ExpiresAt: expires}
	}

	tests := []struct {
		name    string
		secret  string
		setup   func(d deps)
		wantErr error
	}{
		{
			name:   "recently used token",
			secret: "pt_abc",
			setup: func(d deps) {
				d.tokens.EXPECT().ByTokenHash(mock.Anything, sha("pt_abc")).Return(token(-time.Second, time.Time{}), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)
			},
		},
		{
			name:   "use is recorded",
			secret: "pt_abc",
			setup: func(d deps) {
				d.tokens.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(token(-time.Hour, now.Add(time.Hour)), nil)
				d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)
				d.tokens.EXPECT().TouchLastUsed(mock.Anything, domain.APITokenID("tok-1"), now).Return(nil)
			},
		},
		{
			name:    "not a token",
			secret:  "session-looking-thing",
			setup:   func(deps) {},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:   "unknown",
			secret: "pt_abc",
			setup: func(d deps) {
				d.tokens.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(domain.APIToken{}, domain.ErrNotFound)
			},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:   "expired",
			secret: "pt_abc",
			setup: func(d deps) {
				d.tokens.EXPECT().ByTokenHash(mock.Anything, mock.Anything).Return(token(0, now), nil)
			},
			wantErr: domain.ErrUnauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			p, err := newService(t, d, config(domain.RegistrationOpen)).AuthenticateToken(context.Background(), tt.secret)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.False(t, p.ViaSession())
			assert.Equal(t, domain.APITokenID("tok-1"), p.TokenID)
		})
	}
}

// A token's scopes come with it (D398).
func TestAuthenticateTokenScopes(t *testing.T) {
	u := user("u1", domain.RoleUser)
	d := newDeps(t)
	d.tokens.EXPECT().ByTokenHash(mock.Anything, sha("pt_abc")).
		Return(domain.APIToken{ID: "tok-1", UserID: u.ID, LastUsedAt: now, Scopes: []domain.Scope{domain.ScopeRead}}, nil)
	d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil)

	p, err := newService(t, d, config(domain.RegistrationOpen)).AuthenticateToken(context.Background(), "pt_abc")
	require.NoError(t, err)
	assert.True(t, p.Can(domain.ScopeRead))
	assert.False(t, p.Can(domain.ScopeBuild))
}

// A session has every scope, as has the user in their Telegram chat and a token made
// before scopes; a scoped token and an agent have theirs, each with read, and an agent
// with none left has none (D399).
func TestPrincipalCan(t *testing.T) {
	u := user("u1", domain.RoleUser)

	for name, tt := range map[string]struct {
		p    auth.Principal
		want map[domain.Scope]bool
	}{
		"a session":             {sessionPrincipal(u), map[domain.Scope]bool{domain.ScopeRead: true, domain.ScopeLive: true}},
		"a token from before":   {tokenPrincipal(u), map[domain.Scope]bool{domain.ScopeRead: true, domain.ScopeLive: true, domain.ScopeSettings: true}},
		"the user, in Telegram": {auth.Principal{User: u}, map[domain.Scope]bool{domain.ScopePaper: true, domain.ScopeLive: true}},
		"a scoped token": {
			auth.Principal{User: u, TokenID: "t", Scopes: []domain.Scope{domain.ScopePaper}},
			map[domain.Scope]bool{domain.ScopeRead: true, domain.ScopePaper: true, domain.ScopeLive: false, domain.ScopeSettings: false},
		},
		"an agent": {
			auth.Principal{User: u, GrantID: "g", Scopes: []domain.Scope{domain.ScopeBuild}},
			map[domain.Scope]bool{domain.ScopeBuild: true, domain.ScopeRead: true, domain.ScopePaper: false},
		},
		"an agent with none left": {
			auth.Principal{User: u, GrantID: "g", Scopes: []domain.Scope{}},
			map[domain.Scope]bool{domain.ScopeRead: false},
		},
	} {
		for sc, want := range tt.want {
			assert.Equal(t, want, tt.p.Can(sc), "%s: %s", name, sc)
		}
	}
}

func TestLogoutAndPrune(t *testing.T) {
	tests := []struct {
		name  string
		setup func(d deps)
		call  func(s *auth.Service) error
	}{
		{
			name: "logout deletes the session",
			setup: func(d deps) {
				d.sessions.EXPECT().Delete(mock.Anything, domain.SessionID("sess-1")).Return(nil)
			},
			call: func(s *auth.Service) error { return s.Logout(context.Background(), "sess-1") },
		},
		{
			name: "logout of a gone session is fine",
			setup: func(d deps) {
				d.sessions.EXPECT().Delete(mock.Anything, mock.Anything).Return(domain.ErrNotFound)
			},
			call: func(s *auth.Service) error { return s.Logout(context.Background(), "sess-1") },
		},
		{
			name: "prune uses the clock",
			setup: func(d deps) {
				d.sessions.EXPECT().DeleteExpired(mock.Anything, now).Return(3, nil)
			},
			call: func(s *auth.Service) error {
				n, err := s.PruneSessions(context.Background())
				assert.Equal(t, int64(3), n)

				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			require.NoError(t, tt.call(newService(t, d, config(domain.RegistrationOpen))))
		})
	}
}
