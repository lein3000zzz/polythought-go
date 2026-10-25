package auth_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

func TestCreateAPIToken(t *testing.T) {
	u, admin := user("u1", domain.RoleUser), user("a1", domain.RoleAdmin)
	read := []domain.Scope{domain.ScopeRead}

	tests := []struct {
		name    string
		p       auth.Principal
		tokName string
		expires time.Time
		scopes  []domain.Scope
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "issued once, stored hashed", p: sessionPrincipal(u), tokName: " cli ", expires: now.Add(time.Hour),
			scopes: []domain.Scope{domain.ScopePaper, domain.ScopeRead, domain.ScopePaper},
			setup: func(d deps) {
				d.tokens.EXPECT().Create(mock.Anything, mock.MatchedBy(func(t domain.APIToken) bool {
					return t.Name == "cli" && t.UserID == u.ID && t.ExpiresAt.Equal(now.Add(time.Hour)) && strings.HasPrefix(t.Prefix, "pt_") &&
						slices.Equal(t.Scopes, []domain.Scope{domain.ScopeRead, domain.ScopePaper}) // in order, once each
				}), mock.Anything).Return(nil)
			},
		},
		{
			name: "admin, for an admin", p: sessionPrincipal(admin), tokName: "ops", scopes: []domain.Scope{domain.ScopeAdmin},
			setup: func(d deps) {
				d.tokens.EXPECT().Create(mock.Anything, mock.MatchedBy(func(t domain.APIToken) bool {
					return slices.Equal(t.Scopes, []domain.Scope{domain.ScopeAdmin})
				}), mock.Anything).Return(nil)
			},
		},
		{name: "not from another token", p: tokenPrincipal(u), tokName: "cli", scopes: read, setup: func(deps) {}, wantErr: domain.ErrForbidden},
		{name: "empty name", p: sessionPrincipal(u), tokName: "  ", scopes: read, setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "long name", p: sessionPrincipal(u), tokName: strings.Repeat("x", 65), scopes: read, setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "expiry in the past", p: sessionPrincipal(u), tokName: "cli", expires: now, scopes: read, setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "no scope", p: sessionPrincipal(u), tokName: "cli", setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "an unknown scope", p: sessionPrincipal(u), tokName: "cli", scopes: []domain.Scope{"root"}, setup: func(deps) {}, wantErr: domain.ErrInvalidInput},
		{name: "admin, for a user", p: sessionPrincipal(u), tokName: "cli", scopes: []domain.Scope{domain.ScopeRead, domain.ScopeAdmin}, setup: func(deps) {}, wantErr: domain.ErrForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := newDeps(t)
			tt.setup(d)

			secret, tok, err := newService(t, d, config(domain.RegistrationOpen)).CreateAPIToken(context.Background(), tt.p, tt.tokName, tt.expires, tt.scopes)

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

	secret, _, err := newService(t, d, config(domain.RegistrationOpen)).CreateAPIToken(context.Background(), sessionPrincipal(user("u", domain.RoleUser)), "cli", time.Time{}, []domain.Scope{domain.ScopeRead})
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

			var ended []domain.APITokenID

			err := newService(t, d, config(domain.RegistrationOpen),
				auth.WithTokenRevoked(func(_ context.Context, id domain.APITokenID) { ended = append(ended, id) })).
				RevokeAPIToken(context.Background(), tt.p, "tok-9")
			require.ErrorIs(t, err, tt.wantErr)

			if tt.wantErr == nil {
				assert.Equal(t, []domain.APITokenID{"tok-9"}, ended, "told, to end its sessions and what it asked for (A16)")
			} else {
				assert.Empty(t, ended)
			}
		})
	}
}

