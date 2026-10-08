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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

func TestCreateAPIToken(t *testing.T) {
	u := user("u1", domain.RoleUser)

	tests := []struct {
		name    string
		p       auth.Principal
		tokName string
		expires time.Time
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "issued once, stored hashed", p: sessionPrincipal(u), tokName: " cli ", expires: now.Add(time.Hour),
			setup: func(d deps) {
				d.tokens.EXPECT().Create(mock.Anything, mock.MatchedBy(func(t domain.APIToken) bool {
					return t.Name == "cli" && t.UserID == u.ID && t.ExpiresAt.Equal(now.Add(time.Hour)) && strings.HasPrefix(t.Prefix, "pt_")
				}), mock.Anything).Return(nil)
			},
		},
		{name: "not from another token", p: tokenPrincipal(u), tokName: "cli", setup: func(deps) {}, wantErr: domain.ErrForbidden},
		{name: "empty name", p: sessionPrincipal(u), tokName: "  ", setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "long name", p: sessionPrincipal(u), tokName: strings.Repeat("x", 65), setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "expiry in the past", p: sessionPrincipal(u), tokName: "cli", expires: now, setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			secret, tok, err := newService(t, d, config(domain.RegistrationOpen)).CreateAPIToken(context.Background(), tt.p, tt.tokName, tt.expires)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(secret, "pt_"))
			assert.Equal(t, secret[:10], tok.Prefix)
		})
	}
}

func TestStoredTokenHashMatchesSecret(t *testing.T) {
	d := newDeps(t)

	var stored []byte
	d.tokens.EXPECT().Create(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.APIToken, hash []byte) error {
			stored = hash
			return nil
		})

	secret, _, err := newService(t, d, config(domain.RegistrationOpen)).CreateAPIToken(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), "cli", time.Time{})
	require.NoError(t, err)
	assert.Equal(t, sha(secret), stored, "authentication looks the token up by this hash")
}

func TestRevokeAPIToken(t *testing.T) {
	u := user("u1", domain.RoleUser)

	tests := []struct {
		name    string
		p       auth.Principal
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "own token",
			p:    sessionPrincipal(u),
			setup: func(d deps) {
				d.tokens.EXPECT().Delete(mock.Anything, u.ID, domain.APITokenID("tok-9")).Return(nil)
			},
		},
		{
			name: "someone else's token is not found",
			p:    sessionPrincipal(u),
			setup: func(d deps) {
				d.tokens.EXPECT().Delete(mock.Anything, u.ID, domain.APITokenID("tok-9")).Return(domain.ErrNotFound)
			},
			wantErr: domain.ErrNotFound,
		},
		{name: "not from a token", p: tokenPrincipal(u), setup: func(deps) {}, wantErr: domain.ErrForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			err := newService(t, d, config(domain.RegistrationOpen)).RevokeAPIToken(context.Background(), tt.p, "tok-9")
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestUpdateUser(t *testing.T) {
	admin := user("admin", domain.RoleAdmin)
	target := user("target", domain.RoleUser)
	otherAdmin := user("other", domain.RoleAdmin)

	role := func(r domain.Role) *domain.Role { return &r }
	flag := func(b bool) *bool { return &b }

	tests := []struct {
		name    string
		actor   auth.Principal
		id      domain.UserID
		upd     auth.UserUpdate
		setup   func(d deps)
		wantErr error
		check   func(t *testing.T, u domain.User)
	}{
		{
			name: "promote a user", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Role: role(domain.RoleAdmin)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(target, nil)
				d.users.EXPECT().Update(mock.Anything, mock.MatchedBy(func(u domain.User) bool { return u.IsAdmin() && u.UpdatedAt.Equal(now) })).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.True(t, u.IsAdmin()) },
		},
		{
			name: "disabling signs the user out", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(target, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
				d.sessions.EXPECT().DeleteForUser(mock.Anything, target.ID, domain.SessionID("")).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.Equal(t, now, u.DisabledAt) },
		},
		{
			name: "re-enable", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Disabled: flag(false)},
			setup: func(d deps) {
				disabled := target
				disabled.DisabledAt = now.Add(-time.Hour)
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(disabled, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.False(t, u.Disabled()) },
		},
		{
			name: "demote an admin while another remains", actor: sessionPrincipal(admin), id: otherAdmin.ID, upd: auth.UserUpdate{Role: role(domain.RoleUser)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, otherAdmin.ID).Return(otherAdmin, nil)
				d.users.EXPECT().CountActiveAdmins(mock.Anything).Return(2, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.False(t, u.IsAdmin()) },
		},
		{
			name: "the last admin cannot be demoted", actor: sessionPrincipal(admin), id: admin.ID, upd: auth.UserUpdate{Role: role(domain.RoleUser)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, admin.ID).Return(admin, nil)
				d.users.EXPECT().CountActiveAdmins(mock.Anything).Return(1, nil)
			},
			wantErr: domain.ErrLastAdmin,
		},
		{
			name: "the last admin cannot be disabled", actor: sessionPrincipal(admin), id: admin.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, admin.ID).Return(admin, nil)
				d.users.EXPECT().CountActiveAdmins(mock.Anything).Return(1, nil)
			},
			wantErr: domain.ErrLastAdmin,
		},
		{name: "regular users cannot", actor: sessionPrincipal(target), id: admin.ID, setup: func(deps) {}, wantErr: domain.ErrForbidden},
		{name: "unknown role", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Role: role("owner")}, setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{
			name: "unknown user", actor: sessionPrincipal(admin), id: "ghost", upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, domain.UserID("ghost")).Return(domain.User{}, domain.ErrNotFound)
			},
			wantErr: domain.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			got, err := newService(t, d, config(domain.RegistrationOpen)).UpdateUser(context.Background(), tt.actor, tt.id, tt.upd)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			tt.check(t, got)
		})
	}
}

