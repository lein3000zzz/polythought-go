package auth_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	authmocks "github.com/lein3000zzz/polythought-go/internal/mocks/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const (
	dummyHash = "$dummy"
	userHash  = "$hash-of-user-password"
	goodPass  = "correct horse battery"
)

var now = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

type deps struct {
	users    *portsmocks.MockUserRepository
	sessions *portsmocks.MockSessionRepository
	tokens   *portsmocks.MockAPITokenRepository
	invites  *portsmocks.MockInviteRepository
	links    *portsmocks.MockEmailTokenRepository
	mail     *portsmocks.MockAccountMail
	tx       *portsmocks.MockTransactor
	hasher   *portsmocks.MockPasswordHasher
	limiter  *portsmocks.MockRateLimiter

	workflows *authmocks.MockWorkflowStopper
	live      *authmocks.MockLiveStopper
}

func newDeps(t *testing.T) deps {
	t.Helper()

	d := deps{
		users:    portsmocks.NewMockUserRepository(t),
		sessions: portsmocks.NewMockSessionRepository(t),
		tokens:   portsmocks.NewMockAPITokenRepository(t),
		invites:  portsmocks.NewMockInviteRepository(t),
		links:    portsmocks.NewMockEmailTokenRepository(t),
		mail:     portsmocks.NewMockAccountMail(t),
		tx:       portsmocks.NewMockTransactor(t),
		hasher:   portsmocks.NewMockPasswordHasher(t),
		limiter:  portsmocks.NewMockRateLimiter(t),

		workflows: authmocks.NewMockWorkflowStopper(t),
		live:      authmocks.NewMockLiveStopper(t),
	}

	d.tx.EXPECT().WithinTx(mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }).
		Maybe()

	return d
}

// allow sets the rate limiter's answer for every key.
func (d deps) allow(ok bool) {
	d.limiter.EXPECT().Allow(mock.Anything).Return(ok).Maybe()
}

func newService(t *testing.T, d deps, cfg auth.Config) *auth.Service {
	t.Helper()

	d.hasher.EXPECT().Hash("polythought-dummy-password").Return(dummyHash, nil).Once()

	s, err := auth.New(cfg, auth.Repositories{
		Users: d.users, Sessions: d.sessions, Tokens: d.tokens, Invites: d.invites, EmailTokens: d.links, Tx: d.tx,
	}, d.hasher, d.limiter, logging.Noop(),
		auth.WithClock(func() time.Time { return now }),
		auth.WithWorkflowStopper(d.workflows),
		auth.WithLiveStopper(d.live),
		auth.WithAccountMail(d.mail),
	)
	require.NoError(t, err)

	return s
}

func config(mode domain.RegistrationMode) auth.Config {
	cfg := auth.DefaultConfig()
	cfg.Registration = mode

	return cfg
}

func sha(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func user(id string, role domain.Role) domain.User {
	return domain.User{ID: domain.UserID(id), Email: id + "@example.com", Role: role, EmailVerifiedAt: now, CreatedAt: now}
}

func sessionPrincipal(u domain.User) auth.Principal {
	return auth.Principal{User: u, SessionID: "sess-1"}
}

func tokenPrincipal(u domain.User) auth.Principal {
	return auth.Principal{User: u, TokenID: "tok-1"}
}
