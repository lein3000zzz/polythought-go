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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Who switched each workflow and why (D433), its owner's alone, newest first.
func TestWorkflowHistory(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	repo := postgres.NewWorkflowHistory(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "copy")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	kept := func(kind domain.SwitchKind, ago time.Duration, more func(*domain.Switch)) domain.Switch {
		t.Helper()

		sw := domain.Switch{ID: domain.SwitchID(newID()), WorkflowID: w.ID, Kind: kind, At: at.Add(-ago)}
		if more != nil {
			more(&sw)
		}

		require.NoError(t, repo.Record(ctx, sw))

		return sw
	}

	// Oldest first, as their IDs grow with time.
	old := kept(domain.SwitchOn, 100*24*time.Hour, nil)
	live := kept(domain.SwitchLive, 3*time.Hour, func(sw *domain.Switch) {
		sw.By = domain.Actor{GrantID: "g-1", Name: "Claude Code", Confirmed: true}
		sw.Reason, sw.WalletID, sw.WalletAddress = "the user asked", "w-1", "0x5f3C00000000000000000000000000000000A1b2"
	})
	off := kept(domain.SwitchOff, 2*time.Hour, func(sw *domain.Switch) {
		sw.Polythought, sw.Reason = true, "the plugin weather is disabled"
	})
	kill := kept(domain.SwitchKillOn, time.Hour, nil)

	t.Run("kept as it came, the newest first, a page at a time", func(t *testing.T) {
		first, err := repo.List(ctx, alice.ID, w.ID, ports.SwitchFilter{PageFilter: ports.PageFilter{Limit: 2}})
		require.NoError(t, err)
		require.Len(t, first.Items, 2)
		assert.Equal(t, kill.ID, first.Items[0].ID)
		assert.Equal(t, off, first.Items[1])
		require.NotEmpty(t, first.NextCursor)

		rest, err := repo.List(ctx, alice.ID, w.ID, ports.SwitchFilter{PageFilter: ports.PageFilter{Limit: 2, Cursor: first.NextCursor}})
		require.NoError(t, err)
		require.Len(t, rest.Items, 2)
		assert.Equal(t, live, rest.Items[0], "who, why and which wallet kept")
		assert.Equal(t, old.ID, rest.Items[1].ID)
	})

	t.Run("some kinds alone", func(t *testing.T) {
		got, err := repo.List(ctx, alice.ID, w.ID, ports.SwitchFilter{Kinds: []domain.SwitchKind{domain.SwitchOn, domain.SwitchOff}})
		require.NoError(t, err)
		require.Len(t, got.Items, 2)
		assert.Equal(t, off.ID, got.Items[0].ID)
		assert.Equal(t, old.ID, got.Items[1].ID)
	})

	t.Run("not another's", func(t *testing.T) {
		_, err := repo.List(ctx, bob.ID, w.ID, ports.SwitchFilter{})
		require.ErrorIs(t, err, domain.ErrNotFound)

		_, err = repo.List(ctx, alice.ID, "not-an-id", ports.SwitchFilter{})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("forgotten after a while, and with the workflow", func(t *testing.T) {
		n, err := repo.DeleteBefore(ctx, at.Add(-90*24*time.Hour))
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, "the one 100 days ago")

		require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, w.ID))

		n, err = repo.DeleteBefore(ctx, at.Add(time.Hour))
		require.NoError(t, err)
		assert.Zero(t, n, "gone with it")
	})
}
