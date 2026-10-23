package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

func TestInviteByEmail(t *testing.T) {
	admin := sessionPrincipal(user("root", domain.RoleAdmin))

	invite := func(t *testing.T, d deps, email string) (domain.Invite, bool, error) {
		t.Helper()
		return newService(t, d, config(domain.RegistrationInvite)).InviteByEmail(context.Background(), admin, email)
	}

	t.Run("tied to the address, mailed naming the admin", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "new@example.com").Return(domain.User{}, "", domain.ErrNotFound)

		var hash []byte
		d.invites.EXPECT().Create(mock.Anything, mock.MatchedBy(func(i domain.Invite) bool {
			return i.Email == "New@Example.com" && i.CreatedBy == "root" && i.ExpiresAt.Equal(now.Add(7*24*time.Hour)) &&
				i.MaxUses == 1 // once, for that address (D371)
		}), mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, _ domain.Invite, h, _ []byte) error {
			hash = h
			return nil
		})
		d.mail.EXPECT().SendInvite(mock.Anything, "New@Example.com", "root@example.com", mock.Anything, now.Add(7*24*time.Hour)).
			RunAndReturn(func(_ context.Context, _, _, code string, _ time.Time) error {
				assert.Equal(t, sha(strings.ToLower(code)), hash, "found any case (D393)")
				return nil
			})

		inv, sent, err := invite(t, d, " New@Example.com ")
		require.NoError(t, err)
		assert.True(t, sent)
		assert.Equal(t, "New@Example.com", inv.Email)
		assert.True(t, strings.HasPrefix(inv.Code, "inv_"))
	})

	t.Run("a failed mail leaves the invite, to copy", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.invites.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		d.mail.EXPECT().SendInvite(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrMailFailed)

		inv, sent, err := invite(t, d, "new@example.com")
		require.NoError(t, err)
		assert.False(t, sent)
		assert.NotEmpty(t, inv.Code)
	})

	t.Run("not to an address with an account (D183)", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-1", EmailVerifiedAt: now}, "x", nil)

		_, _, err := invite(t, d, "bob@example.com")
		require.ErrorIs(t, err, domain.ErrEmailTaken)
		assert.ErrorContains(t, err, "bob@example.com has an account already")
	})

	t.Run("to one whose account never verified", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{ID: "u-1"}, "x", nil)
		d.invites.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		d.mail.EXPECT().SendInvite(mock.Anything, "bob@example.com", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		_, sent, err := invite(t, d, "bob@example.com")
		require.NoError(t, err)
		assert.True(t, sent)
	})

	t.Run("refused", func(t *testing.T) {
		d := newDeps(t)
		d.mail.EXPECT().SetUp().Return(false)
		_, _, err := invite(t, d, "new@example.com")
		require.ErrorIs(t, err, domain.ErrInviteEmailOff)

		d = newDeps(t)
		d.mail.EXPECT().SetUp().Return(true)
		_, _, err = invite(t, d, "not an address")
		require.ErrorIs(t, err, domain.ErrInvalidEmail)

		_, _, err = newService(t, newDeps(t), config(domain.RegistrationInvite)).
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
		d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("inv_1")}, now).Return(tied, nil)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
		d.users.EXPECT().Create(mock.Anything, mock.MatchedBy(func(u domain.User) bool { return u.EmailVerifiedAt.Equal(now) }), userHash).Return(nil)
		d.invites.EXPECT().Consume(mock.Anything, domain.InviteID("i-1"), mock.Anything, now).Return(nil)

		got, err := newService(t, d, verifying(domain.RegistrationInvite)).Register(context.Background(),
			auth.RegisterInput{Email: "new@example.com", Password: goodPass, InviteCode: "inv_1"})
		require.NoError(t, err)
		assert.False(t, got.MustVerify, "no link to send")
	})

	t.Run("not another address", func(t *testing.T) {
		d := newDeps(t)
		d.allow(true)
		d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("inv_1")}, now).Return(tied, nil)
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
	d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("inv_1")}, now).Return(domain.Invite{ID: "i-1", Email: "new@example.com"}, nil)
	d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("used")}, now).Return(domain.Invite{}, domain.ErrInvalidInvite)

	s := newService(t, d, config(domain.RegistrationInvite))

	got, err := s.InviteFor(context.Background(), " inv_1 ", "1.2.3.4")
	require.NoError(t, err)
	assert.Equal(t, "new@example.com", got.Email)

	_, err = s.InviteFor(context.Background(), "used", "1.2.3.4")
	require.ErrorIs(t, err, domain.ErrInvalidInvite)

	_, err = s.InviteFor(context.Background(), "", "1.2.3.4")
	require.ErrorIs(t, err, domain.ErrInvalidInvite)
}

