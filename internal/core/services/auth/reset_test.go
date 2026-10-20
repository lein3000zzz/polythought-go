package auth_test

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

const newPass = "a brand new passphrase"

func TestRequestPasswordReset(t *testing.T) {
	request := func(t *testing.T, d deps) error {
		t.Helper()
		return newService(t, d, config(domain.RegistrationOpen)).RequestPasswordReset(context.Background(), " Bob@Example.com ", "1.2.3.4")
	}

	t.Run("mailed, the link saved hashed", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-1", Email: "Bob@Example.com"}, "x", nil)
		d.links.EXPECT().CountSince(mock.Anything, domain.UserID("u-1"), domain.EmailTokenReset, mock.Anything).Return(0, nil).Times(2)

		var hash []byte
		d.links.EXPECT().Create(mock.Anything, mock.MatchedBy(func(t domain.EmailToken) bool {
			return t.Purpose == domain.EmailTokenReset && t.ExpiresAt.Equal(now.Add(time.Hour))
		}), mock.Anything).RunAndReturn(func(_ context.Context, _ domain.EmailToken, h []byte) error {
			hash = h
			return nil
		})
		d.mail.EXPECT().SendPasswordReset(mock.Anything, "Bob@Example.com", mock.Anything, time.Hour).
			RunAndReturn(func(_ context.Context, _, token string, _ time.Duration) error {
				assert.Equal(t, sha(token), hash)
				return nil
			})

		require.NoError(t, request(t, d))
	})

	t.Run("answered before the account is looked up, so as fast for any address (R17)", func(t *testing.T) {
		d := newDeps(t) // the mocks fail the test on a lookup or a mail now
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)

		var after func()
		svc := newService(t, d, config(domain.RegistrationOpen), auth.WithLater(func(f func()) { after = f }))

		require.NoError(t, svc.RequestPasswordReset(context.Background(), "bob@example.com", "1.2.3.4"))
		require.NotNil(t, after, "the rest comes after")

		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{}, "", domain.ErrNotFound).Once()
		after()
	})

	t.Run("the same answer for no account, a disabled one, or too many", func(t *testing.T) {
		for _, setup := range []func(d deps){
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
			},
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{ID: "u-1", DisabledAt: now}, "x", nil)
			},
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{ID: "u-1"}, "x", nil)
				d.links.EXPECT().CountSince(mock.Anything, domain.UserID("u-1"), domain.EmailTokenReset, mock.Anything).Return(1, nil)
			},
		} {
			d := newDeps(t)
			d.allow(true)
			d.mail.EXPECT().SetUp().Return(true)
			setup(d)

			require.NoError(t, request(t, d))
		}
	})

	t.Run("off, or without mail", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(false)
		require.ErrorIs(t, request(t, d), domain.ErrPasswordResetOff)

		d = newDeps(t)
		d.allow(true)
		cfg := config(domain.RegistrationOpen)
		cfg.PasswordReset = false
		err := newService(t, d, cfg).RequestPasswordReset(context.Background(), "bob@example.com", "1.2.3.4")
		require.ErrorIs(t, err, domain.ErrPasswordResetOff)
	})
}

func TestResetPassword(t *testing.T) {
	reset := func(t *testing.T, d deps, token, password string) (string, error) {
		t.Helper()
		return newService(t, d, config(domain.RegistrationOpen)).ResetPassword(context.Background(), token, password, "1.2.3.4")
	}

	t.Run("set, verified, signed out everywhere, told", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.hasher.EXPECT().Hash(newPass).Return("$new", nil)
		d.links.EXPECT().Take(mock.Anything, sha("tok"), domain.EmailTokenReset, now).Return("u-1", nil)
		d.users.EXPECT().ByID(mock.Anything, domain.UserID("u-1")).Return(domain.User{ID: "u-1", Email: "bob@example.com"}, nil)
		d.users.EXPECT().SetPasswordHash(mock.Anything, domain.UserID("u-1"), "$new", now).Return(nil)
		d.users.EXPECT().SetEmailVerified(mock.Anything, domain.UserID("u-1"), now).Return(nil) // D178
		d.links.EXPECT().DeleteForUser(mock.Anything, domain.UserID("u-1"), domain.EmailTokenReset).Return(nil)
		d.sessions.EXPECT().DeleteForUser(mock.Anything, domain.UserID("u-1"), domain.SessionID("")).Return(nil) // D179
		d.mail.EXPECT().SendPasswordChanged(mock.Anything, "bob@example.com", now).Return(nil)

		email, err := reset(t, d, " tok ", newPass)
		require.NoError(t, err)
		assert.Equal(t, "bob@example.com", email)
	})

	t.Run("a weak password leaves the link working", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)

		_, err := reset(t, d, "tok", "short")
		require.ErrorIs(t, err, domain.ErrWeakPassword)
	})

	t.Run("a used or old link", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.hasher.EXPECT().Hash(newPass).Return("$new", nil)
		d.links.EXPECT().Take(mock.Anything, sha("old"), domain.EmailTokenReset, now).Return("", domain.ErrNotFound)

		_, err := reset(t, d, "old", newPass)
		require.ErrorIs(t, err, domain.ErrInvalidLink)
	})

	t.Run("not for a disabled account", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.hasher.EXPECT().Hash(newPass).Return("$new", nil)
		d.links.EXPECT().Take(mock.Anything, sha("tok"), domain.EmailTokenReset, now).Return("u-1", nil)
		d.users.EXPECT().ByID(mock.Anything, domain.UserID("u-1")).Return(domain.User{ID: "u-1", DisabledAt: now}, nil)

		_, err := reset(t, d, "tok", newPass)
		require.ErrorIs(t, err, domain.ErrInvalidLink)
	})

	t.Run("rate limited", func(t *testing.T) {
		d := newDeps(t)
		d.allow(false)

		_, err := reset(t, d, "tok", newPass)
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})
}

func TestPasswordResetAvailable(t *testing.T) {
	d := newDeps(t)
	d.mail.EXPECT().SetUp().Return(true).Once()

	s := newService(t, d, config(domain.RegistrationOpen))
	assert.True(t, s.PasswordReset())

	off := config(domain.RegistrationOpen)
	off.PasswordReset = false
	assert.False(t, newService(t, newDeps(t), off).PasswordReset())

	assert.True(t, auth.DefaultConfig().PasswordReset, "on by default (D176)")
}
