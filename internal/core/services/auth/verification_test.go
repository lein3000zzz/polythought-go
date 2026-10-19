package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

func verifying(mode domain.RegistrationMode) auth.Config {
	cfg := config(mode)
	cfg.VerifyEmail = true

	return cfg
}

// mailed expects a link of purpose verify for user, saved hashed, and the mail carrying
// the token it is the hash of; it returns the token sent.
func mailed(d deps, u domain.UserID, to string, err error) *string {
	var token string
	var hash []byte

	d.links.EXPECT().CountSince(mock.Anything, mock.MatchedBy(func(id domain.UserID) bool { return u == "" || id == u }),
		domain.EmailTokenVerify, mock.Anything).Return(0, nil).Times(2)
	d.links.EXPECT().Create(mock.Anything, mock.MatchedBy(func(t domain.EmailToken) bool {
		return t.Purpose == domain.EmailTokenVerify && t.CreatedAt.Equal(now) && t.ExpiresAt.Equal(now.Add(24*time.Hour))
	}), mock.Anything).RunAndReturn(func(_ context.Context, _ domain.EmailToken, h []byte) error {
		hash = h
		return nil
	})
	d.mail.EXPECT().SendVerification(mock.Anything, to, mock.Anything, 24*time.Hour).
		RunAndReturn(func(_ context.Context, _, tok string, _ time.Duration) error {
			token = tok
			if string(sha(tok)) != string(hash) {
				return errors.New("the link saved is not the one mailed")
			}

			return err
		})

	return &token
}

func TestRegisterVerifying(t *testing.T) {
	unverified := mock.MatchedBy(func(u domain.User) bool {
		return u.Email == "bob@example.com" && u.EmailVerifiedAt.IsZero()
	})

	t.Run("the account waits for its link", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{}, "", domain.ErrNotFound)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, unverified, userHash).Return(nil)
		token := mailed(d, "", "bob@example.com", nil)

		got, err := newService(t, d, verifying(domain.RegistrationOpen)).Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
		require.NoError(t, err)
		assert.True(t, got.MustVerify)
		assert.True(t, got.Sent)
		assert.True(t, got.User.EmailVerifiedAt.IsZero())
		assert.Len(t, *token, 43, "256 bits, base64url")
	})

	t.Run("a failed mail keeps the account (D175)", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, unverified, userHash).Return(nil)
		mailed(d, "", "bob@example.com", domain.ErrMailFailed)

		got, err := newService(t, d, verifying(domain.RegistrationOpen)).Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
		require.NoError(t, err)
		assert.True(t, got.MustVerify)
		assert.False(t, got.Sent)
	})

	t.Run("without mail set up, nobody verifies", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(false)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, mock.MatchedBy(func(u domain.User) bool { return u.EmailVerifiedAt.Equal(now) }), userHash).Return(nil)

		got, err := newService(t, d, verifying(domain.RegistrationOpen)).Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
		require.NoError(t, err)
		assert.False(t, got.MustVerify)
	})

	t.Run("an unverified account of the address is replaced (D174)", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-old", Email: "bob@example.com"}, "x", nil)
		d.users.EXPECT().DeleteUnverified(mock.Anything, domain.UserID("u-old")).Return(nil)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, mock.Anything, userHash).Return(nil)

		_, err := newService(t, d, config(domain.RegistrationOpen)).Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
		require.NoError(t, err)
	})

	t.Run("a verified one is not", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-old", EmailVerifiedAt: now}, "x", nil)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, mock.Anything, userHash).Return(domain.ErrEmailTaken)

		_, err := newService(t, d, config(domain.RegistrationOpen)).Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
		require.ErrorIs(t, err, domain.ErrEmailTaken)
	})
}

func TestLoginUnverified(t *testing.T) {
	pending := domain.User{ID: "u-1", Email: "bob@example.com", Role: domain.RoleUser, CreatedAt: now}

	t.Run("refused after the password, saying why (D166)", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(pending, userHash, nil)
		d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, false, nil)

		_, err := newService(t, d, config(domain.RegistrationOpen)).Login(context.Background(), auth.LoginInput{Email: "bob@example.com", Password: goodPass})
		require.ErrorIs(t, err, domain.ErrEmailNotVerified)
	})

	t.Run("a wrong password reveals nothing", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(pending, userHash, nil)
		d.hasher.EXPECT().Verify("wrong", userHash).Return(false, false, nil)

		_, err := newService(t, d, config(domain.RegistrationOpen)).Login(context.Background(), auth.LoginInput{Email: "bob@example.com", Password: "wrong"})
		require.ErrorIs(t, err, domain.ErrInvalidCredentials)
	})
}