func TestCreateInvite(t *testing.T) {
	admin := sessionPrincipal(user("root", domain.RoleAdmin))

	t.Run("a typed code, any case, sealed to copy again (D393)", func(t *testing.T) {
		d := newDeps(t)
		box := portsmocks.NewMockSecretBox(t)
		d.invites.EXPECT().Taken(mock.Anything, [][]byte{sha("spring-2026"), sha("Spring-2026")}).Return(false, nil)
		box.EXPECT().Seal(mock.Anything, []byte("Spring-2026"), mock.MatchedBy(func(aad []byte) bool {
			return strings.HasPrefix(string(aad), "invite:")
		})).Return([]byte("sealed"), nil)
		d.invites.EXPECT().Create(mock.Anything, mock.MatchedBy(func(i domain.Invite) bool {
			return i.MaxUses == 25 && i.ExpiresAt.Equal(now.Add(30*24*time.Hour)) && i.CreatedBy == "root" && i.Email == ""
		}), sha("spring-2026"), []byte("sealed")).Return(nil)

		inv, err := newService(t, d, config(domain.RegistrationInvite), auth.WithInviteBox(box)).CreateInvite(context.Background(), admin,
			auth.InviteSpec{Code: " Spring-2026 ", MaxUses: 25, ExpiresAt: now.Add(30 * 24 * time.Hour)})
		require.NoError(t, err)
		assert.Equal(t, "Spring-2026", inv.Code)
	})

	t.Run("one made, one use, for Invite lifetime, unless said", func(t *testing.T) {
		d := newDeps(t)
		d.invites.EXPECT().Create(mock.Anything, mock.MatchedBy(func(i domain.Invite) bool {
			return i.MaxUses == 1 && i.ExpiresAt.Equal(now.Add(7*24*time.Hour))
		}), mock.Anything, []byte(nil)).Return(nil) // no box: the code shows only now

		inv, err := newService(t, d, config(domain.RegistrationInvite)).CreateInvite(context.Background(), admin, auth.InviteSpec{})
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(inv.Code, "inv_"))
	})

	for name, tc := range map[string]struct {
		spec auth.InviteSpec
		want string
	}{
		"a short code":         {auth.InviteSpec{Code: "short"}, "8 to 64 characters"},
		"a code of spaces":     {auth.InviteSpec{Code: "two words!"}, "letters, digits, - and _ only"},
		"too many uses":        {auth.InviteSpec{MaxUses: 1001}, "1 to 1000 sign-ups"},
		"no uses":              {auth.InviteSpec{MaxUses: -1}, "1 to 1000 sign-ups"},
		"ended already":        {auth.InviteSpec{ExpiresAt: now.Add(-time.Minute)}, "within a year"},
		"more than a year off": {auth.InviteSpec{ExpiresAt: now.Add(366 * 24 * time.Hour)}, "within a year"},
	} {
		t.Run("refused: "+name, func(t *testing.T) {
			_, err := newService(t, newDeps(t), config(domain.RegistrationInvite)).CreateInvite(context.Background(), admin, tc.spec)
			require.ErrorIs(t, err, domain.ErrInvalidInput)
			assert.ErrorContains(t, err, tc.want)
		})
	}

	t.Run("refused: a code another invite has", func(t *testing.T) {
		d := newDeps(t)
		d.invites.EXPECT().Taken(mock.Anything, [][]byte{sha("taken-code")}).Return(true, nil)

		_, err := newService(t, d, config(domain.RegistrationInvite)).CreateInvite(context.Background(), admin, auth.InviteSpec{Code: "taken-code"})
		require.ErrorIs(t, err, domain.ErrInviteCodeTaken)
	})

	t.Run("admins only", func(t *testing.T) {
		_, err := newService(t, newDeps(t), config(domain.RegistrationInvite)).
			CreateInvite(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), auth.InviteSpec{})
		require.ErrorIs(t, err, domain.ErrForbidden)
	})
}

