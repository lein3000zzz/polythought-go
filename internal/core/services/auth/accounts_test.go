package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

func TestNewRejectsUnknownRegistrationMode(t *testing.T) {
	d := newDeps(t)

	_, err := auth.New(config("sometimes"), auth.Repositories{}, d.hasher, d.limiter, logging.Noop())
	require.Error(t, err)
}

func TestLiveConfig(t *testing.T) {
	d := newDeps(t)
	d.allow(true)
	d.hasher.EXPECT().Hash("polythought-dummy-password").Return(dummyHash, nil).Once()

	cfg := config(domain.RegistrationOpen)

	s, err := auth.New(config(domain.RegistrationClosed), auth.Repositories{Users: d.users, Tx: d.tx}, d.hasher, d.limiter, logging.Noop(),
		auth.WithClock(func() time.Time { return now }),
		auth.WithLiveConfig(func() auth.Config { return cfg }),
	)
	require.NoError(t, err)
	assert.Equal(t, domain.RegistrationOpen, s.RegistrationMode(), "the live config replaces the one given to New")

	cfg.Registration = domain.RegistrationClosed
	_, err = s.Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
	require.ErrorIs(t, err, domain.ErrRegistrationClosed, "a change applies to the next request")

	cfg.Registration = domain.RegistrationOpen
	cfg.MinPasswordLength = 30
	_, err = s.Register(context.Background(), auth.RegisterInput{Email: "bob@example.com", Password: goodPass})
	require.ErrorIs(t, err, domain.ErrWeakPassword)
	assert.ErrorContains(t, err, "at least 30 characters")
}

func TestRegister(t *testing.T) {
	createdUser := mock.MatchedBy(func(u domain.User) bool {
		return u.Email == "Bob@Example.com" && u.Role == domain.RoleUser && u.ID != "" && u.CreatedAt.Equal(now) &&
			u.EmailVerifiedAt.Equal(now) // verification is off
	})

	tests := []struct {
		name    string
		mode    domain.RegistrationMode
		in      auth.RegisterInput
		limited bool
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "open registration",
			mode: domain.RegistrationOpen,
			in:   auth.RegisterInput{Email: " Bob@Example.com ", Password: goodPass},
			setup: func(d deps) {
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, createdUser, userHash).Return(nil)
			},
		},
		{
			name: "invite consumed in the same transaction",
			mode: domain.RegistrationInvite,
			in:   auth.RegisterInput{Email: "Bob@Example.com", Password: goodPass, InviteCode: " inv_code "},
			setup: func(d deps) {
				d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("inv_code")}, now).Return(domain.Invite{ID: "i-1"}, nil)
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, createdUser, userHash).Return(nil)
				d.invites.EXPECT().Consume(mock.Anything, domain.InviteID("i-1"), mock.Anything, now).Return(nil)
			},
		},
		{
			name: "invite mode needs a code",
			mode: domain.RegistrationInvite,
			in:   auth.RegisterInput{Email: "bob@example.com", Password: goodPass},
			setup: func(deps) {
			},
			wantErr: domain.ErrInvalidInvite,
		},
		{
			name: "bad invite rolls the user back",
			mode: domain.RegistrationInvite,
			in:   auth.RegisterInput{Email: "Bob@Example.com", Password: goodPass, InviteCode: "inv_used"},
			setup: func(d deps) {
				// Free when looked up, taken by someone else by the time it is used.
				d.invites.EXPECT().ByCode(mock.Anything, [][]byte{sha("inv_used")}, now).Return(domain.Invite{ID: "i-1"}, nil)
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, createdUser, userHash).Return(nil)
				d.invites.EXPECT().Consume(mock.Anything, domain.InviteID("i-1"), mock.Anything, now).Return(domain.ErrInvalidInvite)
			},
			wantErr: domain.ErrInvalidInvite,
		},
		{
			name:    "closed registration",
			mode:    domain.RegistrationClosed,
			in:      auth.RegisterInput{Email: "bob@example.com", Password: goodPass},
			setup:   func(deps) {},
			wantErr: domain.ErrRegistrationClosed,
		},
		{
			name:    "rate limited before anything else",
			mode:    domain.RegistrationOpen,
			in:      auth.RegisterInput{Email: "bob@example.com", Password: goodPass},
			limited: true,
			setup:   func(deps) {},
			wantErr: domain.ErrRateLimited,
		},
		{
			name:    "malformed email",
			mode:    domain.RegistrationOpen,
			in:      auth.RegisterInput{Email: "not-an-email", Password: goodPass},
			setup:   func(deps) {},
			wantErr: domain.ErrInvalidEmail,
		},
		{
			name:    "display-name form is not an address",
			mode:    domain.RegistrationOpen,
			in:      auth.RegisterInput{Email: "Bob <bob@example.com>", Password: goodPass},
			setup:   func(deps) {},
			wantErr: domain.ErrInvalidEmail,
		},
		{
			name:    "short password",
			mode:    domain.RegistrationOpen,
			in:      auth.RegisterInput{Email: "bob@example.com", Password: "short"},
			setup:   func(deps) {},
			wantErr: domain.ErrWeakPassword,
		},
		{
			name:    "blank password",
			mode:    domain.RegistrationOpen,
			in:      auth.RegisterInput{Email: "bob@example.com", Password: "              "},
			setup:   func(deps) {},
			wantErr: domain.ErrWeakPassword,
		},
		{
			name: "email taken",
			mode: domain.RegistrationOpen,
			in:   auth.RegisterInput{Email: "Bob@Example.com", Password: goodPass},
			setup: func(d deps) {
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, createdUser, userHash).Return(domain.ErrEmailTaken)
			},
			wantErr: domain.ErrEmailTaken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			d.allow(!tt.limited)
			d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound).Maybe()
			tt.setup(d)

			got, err := newService(t, d, config(tt.mode)).Register(context.Background(), tt.in)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, domain.RoleUser, got.User.Role)
			assert.Equal(t, "Bob@Example.com", got.User.Email, "the address keeps its case; matching ignores it")
			assert.False(t, got.MustVerify)
		})
	}
}