func TestInvitesAndUserList(t *testing.T) {
	admin := sessionPrincipal(user("admin", domain.RoleAdmin))
	regular := sessionPrincipal(user("u", domain.RoleUser))

	tests := []struct {
		name    string
		setup   func(d deps)
		call    func(s *auth.Service) error
		wantErr error
	}{
		{
			name: "create invite",
			setup: func(d deps) {
				d.invites.EXPECT().Create(mock.Anything, mock.MatchedBy(func(i domain.Invite) bool {
					return i.CreatedBy == "admin" && i.ExpiresAt.Equal(now.Add(7*24*time.Hour))
				}), mock.Anything).Return(nil)
			},
			call: func(s *auth.Service) error {
				code, _, err := s.CreateInvite(context.Background(), admin)
				assert.True(t, strings.HasPrefix(code, "inv_"))

				return err
			},
		},
		{
			name: "list invites",
			setup: func(d deps) {
				d.invites.EXPECT().List(mock.Anything).Return(nil, nil)
			},
			call: func(s *auth.Service) error {
				_, err := s.ListInvites(context.Background(), admin)
				return err
			},
		},
		{
			name: "list users passes the filter",
			setup: func(d deps) {
				d.users.EXPECT().List(mock.Anything, ports.UserFilter{Limit: 5, Cursor: "c"}).Return(ports.Page[domain.User]{}, nil)
			},
			call: func(s *auth.Service) error {
				_, err := s.ListUsers(context.Background(), admin, ports.UserFilter{Limit: 5, Cursor: "c"})
				return err
			},
		},
		{
			name:  "regular users cannot invite",
			setup: func(deps) {},
			call: func(s *auth.Service) error {
				_, _, err := s.CreateInvite(context.Background(), regular)
				return err
			},
			wantErr: domain.ErrForbidden,
		},
		{
			name:  "regular users cannot list invites",
			setup: func(deps) {},
			call: func(s *auth.Service) error {
				_, err := s.ListInvites(context.Background(), regular)
				return err
			},
			wantErr: domain.ErrForbidden,
		},
		{
			name:  "regular users cannot list users",
			setup: func(deps) {},
			call: func(s *auth.Service) error {
				_, err := s.ListUsers(context.Background(), regular, ports.UserFilter{})
				return err
			},
			wantErr: domain.ErrForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			err := tt.call(newService(t, d, config(domain.RegistrationInvite)))
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
