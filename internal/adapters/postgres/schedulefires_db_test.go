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

// When each schedule making up missed runs last fired (D440).
func TestScheduleFires(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	store := postgres.NewScheduleFires(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	wf := newWorkflow(alice.ID, "scheduled")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, wf))

	last, err := store.LastFire(ctx, wf.ID, "every")
	require.NoError(t, err)
	assert.True(t, last.IsZero(), "never fired")

	require.NoError(t, store.Fired(ctx, wf.ID, "every", at))
	require.NoError(t, store.Fired(ctx, wf.ID, "every", at.Add(time.Hour)))
	require.NoError(t, store.Fired(ctx, wf.ID, "other", at))

	last, err = store.LastFire(ctx, wf.ID, "every")
	require.NoError(t, err)
	assert.True(t, last.Equal(at.Add(time.Hour)), "the latest: %v", last)

	last, err = store.LastFire(ctx, "not-a-uuid", "every")
	require.NoError(t, err)
	assert.True(t, last.IsZero())

	require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, wf.ID))

	last, err = store.LastFire(ctx, wf.ID, "every")
	require.NoError(t, err)
	assert.True(t, last.IsZero(), "gone with its workflow")
}
