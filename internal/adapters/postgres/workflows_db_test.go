//go:build db

package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
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
			{ID: "manual", Type: "trigger.manual", Position: domain.CanvasPosition{X: 120, Y: -40.5}},
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
			assert.ErrorIs(t, repo.SetActive(ctx, stranger.ID, w.ID, true, at, domain.Actor{}), domain.ErrNotFound)

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
			require.NoError(t, repo.SetActive(ctx, owner.ID, b.ID, true, at, domain.Actor{}))

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
		{"deactivating everything a user runs", func(t *testing.T, db *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			a, b, idle := newWorkflow(owner.ID, "a"), newWorkflow(owner.ID, "b"), newWorkflow(owner.ID, "idle")
			a.Active, b.Active = true, true

			other := newWorkflow(seedUser(t, db, "eve@x.io", domain.RoleUser).ID, "other")
			other.Active = true

			for _, w := range []domain.Workflow{a, b, idle, other} {
				require.NoError(t, repo.Create(ctx, w))
			}

			ids, err := repo.DeactivateAll(ctx, owner.ID, at.Add(time.Hour))
			require.NoError(t, err)
			assert.ElementsMatch(t, []domain.WorkflowID{a.ID, b.ID}, ids)

			_, active, err := repo.Count(ctx, owner.ID)
			require.NoError(t, err)
			assert.Zero(t, active)

			got, err := repo.Get(ctx, owner.ID, a.ID)
			require.NoError(t, err)
			assert.Equal(t, at.Add(time.Hour), got.UpdatedAt)
			assert.Equal(t, 1, got.Version, "deactivating is not an edit")

			again, err := repo.DeactivateAll(ctx, owner.ID, at)
			require.NoError(t, err)
			assert.Empty(t, again)

			all, err := repo.ListActive(ctx)
			require.NoError(t, err)
			require.Len(t, all, 1)
			assert.Equal(t, other.ID, all[0].ID, "other users' workflows keep running")
		}},
		{"boot leaves out disabled users", func(t *testing.T, db *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			w := newWorkflow(owner.ID, "still marked active")
			w.Active = true
			require.NoError(t, repo.Create(ctx, w))

			owner.DisabledAt = at
			require.NoError(t, postgres.NewUsers(db).Update(ctx, owner))

			all, err := repo.ListActive(ctx)
			require.NoError(t, err)
			assert.Empty(t, all)
		}},
		{"list pages by owner", func(t *testing.T, _ *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			for _, n := range []string{"a", "b", "c"} {
				require.NoError(t, repo.Create(ctx, newWorkflow(owner.ID, n)))
			}

			first, err := repo.List(ctx, owner.ID, ports.WorkflowFilter{PageFilter: ports.PageFilter{Limit: 2}})
			require.NoError(t, err)
			require.Len(t, first.Items, 2)
			assert.Equal(t, "c", first.Items[0].Name, "the newest first")

			second, err := repo.List(ctx, owner.ID, ports.WorkflowFilter{PageFilter: ports.PageFilter{Limit: 2, Cursor: first.NextCursor}})
			require.NoError(t, err)
			require.Len(t, second.Items, 1)
			assert.Equal(t, "a", second.Items[0].Name)
			assert.Empty(t, second.NextCursor)
		}},
		{"searched by name and description, as typed", func(t *testing.T, _ *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			for _, w := range []struct{ name, description string }{
				{"Spread watch", "Logs tight books"},
				{"Copy whales", "Follows the 100% club"},
				{"Weather_bets", ""},
			} {
				wf := newWorkflow(owner.ID, w.name)
				wf.Description = w.description
				require.NoError(t, repo.Create(ctx, wf))
			}

			names := func(q string) []string {
				page, err := repo.List(ctx, owner.ID, ports.WorkflowFilter{Query: q})
				require.NoError(t, err)

				out := []string{}
				for _, w := range page.Items {
					out = append(out, w.Name)
				}

				return out
			}

			assert.Equal(t, []string{"Spread watch"}, names("TIGHT"), "the description, any case")
			assert.Equal(t, []string{"Copy whales"}, names("100%"), "a % as it is")
			assert.Equal(t, []string{"Weather_bets"}, names("r_b"), "an _ as it is")
			assert.Empty(t, names("100_"))
			assert.Len(t, names(" "), 3, "nothing to look for")

			page, err := repo.List(ctx, owner.ID, ports.WorkflowFilter{Active: new(true)})
			require.NoError(t, err)
			assert.Empty(t, page.Items, "none switched on")
		}},
		{"pinned in their order, apart from the rest", func(t *testing.T, _ *postgres.DB, repo *postgres.Workflows, owner domain.User) {
			ids := map[string]domain.WorkflowID{}
			for _, n := range []string{"a", "b", "c", "d"} {
				w := newWorkflow(owner.ID, n)
				require.NoError(t, repo.Create(ctx, w))
				ids[n] = w.ID
			}

			names := func(pinned bool) []string {
				page, err := repo.List(ctx, owner.ID, ports.WorkflowFilter{Pinned: &pinned})
				require.NoError(t, err)

				out := []string{}
				for _, w := range page.Items {
					out = append(out, fmt.Sprintf("%s%d", w.Name, w.Pin))
				}

				return out
			}

			for _, n := range []string{"c", "a", "d"} {
				require.NoError(t, repo.SetPin(ctx, owner.ID, ids[n], true))
			}

			require.NoError(t, repo.SetPin(ctx, owner.ID, ids["a"], true), "pinned already: it stays where it is")
			assert.Equal(t, []string{"c1", "a2", "d3"}, names(true), "each after the others")
			assert.Equal(t, []string{"b0"}, names(false))

			require.NoError(t, repo.SetPin(ctx, owner.ID, ids["c"], false))
			assert.Equal(t, []string{"a1", "d2"}, names(true), "the gap closed")

			require.ErrorIs(t, repo.SetPinOrder(ctx, owner.ID, []domain.WorkflowID{ids["d"]}), domain.ErrConflict, "every one of them")
			require.ErrorIs(t, repo.SetPinOrder(ctx, owner.ID, []domain.WorkflowID{ids["d"], ids["b"]}), domain.ErrConflict, "b is not pinned")
			require.ErrorIs(t, repo.SetPinOrder(ctx, owner.ID, []domain.WorkflowID{ids["d"], ids["d"]}), domain.ErrConflict, "each once")
			require.NoError(t, repo.SetPinOrder(ctx, owner.ID, []domain.WorkflowID{ids["d"], ids["a"]}))
			assert.Equal(t, []string{"d1", "a2"}, names(true))

			got, err := repo.Get(ctx, owner.ID, ids["d"])
			require.NoError(t, err)
			assert.Equal(t, 1, got.Version, "a pin is no change to it")

			require.ErrorIs(t, repo.SetPin(ctx, owner.ID, domain.WorkflowID(newID()), true), domain.ErrNotFound)
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
				{
					NodeID: "manual", Status: domain.NodeRunSucceeded, StartedAt: started, FinishedAt: started, Output: map[string]int{"main": 2},
					Samples: map[string][]domain.ItemSample{"main": {{JSON: json.RawMessage(`{"price":0.020,"ok":true}`)}, {JSON: json.RawMessage(`{}`)}}},
				},
				{
					NodeID: "if", Status: domain.NodeRunFailed, StartedAt: started, FinishedAt: started.Add(time.Second), Input: map[string]int{"main": 1}, Error: "boom",
					Samples: map[string][]domain.ItemSample{"error": {{JSON: json.RawMessage(`{"v":1}`), Error: "boom"}}},
				},
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

			// jsonb keeps the items' meaning, not their bytes: keys may be reordered.
			for i := range e.Runs {
				for port, want := range e.Runs[i].Samples {
					require.Len(t, got.Runs[i].Samples[port], len(want))

					for j := range want {
						assert.JSONEq(t, string(want[j].JSON), string(got.Runs[i].Samples[port][j].JSON))
						assert.Equal(t, want[j].Error, got.Runs[i].Samples[port][j].Error)
					}
				}

				e.Runs[i].Samples, got.Runs[i].Samples = nil, nil
			}

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
