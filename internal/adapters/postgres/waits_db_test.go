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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// seedWaiting saves a workflow of owner with an execution waiting on it until until.
func seedWaiting(t *testing.T, db *postgres.DB, owner domain.User, until time.Time) ports.WaitRecord {
	t.Helper()

	return seedAsked(t, db, owner, until, "")
}

// seedAsked is seedWaiting for a wait that takes answers with key.
func seedAsked(t *testing.T, db *postgres.DB, owner domain.User, until time.Time, key string) ports.WaitRecord {
	t.Helper()
	ctx := context.Background()

	w := newWorkflow(owner.ID, "waits")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	e := domain.Execution{
		ID: domain.ExecutionID(newID()), WorkflowID: w.ID, WorkflowVersion: 1, TriggeredBy: "manual",
		Status: domain.ExecutionWaiting, StartedAt: at, WaitingOn: "pause", WaitingUntil: until,
		Runs: []domain.NodeRun{{NodeID: "pause", Status: domain.NodeRunWaiting, StartedAt: at}},
	}
	require.NoError(t, postgres.NewExecutions(db).Save(ctx, e))

	rec := ports.WaitRecord{
		ExecutionID: e.ID, WorkflowID: w.ID, OwnerID: owner.ID, NodeID: "pause",
		Until: until, Key: key, State: json.RawMessage(`{"pending":{"x":0.0200}}`), CreatedAt: at,
	}
	require.NoError(t, postgres.NewWaits(db).Add(ctx, rec))

	return rec
}

func TestWaits(t *testing.T) {
	ctx := context.Background()

	t.Run("a waiting execution saves again when it carries on", func(t *testing.T) {
		db := fresh(t)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		rec := seedWaiting(t, db, alice, at.Add(time.Hour))
		execs := postgres.NewExecutions(db)

		got, err := execs.Get(ctx, rec.WorkflowID, rec.ExecutionID)
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionWaiting, got.Status)
		assert.Equal(t, domain.NodeID("pause"), got.WaitingOn)
		assert.Equal(t, at.Add(time.Hour), got.WaitingUntil)

		got.Status, got.WaitingOn, got.WaitingUntil, got.FinishedAt = domain.ExecutionSucceeded, "", time.Time{}, at.Add(2*time.Hour)
		got.Runs[0].Status = domain.NodeRunSucceeded
		require.NoError(t, execs.Save(ctx, got))

		again, err := execs.Get(ctx, rec.WorkflowID, rec.ExecutionID)
		require.NoError(t, err)
		assert.Equal(t, got, again)
	})

	t.Run("taken once, by its owner", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewWaits(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)
		rec := seedWaiting(t, db, alice, at.Add(time.Hour))

		n, err := repo.CountForOwner(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		_, err = repo.Take(ctx, bob.ID, rec.ExecutionID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		got, err := repo.Take(ctx, alice.ID, rec.ExecutionID)
		require.NoError(t, err)
		assert.Equal(t, rec.NodeID, got.NodeID)
		assert.Equal(t, rec.Until, got.Until)
		assert.JSONEq(t, `{"pending":{"x":0.0200}}`, string(got.State))

		_, err = repo.Take(ctx, alice.ID, rec.ExecutionID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		n, err = repo.CountForOwner(ctx, alice.ID)
		require.NoError(t, err)
		assert.Zero(t, n)
	})

	t.Run("answered only with its key", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewWaits(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)
		asked := seedAsked(t, db, alice, at.Add(time.Hour), "k1")
		timer := seedWaiting(t, db, alice, at.Add(time.Hour))

		for _, try := range []struct {
			owner domain.UserID
			id    domain.ExecutionID
			key   string
		}{
			{bob.ID, asked.ExecutionID, "k1"},
			{alice.ID, asked.ExecutionID, "k0"},
			{alice.ID, asked.ExecutionID, ""},
			{alice.ID, timer.ExecutionID, ""},
			{alice.ID, "not-an-id", "k1"},
		} {
			_, err := repo.TakeWithKey(ctx, try.owner, try.id, try.key)
			require.ErrorIs(t, err, domain.ErrNotFound, "%+v", try)
		}

		got, err := repo.TakeWithKey(ctx, alice.ID, asked.ExecutionID, "k1")
		require.NoError(t, err)
		assert.Equal(t, "k1", got.Key)

		_, err = repo.TakeWithKey(ctx, alice.ID, asked.ExecutionID, "k1")
		require.ErrorIs(t, err, domain.ErrNotFound)

		got, err = repo.Take(ctx, alice.ID, timer.ExecutionID)
		require.NoError(t, err, "a cancel takes any wait")
		assert.Empty(t, got.Key)
	})

	t.Run("due ones, earliest first, a few at a time", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewWaits(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		later := seedWaiting(t, db, alice, at.Add(2*time.Minute))
		first := seedWaiting(t, db, alice, at.Add(time.Minute))
		seedWaiting(t, db, alice, at.Add(time.Hour))

		due, err := repo.TakeDue(ctx, at.Add(5*time.Minute), 1)
		require.NoError(t, err)
		require.Len(t, due, 1)
		assert.Equal(t, first.ExecutionID, due[0].ExecutionID)

		due, err = repo.TakeDue(ctx, at.Add(5*time.Minute), 10)
		require.NoError(t, err)
		require.Len(t, due, 1)
		assert.Equal(t, later.ExecutionID, due[0].ExecutionID)

		due, err = repo.TakeDue(ctx, at.Add(5*time.Minute), 10)
		require.NoError(t, err)
		assert.Empty(t, due, "the hour-long one is not due")
	})

	t.Run("all of a workflow's, and they go with it", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewWaits(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)
		rec := seedWaiting(t, db, alice, at.Add(time.Hour))
		other := seedWaiting(t, db, alice, at.Add(time.Hour))

		got, err := repo.TakeForWorkflow(ctx, bob.ID, rec.WorkflowID)
		require.NoError(t, err)
		assert.Empty(t, got)

		got, err = repo.TakeForWorkflow(ctx, alice.ID, rec.WorkflowID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, rec.ExecutionID, got[0].ExecutionID)

		require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, other.WorkflowID))

		n, err := repo.CountForOwner(ctx, alice.ID)
		require.NoError(t, err)
		assert.Zero(t, n)
	})
}
