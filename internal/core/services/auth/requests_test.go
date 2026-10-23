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
	authmocks "github.com/lein3000zzz/polythought-go/internal/mocks/auth"
)

// requests is the configuration with requests to join on, as D394 has them.
func requests() auth.Config {
	cfg := config(domain.RegistrationInvite)
	cfg.Requests = true

	return cfg
}

func TestRequestsOpen(t *testing.T) {
	for name, tc := range map[string]struct {
		cfg  func() auth.Config
		mail bool
		want bool
	}{
		"on, by invite, mail set up": {requests, true, true},
		"switched off":               {func() auth.Config { return config(domain.RegistrationInvite) }, true, false},
		"without mail":               {requests, false, false},
		"registration open": {func() auth.Config {
			cfg := requests()
			cfg.Registration = domain.RegistrationOpen

			return cfg
		}, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDeps(t)
			d.mail.EXPECT().SetUp().Return(tc.mail).Maybe()

			assert.Equal(t, tc.want, newService(t, d, tc.cfg()).RequestsOpen())
		})
	}
}

func TestRequestToJoin(t *testing.T) {
	ctx := context.Background()
	open := func(d deps) {
		d.allow(true)
		d.mail.EXPECT().SetUp().Return(true).Maybe()
	}

	t.Run("a new request, its link mailed to confirm it", func(t *testing.T) {
		d := newDeps(t)
		open(d)
		d.users.EXPECT().ByEmail(mock.Anything, "new@example.com").Return(domain.User{}, "", domain.ErrNotFound)
		d.joins.EXPECT().DeniedSince(mock.Anything, "new@example.com", now.Add(-30*24*time.Hour)).Return(false, nil)
		d.joins.EXPECT().Open(mock.Anything, "new@example.com").Return(domain.JoinRequest{}, domain.ErrNotFound)
		d.joins.EXPECT().CountOpen(mock.Anything).Return(3, nil)

		var hash []byte
		d.joins.EXPECT().Create(mock.Anything, mock.MatchedBy(func(r domain.JoinRequest) bool {
			return r.Email == "New@Example.com" && r.Note == "I trade weather markets." &&
				r.LinkExpiresAt.Equal(now.Add(24*time.Hour)) && r.Mails == 1
		}), mock.Anything).RunAndReturn(func(_ context.Context, _ domain.JoinRequest, h []byte) error {
			hash = h
			return nil
		})
		d.mail.EXPECT().SendJoinConfirm(mock.Anything, "New@Example.com", mock.Anything, 24*time.Hour).
			RunAndReturn(func(_ context.Context, _, token string, _ time.Duration) error {
				assert.Equal(t, sha(token), hash)
				return nil
			})

		require.NoError(t, newService(t, d, requests()).RequestToJoin(ctx, " New@Example.com ", "  I trade weather markets. ", "1.2.3.4"))
	})

	// The same answer for each: nobody learns which addresses have accounts or requests.
	for name, setup := range map[string]func(d deps){
		"an address with an account": func(d deps) {
			d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{ID: "u-1", EmailVerifiedAt: now}, "x", nil)
		},
		"one denied in the last 30 days": func(d deps) {
			d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
			d.joins.EXPECT().DeniedSince(mock.Anything, mock.Anything, mock.Anything).Return(true, nil)
		},
		"one with the admins already": func(d deps) {
			d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
			d.joins.EXPECT().DeniedSince(mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
			d.joins.EXPECT().Open(mock.Anything, mock.Anything).Return(domain.JoinRequest{ConfirmedAt: now}, nil)
		},
		"one mailed a minute ago, or five times": func(d deps) {
			d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound).Twice()
			d.joins.EXPECT().DeniedSince(mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Twice()
			d.joins.EXPECT().Open(mock.Anything, mock.Anything).Return(domain.JoinRequest{MailedAt: now.Add(-30 * time.Second), Mails: 1}, nil).Once()
			d.joins.EXPECT().Open(mock.Anything, mock.Anything).Return(domain.JoinRequest{MailedAt: now.Add(-time.Hour), Mails: 5}, nil).Once()
		},
		"200 open already": func(d deps) {
			d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
			d.joins.EXPECT().DeniedSince(mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
			d.joins.EXPECT().Open(mock.Anything, mock.Anything).Return(domain.JoinRequest{}, domain.ErrNotFound)
			d.joins.EXPECT().CountOpen(mock.Anything).Return(200, nil)
		},
	} {
		t.Run("nothing mailed: "+name, func(t *testing.T) {
			d := newDeps(t)
			open(d)
			setup(d)
			s := newService(t, d, requests())

			require.NoError(t, s.RequestToJoin(ctx, "new@example.com", "", "1.2.3.4"))

			if strings.HasPrefix(name, "one mailed") {
				require.NoError(t, s.RequestToJoin(ctx, "new@example.com", "", "1.2.3.4"))
			}
		})
	}

	t.Run("an unconfirmed one mailed again, a new link", func(t *testing.T) {
		d := newDeps(t)
		open(d)
		d.users.EXPECT().ByEmail(mock.Anything, mock.Anything).Return(domain.User{}, "", domain.ErrNotFound)
		d.joins.EXPECT().DeniedSince(mock.Anything, mock.Anything, mock.Anything).Return(false, nil)
		d.joins.EXPECT().Open(mock.Anything, mock.Anything).Return(domain.JoinRequest{ID: "r-1", MailedAt: now.Add(-2 * time.Minute), Mails: 2}, nil)
		d.joins.EXPECT().Remail(mock.Anything, domain.JoinRequestID("r-1"), mock.Anything, now.Add(24*time.Hour), now).Return(nil)
		d.mail.EXPECT().SendJoinConfirm(mock.Anything, "new@example.com", mock.Anything, 24*time.Hour).Return(nil)

		require.NoError(t, newService(t, d, requests()).RequestToJoin(ctx, "new@example.com", "", "1.2.3.4"))
	})

	t.Run("refused", func(t *testing.T) {
		d := newDeps(t)
		open(d)
		s := newService(t, d, requests())

		require.ErrorIs(t, s.RequestToJoin(ctx, "not an address", "", "1.2.3.4"), domain.ErrInvalidEmail)
		require.ErrorIs(t, s.RequestToJoin(ctx, "new@example.com", strings.Repeat("é", 501), "1.2.3.4"), domain.ErrInvalidInput)

		off := newDeps(t)
		open(off)
		require.ErrorIs(t, newService(t, off, config(domain.RegistrationInvite)).RequestToJoin(ctx, "new@example.com", "", "1.2.3.4"), domain.ErrRequestsOff)

		limited := newDeps(t)
		limited.allow(false)
		require.ErrorIs(t, newService(t, limited, requests()).RequestToJoin(ctx, "new@example.com", "", "1.2.3.4"), domain.ErrRateLimited)
	})
}

func TestConfirmJoin(t *testing.T) {
	ctx := context.Background()
	d := newDeps(t)
	d.allow(true)
	joins := authmocks.NewMockJoinNotifier(t)
	d.joins.EXPECT().Confirm(mock.Anything, sha("tok"), now).Return(domain.JoinRequest{ID: "r-1", Email: "new@example.com", Note: "Hello"}, nil)
	d.joins.EXPECT().Confirm(mock.Anything, sha("used"), now).Return(domain.JoinRequest{}, domain.ErrInvalidLink)
	joins.EXPECT().JoinRequested("new@example.com", "Hello").Once() // the admins hear of it once confirmed

	s := newService(t, d, requests(), auth.WithJoinNotifier(joins))

	require.NoError(t, s.ConfirmJoin(ctx, " tok ", "1.2.3.4"))
	require.ErrorIs(t, s.ConfirmJoin(ctx, "used", "1.2.3.4"), domain.ErrInvalidLink)
	require.ErrorIs(t, s.ConfirmJoin(ctx, "", "1.2.3.4"), domain.ErrInvalidLink)
}

func TestDecideJoin(t *testing.T) {
	ctx := context.Background()
	admin := sessionPrincipal(user("root", domain.RoleAdmin))
	pending := domain.JoinRequest{ID: "r-1", Email: "New@Example.com", ConfirmedAt: now}

	t.Run("approved: an invite of the address alone, once, mailed", func(t *testing.T) {
		d := newDeps(t)
		d.joins.EXPECT().Get(mock.Anything, domain.JoinRequestID("r-1")).Return(pending, nil)
		d.users.EXPECT().ByEmail(mock.Anything, "new@example.com").Return(domain.User{}, "", domain.ErrNotFound)

		var made domain.InviteID
		d.invites.EXPECT().Create(mock.Anything, mock.MatchedBy(func(i domain.Invite) bool {
			made = i.ID
			return i.Email == "New@Example.com" && i.MaxUses == 1 && i.ExpiresAt.Equal(now.Add(7*24*time.Hour))
		}), mock.Anything, mock.Anything).Return(nil)
		d.joins.EXPECT().Decide(mock.Anything, domain.JoinRequestID("r-1"), true, domain.UserID("root"), now, mock.Anything).
			RunAndReturn(func(_ context.Context, _ domain.JoinRequestID, _ bool, _ domain.UserID, _ time.Time, invite domain.InviteID) error {
				assert.Equal(t, made, invite)
				return nil
			})
		d.mail.EXPECT().SendInvite(mock.Anything, "New@Example.com", "root@example.com", mock.Anything, now.Add(7*24*time.Hour)).Return(nil)

		inv, sent, err := newService(t, d, requests()).ApproveJoin(ctx, admin, "r-1")
		require.NoError(t, err)
		assert.True(t, sent)
		assert.Equal(t, "New@Example.com", inv.Email)
	})

	t.Run("denied: mailed while the setting says", func(t *testing.T) {
		for _, tell := range []bool{true, false} {
			d := newDeps(t)
			d.mail.EXPECT().SetUp().Return(true).Maybe()
			d.joins.EXPECT().Get(mock.Anything, domain.JoinRequestID("r-1")).Return(pending, nil)
			d.joins.EXPECT().Decide(mock.Anything, domain.JoinRequestID("r-1"), false, domain.UserID("root"), now, domain.InviteID("")).Return(nil)

			if tell {
				d.mail.EXPECT().SendJoinDenied(mock.Anything, "New@Example.com").Return(nil)
			}

			cfg := requests()
			cfg.RequestDeniedMail = tell
			require.NoError(t, newService(t, d, cfg).DenyJoin(ctx, admin, "r-1"))
		}
	})

	t.Run("only a pending one, by an admin, to an address with no account", func(t *testing.T) {
		d := newDeps(t)
		d.joins.EXPECT().Get(mock.Anything, domain.JoinRequestID("r-2")).Return(domain.JoinRequest{ID: "r-2", ConfirmedAt: now, DecidedAt: now, Approved: true}, nil)
		d.joins.EXPECT().Get(mock.Anything, domain.JoinRequestID("r-3")).Return(domain.JoinRequest{ID: "r-3"}, nil) // unconfirmed
		d.joins.EXPECT().Get(mock.Anything, domain.JoinRequestID("r-1")).Return(pending, nil)
		d.users.EXPECT().ByEmail(mock.Anything, "new@example.com").Return(domain.User{ID: "u-1", EmailVerifiedAt: now}, "x", nil)
		s := newService(t, d, requests())

		_, _, err := s.ApproveJoin(ctx, admin, "r-2")
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.ErrorIs(t, s.DenyJoin(ctx, admin, "r-3"), domain.ErrNotFound)

		_, _, err = s.ApproveJoin(ctx, admin, "r-1")
		require.ErrorIs(t, err, domain.ErrEmailTaken)

		_, _, err = s.ApproveJoin(ctx, sessionPrincipal(user("u", domain.RoleUser)), "r-1")
		require.ErrorIs(t, err, domain.ErrForbidden)
		_, err = s.ListJoinRequests(ctx, sessionPrincipal(user("u", domain.RoleUser)))
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("listed: pending, and those decided in the last 30 days", func(t *testing.T) {
		d := newDeps(t)
		d.joins.EXPECT().List(mock.Anything, now.Add(-30*24*time.Hour)).Return([]domain.JoinRequest{pending}, nil)

		got, err := newService(t, d, requests()).ListJoinRequests(ctx, admin)
		require.NoError(t, err)
		assert.Equal(t, []domain.JoinRequest{pending}, got)
	})
}
