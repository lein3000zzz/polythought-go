//go:build db

package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func TestNodeMemory(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	mem := postgres.NewNodeMemory(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "alerts")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	day := now.Add(24 * time.Hour)

	t.Run("new keys come back, held ones do not, a key given twice once", func(t *testing.T) {
		fresh, err := mem.Remember(ctx, w.ID, "new", []string{"a", "b", "a"}, now, day)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, fresh)

		fresh, err = mem.Remember(ctx, w.ID, "new", []string{"c", "b"}, now.Add(time.Hour), day)
		require.NoError(t, err)
		assert.Equal(t, []string{"c"}, fresh)

		n, err := mem.Count(ctx, w.ID, "new", now)
		require.NoError(t, err)
		assert.Equal(t, 3, n)

		fresh, err = mem.Remember(ctx, w.ID, "other", []string{"a"}, now, day)
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, fresh, "each node remembers for itself")
	})

	t.Run("an expired key is new again, and counts no more", func(t *testing.T) {
		later := day.Add(time.Minute)

		n, err := mem.Count(ctx, w.ID, "new", later)
		require.NoError(t, err)
		assert.Zero(t, n)

		fresh, err := mem.Remember(ctx, w.ID, "new", []string{"a"}, later, later.Add(time.Hour))
		require.NoError(t, err)
		assert.Equal(t, []string{"a"}, fresh)
	})

	t.Run("past the limit, the oldest go first (D138)", func(t *testing.T) {
		keys := make([]string, ports.MaxRemembered)
		for i := range keys {
			keys[i] = fmt.Sprintf("k%05d", i)
		}

		_, err := mem.Remember(ctx, w.ID, "full", keys, now, day)
		require.NoError(t, err)

		_, err = mem.Remember(ctx, w.ID, "full", []string{"newest"}, now.Add(time.Second), day)
		require.NoError(t, err)

		n, err := mem.Count(ctx, w.ID, "full", now)
		require.NoError(t, err)
		assert.Equal(t, ports.MaxRemembered, n)

		fresh, err := mem.Remember(ctx, w.ID, "full", []string{"newest", "k00000"}, now.Add(2*time.Second), day)
		require.NoError(t, err)
		assert.Equal(t, []string{"k00000"}, fresh, "the oldest made room: of those as old, the first")
	})

	t.Run("forgetting a node, the others gone, and the expired", func(t *testing.T) {
		require.NoError(t, mem.Forget(ctx, w.ID, "other"))
		n, err := mem.Count(ctx, w.ID, "other", now)
		require.NoError(t, err)
		assert.Zero(t, n)

		require.NoError(t, mem.ForgetOthers(ctx, w.ID, []domain.NodeID{"new"}))
		n, err = mem.Count(ctx, w.ID, "full", now)
		require.NoError(t, err)
		assert.Zero(t, n, "no longer in the workflow")

		swept, err := mem.Sweep(ctx, day.Add(2*time.Hour))
		require.NoError(t, err)
		assert.Equal(t, 3, swept, "the new node's b, c, and a again")

		require.NoError(t, mem.ForgetOthers(ctx, w.ID, nil))
	})

	t.Run("a deleted workflow takes its memory along", func(t *testing.T) {
		_, err := mem.Remember(ctx, w.ID, "new", []string{"x"}, now, day)
		require.NoError(t, err)

		require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, w.ID))

		swept, err := mem.Sweep(ctx, day.Add(time.Hour))
		require.NoError(t, err)
		assert.Zero(t, swept)
	})
}
