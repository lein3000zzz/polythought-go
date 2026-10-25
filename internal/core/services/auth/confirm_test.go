package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// holder holds every action it is asked to, as the confirmations service does where the
// admin has them wait.
type holder struct {
	asks []domain.Ask
}

func (h *holder) Hold(_ context.Context, p auth.Principal, a domain.Ask) error {
	if !p.User.Unconfirmed.Waits(a.Kind) || p.Confirmed {
		return nil
	}

	h.asks = append(h.asks, a)

	return &domain.AwaitingError{Confirmation: domain.Confirmation{ID: "c-1"}}
}

// An admin's agent's work on another user waits for the admin where they say so (D410):
// role changes, disabling, raising caps, code access; lowering caps goes ahead.
func TestAdminWorkWaits(t *testing.T) {
	ctx := context.Background()
	admin := user("admin", domain.RoleAdmin)
	target := user("target", domain.RoleUser)
	agent := auth.Principal{User: admin, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeAdmin}}

	role := func(r domain.Role) *domain.Role { return &r }
	flag := func(b bool) *bool { return &b }
	dollars := func(n int) *int { return &n }

	instance := func() (domain.LiveCaps, domain.AgentCaps) {
		d := decimal.NewFromInt(20)
		return domain.LiveCaps{PerOrder: dollars(100), PerDay: dollars(1000)}, domain.AgentCaps{Dollars: &d}
	}

	held := func(t *testing.T, who auth.Principal, u domain.User, upd auth.UserUpdate) (*holder, error) {
		t.Helper()

		d := newDeps(t)
		h := &holder{}
		svc := newService(t, d, config(domain.RegistrationOpen), auth.WithConfirmations(h), auth.WithInstanceCaps(instance))
		d.users.EXPECT().ByID(mock.Anything, u.ID).Return(u, nil).Maybe()
		d.users.EXPECT().LockActiveAdmins(mock.Anything).Return(2, nil).Maybe()
		d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil).Maybe()
		d.sessions.EXPECT().DeleteForUser(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		d.workflows.EXPECT().StopAll(mock.Anything, mock.Anything).Return(nil).Maybe()
		d.live.EXPECT().StopLive(mock.Anything, mock.Anything).Return(nil).Maybe()

		_, err := svc.UpdateUser(ctx, who, u.ID, upd)

		return h, err
	}

	for name, upd := range map[string]auth.UserUpdate{
		"made an admin":          {Role: role(domain.RoleAdmin)},
		"disabled":               {Disabled: flag(true)},
		"its caps raised":        {LiveCaps: &domain.LiveCaps{PerOrder: dollars(500)}},
		"its cap removed":        {LiveCaps: &domain.LiveCaps{PerDay: dollars(0)}},
		"its agent spend raised": {AgentCaps: &domain.AgentCaps{Dollars: new(decimal.NewFromInt(50))}},
		"let to write code":      {CodeChosen: flag(true)},
	} {
		t.Run(name+", it waits", func(t *testing.T) {
			h, err := held(t, agent, target, upd)
			require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)
			require.Len(t, h.asks, 1)
			assert.Equal(t, "target@example.com", h.asks[0].About, "whom it is about, for the admin")
			assert.NotContains(t, h.asks[0].What, "@", "never the agent's to see")
			assert.Equal(t, auth.UserAsked{User: target.ID, Update: upd}, h.asks[0].Payload)
		})
	}

	t.Run("caps lowered, a role as it was, code taken away: at once", func(t *testing.T) {
		for _, upd := range []auth.UserUpdate{
			{LiveCaps: &domain.LiveCaps{PerOrder: dollars(50)}},
			{Role: role(domain.RoleUser)},
			{CodeChosen: flag(false)},
		} {
			h, err := held(t, agent, target, upd)
			require.NoError(t, err)
			assert.Empty(t, h.asks)
		}
	})

	t.Run("several at once, as one", func(t *testing.T) {
		h, err := held(t, agent, target, auth.UserUpdate{Role: role(domain.RoleAdmin), CodeChosen: flag(true)})
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)
		require.Len(t, h.asks, 1)
		assert.Equal(t, domain.ConfirmRole, h.asks[0].Kind)
		assert.Equal(t, "Change the user target: make them an admin, let them write code", h.asks[0].What)
	})

	t.Run("saying all it does, what alone would not wait too (A19)", func(t *testing.T) {
		coder := target
		coder.CodeChosen, coder.EmailVerifiedAt = true, time.Time{}

		h, err := held(t, agent, coder, auth.UserUpdate{
			Role: role(domain.RoleAdmin), EmailVerified: flag(true), CodeChosen: flag(false),
			LiveCaps: &domain.LiveCaps{PerOrder: dollars(10)},
		})
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)
		require.Len(t, h.asks, 1)
		assert.Equal(t, "Change the user target: make them an admin, mark their address verified, "+
			"no longer let them write code, lower their own caps", h.asks[0].What)

		h, err = held(t, agent, target, auth.UserUpdate{Role: role(domain.RoleAdmin), LiveCaps: &domain.LiveCaps{}})
		require.ErrorIs(t, err, domain.ErrAwaitsConfirmation)
		assert.Equal(t, "Change the user target: make them an admin", h.asks[0].What, "caps as they were, by value")
	})

	t.Run("where the admin lets it go, and the admin's own", func(t *testing.T) {
		lets := agent
		lets.User.Unconfirmed.Role = true

		h, err := held(t, lets, target, auth.UserUpdate{Role: role(domain.RoleAdmin)})
		require.NoError(t, err)
		assert.Empty(t, h.asks)

		h, err = held(t, sessionPrincipal(admin), target, auth.UserUpdate{Disabled: flag(true)})
		require.NoError(t, err)
		assert.Empty(t, h.asks)

		confirmed := agent
		confirmed.Confirmed = true

		h, err = held(t, confirmed, target, auth.UserUpdate{Disabled: flag(true)})
		require.NoError(t, err)
		assert.Empty(t, h.asks, "the admin confirmed it")
	})
}