func TestUpdateUser(t *testing.T) {
	admin := user("admin", domain.RoleAdmin)
	target := user("target", domain.RoleUser)
	otherAdmin := user("other", domain.RoleAdmin)

	role := func(r domain.Role) *domain.Role { return &r }
	flag := func(b bool) *bool { return &b }
	dollars := func(n int) *int { return &n }

	tests := []struct {
		name    string
		actor   auth.Principal
		id      domain.UserID
		upd     auth.UserUpdate
		admins  int // active admins when the update starts
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
			name: "pick a user to write code", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{CodeChosen: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(target, nil)
				d.users.EXPECT().Update(mock.Anything, mock.MatchedBy(func(u domain.User) bool {
					return u.CodeChosen && u.Role == domain.RoleUser && !u.Disabled()
				})).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.True(t, u.CodeChosen) },
		},
		{
			name: "disabling signs the user out and stops their workflows", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(target, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
				d.sessions.EXPECT().DeleteForUser(mock.Anything, target.ID, domain.SessionID("")).Return(nil)
				d.workflows.EXPECT().StopAll(mock.Anything, target.ID).Return(nil).Once()
				d.live.EXPECT().StopLive(mock.Anything, target.ID).Return(nil).Once()
			},
			check: func(t *testing.T, u domain.User) { assert.Equal(t, now, u.DisabledAt) },
		},
		{
			name: "disabling again stops them again, repairing a failed stop", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				disabled := target
				disabled.DisabledAt = now.Add(-time.Hour)
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(disabled, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
				d.sessions.EXPECT().DeleteForUser(mock.Anything, target.ID, domain.SessionID("")).Return(nil)
				d.workflows.EXPECT().StopAll(mock.Anything, target.ID).Return(nil).Once()
				d.live.EXPECT().StopLive(mock.Anything, target.ID).Return(nil).Once()
			},
			check: func(t *testing.T, u domain.User) {
				assert.Equal(t, now.Add(-time.Hour), u.DisabledAt, "the first disable time stays")
			},
		},
		{
			name: "a failed stop is reported", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(target, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
				d.sessions.EXPECT().DeleteForUser(mock.Anything, target.ID, domain.SessionID("")).Return(nil)
				d.workflows.EXPECT().StopAll(mock.Anything, target.ID).Return(domain.ErrUnavailable)
			},
			wantErr: domain.ErrUnavailable,
		},
		{
			name: "disabling turns their kill switch on, a failure reported (D226)", actor: sessionPrincipal(admin), id: target.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(target, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
				d.sessions.EXPECT().DeleteForUser(mock.Anything, target.ID, domain.SessionID("")).Return(nil)
				d.workflows.EXPECT().StopAll(mock.Anything, target.ID).Return(nil).Once()
				d.live.EXPECT().StopLive(mock.Anything, target.ID).Return(domain.ErrUnavailable).Once()
			},
			wantErr: domain.ErrUnavailable,
		},
		{
			name: "an admin's caps for a user (D222)", actor: sessionPrincipal(admin), id: target.ID,
			upd: auth.UserUpdate{LiveCaps: &domain.LiveCaps{PerOrder: dollars(250)}},
			setup: func(d deps) {
				capped := target
				capped.LiveCaps = domain.LiveCaps{PerOrder: dollars(50), PerDay: dollars(500)}
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(capped, nil)
				d.users.EXPECT().Update(mock.Anything, mock.MatchedBy(func(u domain.User) bool {
					return *u.LiveCaps.PerOrder == 250 && u.LiveCaps.PerDay == nil
				})).Return(nil)
			},
			check: func(t *testing.T, u domain.User) {
				assert.Equal(t, 250, *u.LiveCaps.PerOrder)
				assert.Nil(t, u.LiveCaps.PerDay, "back to the instance's")
			},
		},
		{
			name: "a cap below 0 is refused", actor: sessionPrincipal(admin), id: target.ID,
			upd: auth.UserUpdate{LiveCaps: &domain.LiveCaps{PerDay: dollars(-1)}}, setup: func(deps) {}, wantErr: domain.ErrInvalidInput,
		},
		{
			name: "an admin's caps on a user's agents (D271), which leave their live caps be", actor: sessionPrincipal(admin), id: target.ID,
			upd: auth.UserUpdate{AgentCaps: &domain.AgentCaps{Dollars: agentDollars("12.50"), TotalTokens: tokens(1_000_000)}},
			setup: func(d deps) {
				capped := target
				capped.LiveCaps = domain.LiveCaps{PerOrder: dollars(50)}
				d.users.EXPECT().ByID(mock.Anything, target.ID).Return(capped, nil)
				d.users.EXPECT().Update(mock.Anything, mock.MatchedBy(func(u domain.User) bool {
					return u.AgentCaps.Dollars.String() == "12.5" && *u.AgentCaps.TotalTokens == 1_000_000 &&
						u.AgentCaps.InputTokens == nil && *u.LiveCaps.PerOrder == 50
				})).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.Equal(t, int64(1_000_000), *u.AgentCaps.TotalTokens) },
		},
		{
			name: "an agent dollar cap past the cent is refused", actor: sessionPrincipal(admin), id: target.ID,
			upd: auth.UserUpdate{AgentCaps: &domain.AgentCaps{Dollars: agentDollars("1.005")}}, setup: func(deps) {}, wantErr: domain.ErrInvalidInput,
		},
		{
			name: "an agent token cap below 0 is refused", actor: sessionPrincipal(admin), id: target.ID,
			upd: auth.UserUpdate{AgentCaps: &domain.AgentCaps{OutputTokens: tokens(-1)}}, setup: func(deps) {}, wantErr: domain.ErrInvalidInput,
		},
		{
			// The mock fails the test if StopAll is called: enabling does not restart anything.
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
			name: "demote an admin while another remains", actor: sessionPrincipal(admin), id: otherAdmin.ID, upd: auth.UserUpdate{Role: role(domain.RoleUser)}, admins: 2,
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, otherAdmin.ID).Return(otherAdmin, nil)
				d.users.EXPECT().Update(mock.Anything, mock.Anything).Return(nil)
			},
			check: func(t *testing.T, u domain.User) { assert.False(t, u.IsAdmin()) },
		},
		{
			name: "the last admin cannot be demoted", actor: sessionPrincipal(admin), id: admin.ID, upd: auth.UserUpdate{Role: role(domain.RoleUser)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, admin.ID).Return(admin, nil)
			},
			wantErr: domain.ErrLastAdmin,
		},
		{
			name: "the last admin cannot be disabled", actor: sessionPrincipal(admin), id: admin.ID, upd: auth.UserUpdate{Disabled: flag(true)},
			setup: func(d deps) {
				d.users.EXPECT().ByID(mock.Anything, admin.ID).Return(admin, nil)
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
			// The last-admin cases fail unless this count reaches the check, so Maybe is safe.
			d.users.EXPECT().LockActiveAdmins(mock.Anything).Return(max(tt.admins, 1), nil).Maybe()
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
				}), mock.Anything, mock.Anything).Return(nil)
			},
			call: func(s *auth.Service) error {
				inv, err := s.CreateInvite(context.Background(), admin, auth.InviteSpec{})
				assert.True(t, strings.HasPrefix(inv.Code, "inv_"))

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
				d.users.EXPECT().List(mock.Anything, ports.PageFilter{Limit: 5, Cursor: "c"}).Return(ports.Page[domain.User]{}, nil)
			},
			call: func(s *auth.Service) error {
				_, err := s.ListUsers(context.Background(), admin, ports.PageFilter{Limit: 5, Cursor: "c"})
				return err
			},
		},
		{
			name:  "regular users cannot invite",
			setup: func(deps) {},
			call: func(s *auth.Service) error {
				_, err := s.CreateInvite(context.Background(), regular, auth.InviteSpec{})
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
				_, err := s.ListUsers(context.Background(), regular, ports.PageFilter{})
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

func agentDollars(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

func tokens(n int64) *int64 { return &n }