func TestCreateUserAndBootstrap(t *testing.T) {
	tests := []struct {
		name    string
		call    func(s *auth.Service) (domain.User, error)
		setup   func(d deps)
		want    domain.Role
		wantErr error
	}{
		{
			name: "admin creates a user",
			call: func(s *auth.Service) (domain.User, error) {
				return s.CreateUser(context.Background(), sessionPrincipal(user("admin", domain.RoleAdmin)), "new@example.com", goodPass, domain.RoleUser)
			},
			setup: func(d deps) {
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, mock.Anything, userHash).Return(nil)
			},
			want: domain.RoleUser,
		},
		{
			name: "regular users cannot",
			call: func(s *auth.Service) (domain.User, error) {
				return s.CreateUser(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), "new@example.com", goodPass, domain.RoleUser)
			},
			setup:   func(deps) {},
			wantErr: domain.ErrForbidden,
		},
		{
			name: "unknown role",
			call: func(s *auth.Service) (domain.User, error) {
				return s.CreateUser(context.Background(), sessionPrincipal(user("admin", domain.RoleAdmin)), "new@example.com", goodPass, "owner")
			},
			setup:   func(deps) {},
			wantErr: domain.ErrInvalidInput,
		},
		{
			name: "bootstrap makes an admin",
			call: func(s *auth.Service) (domain.User, error) {
				return s.BootstrapAdmin(context.Background(), "root@example.com", goodPass)
			},
			setup: func(d deps) {
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, mock.MatchedBy(func(u domain.User) bool { return u.Role == domain.RoleAdmin }), userHash).Return(nil)
			},
			want: domain.RoleAdmin,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			got, err := tt.call(newService(t, d, config(domain.RegistrationClosed)))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Role)
			assert.Equal(t, now, got.EmailVerifiedAt, "an admin's word for the address (D167)")
		})
	}
}

func TestEnsureAdmin(t *testing.T) {
	tests := []struct {
		name        string
		admins      int
		wantCreated bool
	}{
		{name: "an admin exists", admins: 1},
		{name: "no admin yet", admins: 0, wantCreated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			d.users.EXPECT().CountActiveAdmins(mock.Anything).Return(tt.admins, nil)

			if tt.wantCreated {
				d.hasher.EXPECT().Hash(goodPass).Return(userHash, nil)
				d.users.EXPECT().Create(mock.Anything, mock.Anything, userHash).Return(nil)
			}

			created, err := newService(t, d, config(domain.RegistrationClosed)).EnsureAdmin(context.Background(), "root@example.com", goodPass)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCreated, created)
		})
	}
}

func TestChangePassword(t *testing.T) {
	u := user("u1", domain.RoleUser)

	tests := []struct {
		name    string
		p       auth.Principal
		current string
		next    string
		limited bool
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "changes and signs out other sessions",
			p:    sessionPrincipal(u), current: goodPass, next: "a brand new passphrase",
			setup: func(d deps) {
				d.users.EXPECT().PasswordHash(mock.Anything, u.ID).Return(userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, false, nil)
				d.hasher.EXPECT().Hash("a brand new passphrase").Return("$new", nil)
				d.users.EXPECT().SetPasswordHash(mock.Anything, u.ID, "$new", now).Return(nil)
				d.sessions.EXPECT().DeleteForUser(mock.Anything, u.ID, domain.SessionID("sess-1")).Return(nil)
				d.mail.EXPECT().SetUp().Return(true)
				d.mail.EXPECT().SendPasswordChanged(mock.Anything, u.Email, now).Return(nil) // told, in case it was not them
			},
		},
		{
			name: "not with an api token", p: tokenPrincipal(u), current: goodPass, next: "a brand new passphrase",
			setup:   func(deps) {},
			wantErr: domain.ErrForbidden,
		},
		{
			name: "wrong current password", p: sessionPrincipal(u), current: "guess", next: "a brand new passphrase",
			setup: func(d deps) {
				d.users.EXPECT().PasswordHash(mock.Anything, u.ID).Return(userHash, nil)
				d.hasher.EXPECT().Verify("guess", userHash).Return(false, false, nil)
			},
			wantErr: domain.ErrInvalidCredentials,
		},
		{
			name: "weak new password", p: sessionPrincipal(u), current: goodPass, next: "short",
			setup: func(d deps) {
				d.users.EXPECT().PasswordHash(mock.Anything, u.ID).Return(userHash, nil)
				d.hasher.EXPECT().Verify(goodPass, userHash).Return(true, false, nil)
			},
			wantErr: domain.ErrWeakPassword,
		},
		{
			name: "rate limited", p: sessionPrincipal(u), current: goodPass, next: "a brand new passphrase", limited: true,
			setup:   func(deps) {},
			wantErr: domain.ErrRateLimited,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			d.allow(!tt.limited)
			tt.setup(d)

			err := newService(t, d, config(domain.RegistrationOpen)).ChangePassword(context.Background(), tt.p, tt.current, tt.next)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestRepositoryErrorsPassThrough(t *testing.T) {
	d := newDeps(t)
	d.allow(true)

	boom := errors.New("db down")
	d.users.EXPECT().ByEmail(mock.Anything, "bob@example.com").Return(domain.User{}, "", boom)

	_, err := newService(t, d, config(domain.RegistrationOpen)).Login(context.Background(), auth.LoginInput{Email: "bob@example.com", Password: goodPass})
	require.ErrorIs(t, err, boom)
}
