//go:build db

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestConfirmations(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewConfirmations(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "live")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	ask := func(kind domain.ConfirmKind, wf domain.WorkflowID, created time.Time) domain.Confirmation {
		c := domain.Confirmation{
			ID: domain.ConfirmationID(newID()), OwnerID: alice.ID, Kind: kind, WorkflowID: wf, GrantID: "g-1", Agent: "Claude Code",
			Scopes: []domain.Scope{domain.ScopeBuild, domain.ScopeLive}, What: "Run it", Payload: json.RawMessage(`{"trigger":"manual"}`),
			Status: domain.ConfirmPending, CreatedAt: created, ExpiresAt: created.Add(time.Hour),
		}
		require.NoError(t, repo.Create(ctx, c))

		return c
	}

	run := ask(domain.ConfirmRun, w.ID, at)
	kill := ask(domain.ConfirmKillOff, "", at.Add(time.Minute))

	t.Run("kept as asked, its owner's", func(t *testing.T) {
		got, err := repo.Get(ctx, alice.ID, run.ID)
		require.NoError(t, err)
		assert.JSONEq(t, `{"trigger":"manual"}`, string(got.Payload))
		got.Payload = run.Payload
		assert.Equal(t, run, got)

		_, err = repo.Get(ctx, bob.ID, run.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		none, err := repo.Get(ctx, alice.ID, kill.ID)
		require.NoError(t, err)
		assert.Empty(t, none.WorkflowID, "the kill switch is no workflow's")

		list, err := repo.Recent(ctx, alice.ID, 10)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, kill.ID, list[0].ID, "the newest first")

		n, err := repo.CountPending(ctx, alice.ID, at.Add(30*time.Minute))
		require.NoError(t, err)
		assert.Equal(t, 2, n)

		n, err = repo.CountPending(ctx, alice.ID, at.Add(time.Hour))
		require.NoError(t, err)
		assert.Equal(t, 1, n, "the first expired")
	})

	t.Run("decided once, in time, by its owner", func(t *testing.T) {
		_, err := repo.Decide(ctx, bob.ID, run.ID, domain.ConfirmConfirmed, at)
		require.ErrorIs(t, err, domain.ErrNotFound)

		decided, err := repo.Decide(ctx, alice.ID, run.ID, domain.ConfirmConfirmed, at.Add(time.Minute))
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmConfirmed, decided.Status)
		assert.Equal(t, at.Add(time.Minute), decided.DecidedAt)

		_, err = repo.Decide(ctx, alice.ID, run.ID, domain.ConfirmRefused, at.Add(time.Minute))
		require.ErrorIs(t, err, domain.ErrConflict)

		_, err = repo.Decide(ctx, alice.ID, kill.ID, domain.ConfirmConfirmed, at.Add(2*time.Hour))
		require.ErrorIs(t, err, domain.ErrConflict, "expired")

		require.NoError(t, repo.Finish(ctx, run.ID, domain.ConfirmDone, "execution ex-1, succeeded"))

		done, err := repo.Get(ctx, alice.ID, run.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ConfirmDone, done.Status)
		assert.Equal(t, "execution ex-1, succeeded", done.Result)
	})
}

func TestUnconfirmed(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	users := postgres.NewUsers(db)
	u := seedUser(t, db, "a@x.io", domain.RoleUser)

	got, err := users.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.Unconfirmed{}, got.Unconfirmed, "every live action waits to start with")

	lets := domain.Unconfirmed{Run: true, KillOff: true}
	require.NoError(t, users.SetUnconfirmed(ctx, u.ID, lets))
	require.NoError(t, users.Update(ctx, got), "an update with the user as it was")

	got, err = users.ByID(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, lets, got.Unconfirmed)
}