func TestCheckInviteCode(t *testing.T) {
	admin := sessionPrincipal(user("root", domain.RoleAdmin))
	d := newDeps(t)
	d.invites.EXPECT().Taken(mock.Anything, [][]byte{sha("free-code")}).Return(false, nil)
	d.invites.EXPECT().Taken(mock.Anything, [][]byte{sha("taken-code"), sha("TAKEN-code")}).Return(true, nil)
	s := newService(t, d, config(domain.RegistrationInvite))

	for code, want := range map[string]string{
		"free-code":  "",
		"TAKEN-code": "Another invite has this code.",
		"short":      "A code has 8 to 64 characters.",
		"no spaces!": "A code has letters, digits, - and _ only.",
	} {
		got, err := s.CheckInviteCode(context.Background(), admin, code)
		require.NoError(t, err)
		assert.Equal(t, want, got, code)
	}

	_, err := s.CheckInviteCode(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), "free-code")
	require.ErrorIs(t, err, domain.ErrForbidden)
}

func TestRevokeInvite(t *testing.T) {
	d := newDeps(t)
	d.invites.EXPECT().Revoke(mock.Anything, domain.InviteID("i-1"), domain.UserID("root"), now).Return(nil)
	d.invites.EXPECT().Revoke(mock.Anything, domain.InviteID("none"), domain.UserID("root"), now).Return(domain.ErrNotFound)
	s := newService(t, d, config(domain.RegistrationInvite))
	admin := sessionPrincipal(user("root", domain.RoleAdmin))

	require.NoError(t, s.RevokeInvite(context.Background(), admin, "i-1"))
	require.ErrorIs(t, s.RevokeInvite(context.Background(), admin, "none"), domain.ErrNotFound)
	require.ErrorIs(t, s.RevokeInvite(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), "i-1"), domain.ErrForbidden)
}

// Listed, each code is opened to copy; one made before codes were kept, or one that will
// not open, has none.
func TestListInvites(t *testing.T) {
	d := newDeps(t)
	box := portsmocks.NewMockSecretBox(t)
	d.invites.EXPECT().List(mock.Anything).Return([]ports.StoredInvite{
		{Invite: domain.Invite{ID: "i-1", MaxUses: 5, Used: 2}, Sealed: []byte("sealed-1")},
		{Invite: domain.Invite{ID: "i-2"}},
		{Invite: domain.Invite{ID: "i-3"}, Sealed: []byte("broken")},
	}, nil)
	box.EXPECT().Open(mock.Anything, []byte("sealed-1"), []byte("invite:i-1")).Return([]byte("Spring-2026"), nil)
	box.EXPECT().Open(mock.Anything, []byte("broken"), []byte("invite:i-3")).Return(nil, errors.New("message authentication failed"))

	got, err := newService(t, d, config(domain.RegistrationInvite), auth.WithInviteBox(box)).
		ListInvites(context.Background(), sessionPrincipal(user("root", domain.RoleAdmin)))
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "Spring-2026", got[0].Code)
	assert.Equal(t, 2, got[0].Used)
	assert.Empty(t, got[1].Code)
	assert.Empty(t, got[2].Code)
}

// A code is found any case; one of the invites made before codes were any case, as given.
func TestInviteForAnyCase(t *testing.T) {
	d := newDeps(t)
	d.allow(true)
	d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("spring-2026"), sha("SPRING-2026")}, now).Return(domain.Invite{ID: "i-1"}, nil)

	_, err := newService(t, d, config(domain.RegistrationInvite)).InviteFor(context.Background(), "SPRING-2026", "1.2.3.4")
	require.NoError(t, err)
}
