//go:build db

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func joinRequest(email string, created time.Time) domain.JoinRequest {
	return domain.JoinRequest{
		ID: domain.JoinRequestID(newID()), Email: email, Note: "Hello", CreatedAt: created,
		LinkExpiresAt: created.Add(24 * time.Hour), MailedAt: created, Mails: 1,
	}
}

// A request is confirmed by its link once, waits for an admin, and is decided once (D394).
func TestJoinRequests(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	joins := postgres.NewJoinRequests(db)
	invites := postgres.NewInvites(db)
	admin := seedUser(t, db, "admin@x.io", domain.RoleAdmin)

	req := joinRequest("New@X.io", at)
	require.NoError(t, joins.Create(ctx, req, []byte("tok")))

	open, err := joins.Open(ctx, "new@x.io")
	require.NoError(t, err)
	assert.Equal(t, domain.JoinUnconfirmed, open.Status())
	assert.Equal(t, "New@X.io", open.Email)

	n, err := joins.CountOpen(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	list, err := joins.List(ctx, at.Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, list, "admins see confirmed requests only")

	// A second open one for the address is refused.
	require.Error(t, joins.Create(ctx, joinRequest("new@x.io", at), []byte("tok2")))

	// A new link replaces the old one.
	require.NoError(t, joins.Remail(ctx, req.ID, []byte("tok3"), at.Add(25*time.Hour), at.Add(time.Hour)))
	_, err = joins.Confirm(ctx, []byte("tok"), at.Add(2*time.Hour))
	require.ErrorIs(t, err, domain.ErrInvalidLink, "the old link")

	_, err = joins.Confirm(ctx, []byte("tok3"), at.Add(26*time.Hour))
	require.ErrorIs(t, err, domain.ErrInvalidLink, "too old")

	got, err := joins.Confirm(ctx, []byte("tok3"), at.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, domain.JoinPending, got.Status())
	assert.Equal(t, 2, got.Mails)

	_, err = joins.Confirm(ctx, []byte("tok3"), at.Add(2*time.Hour))
	require.ErrorIs(t, err, domain.ErrInvalidLink, "once")

	inv := domain.Invite{ID: domain.InviteID(newID()), CreatedBy: admin.ID, Email: "New@X.io", CreatedAt: at, ExpiresAt: at.Add(time.Hour), MaxUses: 1}
	require.NoError(t, invites.Create(ctx, inv, []byte("code"), nil))
	require.NoError(t, joins.Decide(ctx, req.ID, true, admin.ID, at.Add(3*time.Hour), inv.ID))
	require.ErrorIs(t, joins.Decide(ctx, req.ID, false, admin.ID, at.Add(4*time.Hour), ""), domain.ErrNotFound, "decided once")

	decided, err := joins.Get(ctx, req.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.JoinApproved, decided.Status())
	assert.Equal(t, inv.ID, decided.InviteID)
	assert.Equal(t, admin.ID, decided.DecidedBy)

	_, err = joins.Open(ctx, "new@x.io")
	require.ErrorIs(t, err, domain.ErrNotFound, "closed")

	list, err = joins.List(ctx, at)
	require.NoError(t, err)
	require.Len(t, list, 1)

	list, err = joins.List(ctx, at.Add(5*time.Hour))
	require.NoError(t, err)
	assert.Empty(t, list, "decided before since")
}

func TestJoinRequestsDeniedAndPruned(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	joins := postgres.NewJoinRequests(db)
	admin := seedUser(t, db, "admin@x.io", domain.RoleAdmin)

	denied := joinRequest("no@x.io", at)
	require.NoError(t, joins.Create(ctx, denied, []byte("tok-no")))
	_, err := joins.Confirm(ctx, []byte("tok-no"), at)
	require.NoError(t, err)

	pending := joinRequest("wait@x.io", at.Add(time.Minute))
	require.NoError(t, joins.Create(ctx, pending, []byte("tok-wait")))
	_, err = joins.Confirm(ctx, []byte("tok-wait"), at.Add(time.Minute))
	require.NoError(t, err)

	require.NoError(t, joins.Decide(ctx, denied.ID, false, admin.ID, at.Add(time.Hour), ""))

	was, err := joins.DeniedSince(ctx, "no@x.io", at)
	require.NoError(t, err)
	assert.True(t, was)

	was, err = joins.DeniedSince(ctx, "no@x.io", at.Add(2*time.Hour))
	require.NoError(t, err)
	assert.False(t, was, "denied before since")

	// Pending first, then decided.
	list, err := joins.List(ctx, at)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, pending.ID, list[0].ID)
	assert.Equal(t, domain.JoinDenied, list[1].Status())

	// Never confirmed, its link long gone: pruned; a confirmed one stays.
	stale := joinRequest("stale@x.io", at)
	require.NoError(t, joins.Create(ctx, stale, []byte("tok-stale")))

	pruned, err := joins.PruneUnconfirmed(ctx, at.Add(25*time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), pruned)

	_, err = joins.Get(ctx, stale.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = joins.Get(ctx, pending.ID)
	require.NoError(t, err)
}