func TestVerifyEmail(t *testing.T) {
	t.Run("verified once, the address given back", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.links.EXPECT().Take(mock.Anything, sha("tok"), domain.EmailTokenVerify, now).Return("u-1", nil)
		d.users.EXPECT().SetEmailVerified(mock.Anything, domain.UserID("u-1"), now).Return(nil)
		d.links.EXPECT().DeleteForUser(mock.Anything, domain.UserID("u-1"), domain.EmailTokenVerify).Return(nil)
		d.users.EXPECT().ByID(mock.Anything, domain.UserID("u-1")).Return(domain.User{ID: "u-1", Email: "bob@example.com"}, nil)

		email, err := newService(t, d, config(domain.RegistrationOpen)).VerifyEmail(context.Background(), " tok ", "1.2.3.4")
		require.NoError(t, err)
		assert.Equal(t, "bob@example.com", email)
	})

	t.Run("a used or old link", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.links.EXPECT().Take(mock.Anything, sha("old"), domain.EmailTokenVerify, now).Return("", domain.ErrNotFound)

		s := newService(t, d, config(domain.RegistrationOpen))
		_, err := s.VerifyEmail(context.Background(), "old", "1.2.3.4")
		require.ErrorIs(t, err, domain.ErrInvalidLink)

		_, err = s.VerifyEmail(context.Background(), "", "1.2.3.4")
		require.ErrorIs(t, err, domain.ErrInvalidLink)
	})

	t.Run("rate limited", func(t *testing.T) {
		d := newDeps(t)
		d.allow(false)

		_, err := newService(t, d, config(domain.RegistrationOpen)).VerifyEmail(context.Background(), "tok", "1.2.3.4")
		require.ErrorIs(t, err, domain.ErrRateLimited)
	})
}

func TestResendVerification(t *testing.T) {
	resend := func(t *testing.T, d deps) error {
		t.Helper()
		return newService(t, d, config(domain.RegistrationOpen)).ResendVerification(context.Background(), " Bob@Example.com ", "1.2.3.4")
	}

	t.Run("to an account that must verify", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-1", Email: "Bob@Example.com"}, "x", nil)
		mailed(d, "u-1", "Bob@Example.com", nil)

		require.NoError(t, resend(t, d))
	})

	t.Run("the same answer for no account, a verified one, or too soon", func(t *testing.T) {
		for _, setup := range []func(d deps){
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
			},
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{ID: "u-1", EmailVerifiedAt: now}, "x", nil)
			},
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{ID: "u-1"}, "x", nil)
				d.links.EXPECT().CountSince(mock.Anything, domain.UserID("u-1"), domain.EmailTokenVerify, now.Add(-time.Minute)).Return(1, nil)
			},
			func(d deps) {
				d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{ID: "u-1"}, "x", nil)
				d.links.EXPECT().CountSince(mock.Anything, domain.UserID("u-1"), domain.EmailTokenVerify, now.Add(-time.Minute)).Return(0, nil)
				d.links.EXPECT().CountSince(mock.Anything, domain.UserID("u-1"), domain.EmailTokenVerify, now.Add(-24*time.Hour)).Return(5, nil)
			},
		} {
			d := newDeps(t)
			d.allow(true)
			d.mail.EXPECT().SetUp().Return(true)
			setup(d)

			require.NoError(t, resend(t, d))
		}
	})

	t.Run("without mail", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(false)

		require.ErrorIs(t, resend(t, d), domain.ErrMailNotSetUp)
	})
}

func TestAdminVerification(t *testing.T) {
	admin := sessionPrincipal(user("root", domain.RoleAdmin))

	t.Run("a link sent again", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByID(mock.Anything, domain.UserID("u-1")).Return(domain.User{ID: "u-1", Email: "bob@example.com"}, nil)
		mailed(d, "u-1", "bob@example.com", nil)

		require.NoError(t, newService(t, d, config(domain.RegistrationOpen)).SendVerificationLink(context.Background(), admin, "u-1"))
	})

	t.Run("not to the verified, nor by others", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByID(mock.Anything, domain.UserID("u-1")).Return(domain.User{ID: "u-1", EmailVerifiedAt: now}, nil)

		s := newService(t, d, config(domain.RegistrationOpen))
		require.ErrorIs(t, s.SendVerificationLink(context.Background(), admin, "u-1"), domain.ErrInvalidInput)
		require.ErrorIs(t, s.SendVerificationLink(context.Background(), sessionPrincipal(user("u-2", domain.RoleUser)), "u-1"), domain.ErrForbidden)
	})

	t.Run("marked verified, never unverified", func(t *testing.T) {
		d := newDeps(t)
		d.users.EXPECT().LockActiveAdmins(mock.Anything).Return(1, nil)
		d.users.EXPECT().ByID(mock.Anything, domain.UserID("u-1")).Return(domain.User{ID: "u-1", Role: domain.RoleUser}, nil)
		d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
		d.users.EXPECT().SetEmailVerified(mock.Anything, domain.UserID("u-1"), now).Return(nil)

		yes, no := true, false
		s := newService(t, d, config(domain.RegistrationOpen))

		got, err := s.UpdateUser(context.Background(), admin, "u-1", auth.UserUpdate{EmailVerified: &yes})
		require.NoError(t, err)
		assert.Equal(t, now, got.EmailVerifiedAt)

		_, err = s.UpdateUser(context.Background(), admin, "u-1", auth.UserUpdate{EmailVerified: &no})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})
}

func TestPruneUnverified(t *testing.T) {
	d := newDeps(t)
	d.users.EXPECT().PruneUnverified(mock.Anything, now.Add(-7*24*time.Hour)).Return(2, nil)
	d.links.EXPECT().DeleteExpired(mock.Anything, now).Return(5, nil)

	n, err := newService(t, d, config(domain.RegistrationOpen)).PruneUnverified(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
}
