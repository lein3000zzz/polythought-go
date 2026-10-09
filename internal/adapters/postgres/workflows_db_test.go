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

func newWorkflow(owner domain.UserID, name string) domain.Workflow {
	return domain.Workflow{
		ID:      domain.WorkflowID(newID()),
		OwnerID: owner,
		Name:    name,
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"},
			{ID: "if", Type: "logic.if", Name: "Tight?", Params: map[string]any{"field": "spread", "value": json.Number("0.02")}, ContinueOnFail: true},
		},
		Connections: []domain.Connection{{From: "manual", FromPort: "main", To: "if", ToPort: "main"}},
		Version:     1,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

func TestWorkflows(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, repo *postgres.Workflows, owner domain.User)
	}{
		{"round trip keeps the graph and exact numbers", func(t *testing.T, _ *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			w := newWorkflow(owner.ID, "spreads")
			require.NoError(t, repo.Create(ctx, w))

			got, err := repo.Get(ctx, owner.ID, w.ID)
			require.NoError(t, err)
			assert.Equal(t, w, got)
		}},
		{"other users cannot reach it", func(t *testing.T, db *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			w := newWorkflow(owner.ID, "spreads")
			require.NoError(t, repo.Create(ctx, w))

			stranger := seedUser(t, db, "eve@x.io", domain.RoleUser)

			_, err := repo.Get(ctx, stranger.ID, w.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
			assert.ErrorIs(t, repo.Delete(ctx, stranger.ID, w.ID), domain.ErrNotFound)
			assert.ErrorIs(t, repo.SetActive(ctx, stranger.ID, w.ID, true, at), domain.ErrNotFound)

			w.OwnerID = stranger.ID
			w.Version = 2
			assert.ErrorIs(t, repo.Update(ctx, w, 1), domain.ErrNotFound)
		}},
		{"update is a compare-and-swap on the version", func(t *testing.T, _ *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			w := newWorkflow(owner.ID, "v1")
			require.NoError(t, repo.Create(ctx, w))

			w.Name, w.Version, w.UpdatedAt = "v2", 2, at.Add(time.Minute)
			require.NoError(t, repo.Update(ctx, w, 1))

			stale := w
			stale.Name, stale.Version = "stale edit", 2
			assert.ErrorIs(t, repo.Update(ctx, stale, 1), domain.ErrConflict)

			got, err := repo.Get(ctx, owner.ID, w.ID)
			require.NoError(t, err)
			assert.Equal(t, "v2", got.Name)
			assert.Equal(t, 2, got.Version)
		}},
		{"activation, counts and the active list", func(t *testing.T, db *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			a, b := newWorkflow(owner.ID, "a"), newWorkflow(owner.ID, "b")
			require.NoError(t, repo.Create(ctx, a))
			require.NoError(t, repo.Create(ctx, b))
			require.NoError(t, repo.SetActive(ctx, owner.ID, b.ID, true, at))

			other := newWorkflow(seedUser(t, db, "eve@x.io", domain.RoleUser).ID, "c")
			other.Active = true
			require.NoError(t, repo.Create(ctx, other))

			total, active, err := repo.Count(ctx, owner.ID)
			require.NoError(t, err)
			assert.Equal(t, 2, total)
			assert.Equal(t, 1, active)

			all, err := repo.ListActive(ctx)
			require.NoError(t, err)
			assert.ElementsMatch(t, []domain.WorkflowID{b.ID, other.ID}, []domain.WorkflowID{all[0].ID, all[1].ID})
		}},
		{"list pages by owner", func(t *testing.T, _ *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			for _, n := range []string{"a", "b", "c"} {
				require.NoError(t, repo.Create(ctx, newWorkflow(owner.ID, n)))
			}

			first, err := repo.List(ctx, owner.ID, ports.PageFilter{Limit: 2})
			require.NoError(t, err)
			require.Len(t, first.Items, 2)

			second, err := repo.List(ctx, owner.ID, ports.PageFilter{Limit: 2, Cursor: first.NextCursor})
			require.NoError(t, err)
			require.Len(t, second.Items, 1)
			assert.Equal(t, "c", second.Items[0].Name)
			assert.Empty(t, second.NextCursor)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewWorkflows(db), seedUser(t, db, "owner@x.io", domain.RoleUser))
		})
	}
}

func TestExecutions(t *testing.T) {
	ctx := context.Background()

	newExecution := func(w domain.Workflow, started time.Time, status domain.ExecutionStatus) domain.Execution {
		return domain.Execution{
			ID: domain.ExecutionID(newID()), WorkflowID: w.ID, WorkflowVersion: 3, TriggeredBy: "manual",
			Status: status, StartedAt: started, FinishedAt: started.Add(time.Second), Error: "",
			Runs: []domain.NodeRun{
				{NodeID: "manual", Status: domain.NodeRunSucceeded, StartedAt: started, FinishedAt: started, Output: map[string]int{"main": 1}},
				{NodeID: "if", Status: domain.NodeRunFailed, StartedAt: started, FinishedAt: started.Add(time.Second), Input: map[string]int{"main": 1}, Error: "boom"},
			},
		}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, execs *postgres.Executions, w domain.Workflow)
	}{
		{"round trip", func(t *testing.T, execs *postgres.Executions, w domain.Workflow) {
			e := newExecution(w, at, domain.ExecutionFailed)
			require.NoError(t, execs.Save(ctx, e))

			got, err := execs.Get(ctx, w.ID, e.ID)
			require.NoError(t, err)
			assert.Equal(t, e, got)

			_, err = execs.Get(ctx, domain.WorkflowID(newID()), e.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound, "executions are only found under their workflow")
		}},
		{"newest first, paged", func(t *testing.T, execs *postgres.Executions, w domain.Workflow) {
			var ids []domain.ExecutionID
			for i := range 3 {
				e := newExecution(w, at.Add(time.Duration(i)*time.Minute), domain.ExecutionSucceeded)
				require.NoError(t, execs.Save(ctx, e))
				ids = append(ids, e.ID)
			}

			first, err := execs.List(ctx, w.ID, ports.PageFilter{Limit: 2})
			require.NoError(t, err)
			assert.Equal(t, []domain.ExecutionID{ids[2], ids[1]}, []domain.ExecutionID{first.Items[0].ID, first.Items[1].ID})

			second, err := execs.List(ctx, w.ID, ports.PageFilter{Limit: 2, Cursor: first.NextCursor})
			require.NoError(t, err)
			require.Len(t, second.Items, 1)
			assert.Equal(t, ids[0], second.Items[0].ID)
		}},
		{"retention pruning", func(t *testing.T, execs *postgres.Executions, w domain.Workflow) {
			old := newExecution(w, at.Add(-48*time.Hour), domain.ExecutionSucceeded)
			recent := newExecution(w, at, domain.ExecutionSucceeded)
			require.NoError(t, execs.Save(ctx, old))
			require.NoError(t, execs.Save(ctx, recent))

			n, err := execs.DeleteFinishedBefore(ctx, at.Add(-24*time.Hour))
			require.NoError(t, err)
			assert.Equal(t, int64(1), n)

			_, err = execs.Get(ctx, w.ID, recent.ID)
			assert.NoError(t, err)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			w := newWorkflow(seedUser(t, db, "owner@x.io", domain.RoleUser).ID, "flow")
			require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

			tt.run(t, postgres.NewExecutions(db), w)
		})
	}
}