// An admin's agent invites rather than sets passwords, unless the instance lets it, and
// makes no one an admin at once; it finds an account by the email it names (D410).
func TestAgentsOnAccounts(t *testing.T) {
	ctx := context.Background()
	admin := user("admin", domain.RoleAdmin)
	agent := auth.Principal{User: admin, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeAdmin}}

	passwords := func(on bool) auth.Config {
		cfg := config(domain.RegistrationClosed)
		cfg.AgentPasswords = on

		return cfg
	}

	t.Run("no passwords where the instance has them off", func(t *testing.T) {
		svc := newService(t, newDeps(t), passwords(false))

		_, err := svc.CreateUser(ctx, agent, "new@example.com", goodPass, domain.RoleUser)
		require.ErrorIs(t, err, domain.ErrForbidden)
		assert.Contains(t, err.Error(), "agents and tokens invite people here")

		_, err = svc.CreateUser(ctx, tokenPrincipal(admin), "new@example.com", goodPass, domain.RoleUser)
		require.ErrorIs(t, err, domain.ErrForbidden, "nor a token")
	})

	t.Run("a user where it has them on, never an admin", func(t *testing.T) {
		d := newDeps(t)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, mock.Anything, userHash).Return(nil)
		svc := newService(t, d, passwords(true))

		got, err := svc.CreateUser(ctx, agent, "new@example.com", goodPass, domain.RoleUser)
		require.NoError(t, err)
		assert.Equal(t, domain.RoleUser, got.Role)

		_, err = svc.CreateUser(ctx, agent, "boss@example.com", goodPass, domain.RoleAdmin)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "create them as a user, then make them an admin")
	})

	t.Run("an account by its email, for admins", func(t *testing.T) {
		d := newDeps(t)
		bob := user("bob", domain.RoleUser)
		d.users.EXPECT().ByEmail(mock.Anything, "Bob@Example.com").Return(bob, "", nil) // the repository folds its case
		svc := newService(t, d, passwords(false))

		got, err := svc.FindUser(ctx, agent, " Bob@Example.com ")
		require.NoError(t, err)
		assert.Equal(t, bob.ID, got.ID)

		_, err = svc.FindUser(ctx, agent, "not an email")
		require.ErrorIs(t, err, domain.ErrInvalidEmail)

		_, err = svc.FindUser(ctx, sessionPrincipal(user("u", domain.RoleUser)), "bob@example.com")
		require.ErrorIs(t, err, domain.ErrForbidden)
	})
}
