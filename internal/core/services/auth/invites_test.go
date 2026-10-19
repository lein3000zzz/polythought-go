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

func TestInviteByEmail(t *testing.T) {
	admin := sessionPrincipal(user("root", domain.RoleAdmin))

	invite := func(t *testing.T, d deps, email string) (string, domain.Invite, bool, error) {
		t.Helper()
		return newService(t, d, config(domain.RegistrationInvite)).InviteByEmail(context.Background(), admin, email)
	}

	t.Run("tied to the address, mailed naming the admin", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "new@example.com").Return(domain.User{}, "", domain.ErrNotFound)

		var hash []byte
		d.invites.EXPECT().Create(mock.Anything, mock.MatchedBy(func(i domain.Invite) bool {
			return i.Email == "New@Example.com" && i.CreatedBy == "root" && i.ExpiresAt.Equal(now.Add(7*24*time.Hour))
		}), mock.Anything).RunAndReturn(func(_ context.Context, _ domain.Invite, h []byte) error {
			hash = h
			return nil
		})
		d.mail.EXPECT().SendInvite(mock.Anything, "New@Example.com", "root@example.com", mock.Anything, now.Add(7*24*time.Hour)).
			RunAndReturn(func(_ context.Context, _, _, code string, _ time.Time) error {
				assert.Equal(t, sha(code), hash)
				return nil
			})

		code, inv, sent, err := invite(t, d, " New@Example.com ")
		require.NoError(t, err)
		assert.True(t, sent)
		assert.Equal(t, "New@Example.com", inv.Email)
		assert.NotEmpty(t, code)
	})

	t.Run("a failed mail leaves the invite, to copy", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.invites.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil)
		d.mail.EXPECT().SendInvite(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrMailFailed)

		code, _, sent, err := invite(t, d, "new@example.com")
		require.NoError(t, err)
		assert.False(t, sent)
		assert.NotEmpty(t, code)
	})

	t.Run("not to an address with an account (D183)", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-1", EmailVerifiedAt: now}, "x", nil)

		_, _, _, err := invite(t, d, "bob@example.com")
		require.ErrorIs(t, err, domain.ErrEmailTaken)
		assert.ErrorContains(t, err, "bob@example.com has an account already")
	})

	t.Run("to one whose account never verified", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-1"}, "x", nil)
		d.invites.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).Return(nil)
		d.mail.EXPECT().SendInvite(mock.Anything, "bob@example.com", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		_, _, sent, err := invite(t, d, "bob@example.com")
		require.NoError(t, err)
		assert.True(t, sent)
	})

	t.Run("refused", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(false)
		_, _, _, err := invite(t, d, "new@example.com")
		require.ErrorIs(t, err, domain.ErrInviteEmailOff)

		d = newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		_, _, _, err = invite(t, d, "not an address")
		require.ErrorIs(t, err, domain.ErrInvalidEmail)

		_, _, _, err = newService(t, newDeps(t), config(domain.RegistrationInvite)).
			InviteByEmail(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), "new@example.com")
		require.ErrorIs(t, err, domain.ErrForbidden)
	})
}

func TestRegisterWithAMailedInvite(t *testing.T) {
	tied := domain.Invite{ID: "i-1", Email: "New@Example.com"}

	t.Run("its address, verified though sign-ups verify (D181)", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true).Maybe()
		d.invites.EXPECT().ByCode(mock.Anything, sha("inv_1"), now).Return(tied, nil)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, mock.MatchedBy(func(u domain.User) bool { return u.EmailVerifiedAt.Equal(now) }), userHash).Return(nil)
		d.invites.EXPECT().Consume(mock.Anything, sha("inv_1"), mock.Anything, now).Return(nil)

		got, err := newService(t, d, verifying(domain.RegistrationInvite)).Register(context.Background(),
			auth.RegisterInput{Email: "new@example.com", Password: goodPass, InviteCode: "inv_1"})
		require.NoError(t, err)
		assert.False(t, got.MustVerify, "no link to send")
	})

	t.Run("not another address", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.invites.EXPECT().ByCode(mock.Anything, sha("inv_1"), now).Return(tied, nil)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)

		_, err := newService(t, d, config(domain.RegistrationInvite)).Register(context.Background(),
			auth.RegisterInput{Email: "other@example.com", Password: goodPass, InviteCode: "inv_1"})
		require.ErrorIs(t, err, domain.ErrInvalidInvite)
		assert.ErrorContains(t, err, "it is for New@Example.com")
	})
}

func TestInviteFor(t *testing.T) {
	d := newDeps(t)
	d.allow(true)
	d.invites.EXPECT().ByCode(mock.Anything, sha("inv_1"), now).Return(domain.Invite{ID: "i-1", Email: "new@example.com"}, nil)
	d.invites.EXPECT().ByCode(mock.Anything, sha("used"), now).Return(domain.Invite{}, domain.ErrInvalidInvite)

	s := newService(t, d, config(domain.RegistrationInvite))

	got, err := s.InviteFor(context.Background(), " inv_1 ", "1.2.3.4")
	require.NoError(t, err)
	assert.Equal(t, "new@example.com", got.Email)

	_, err = s.InviteFor(context.Background(), "used", "1.2.3.4")
	require.ErrorIs(t, err, domain.ErrInvalidInvite)

	_, err = s.InviteFor(context.Background(), "", "1.2.3.4")
	require.ErrorIs(t, err, domain.ErrInvalidInvite)
}
