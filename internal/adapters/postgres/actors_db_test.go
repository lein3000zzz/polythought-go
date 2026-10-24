//go:build db

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Who acted is kept on the workflow until it is switched again, and on each execution
// (D407).
func TestActedBy(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewWorkflows(db)
	owner := seedUser(t, db, "a@x.io", domain.RoleUser)

	w := newWorkflow(owner.ID, "acted")
	require.NoError(t, repo.Create(ctx, w))

	switchedOn := domain.Actor{GrantID: "g-1", Name: "Claude Code", How: domain.ActorSwitchedOn, Confirmed: true}
	require.NoError(t, repo.SetActive(ctx, owner.ID, w.ID, true, at, switchedOn))

	got, err := repo.Get(ctx, owner.ID, w.ID)
	require.NoError(t, err)
	assert.Equal(t, switchedOn, got.ActedBy)

	active, err := repo.ListActive(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, switchedOn, active[0].ActedBy, "as its triggers start again")

	changed := domain.Actor{TokenID: "t-1", Name: "deploy", How: domain.ActorChanged}
	got.ActedBy, got.Version = changed, got.Version+1
	require.NoError(t, repo.Update(ctx, got, got.Version-1))

	got, err = repo.Get(ctx, owner.ID, w.ID)
	require.NoError(t, err)
	assert.Equal(t, changed, got.ActedBy)

	_, err = repo.DeactivateAll(ctx, owner.ID, at)
	require.NoError(t, err)

	got, err = repo.Get(ctx, owner.ID, w.ID)
	require.NoError(t, err)
	assert.True(t, got.ActedBy.IsUser(), "off, no one")

	execs := postgres.NewExecutions(db)
	e := domain.NewExecution(domain.ExecutionID(newID()), w.ID, "trigger", at)
	e.By = domain.Actor{GrantID: "g-1", Name: "Claude Code", How: domain.ActorRan}
	require.NoError(t, execs.Save(ctx, e))

	saved, err := execs.Get(ctx, w.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.By, saved.By)
}
