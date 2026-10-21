//go:build db

package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func TestPluginStore(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	store := postgres.NewPluginStore(db)
	mem := postgres.NewNodeMemory(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "weather")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	node := ports.PluginNode{Workflow: w.ID, Node: "forecast", Plugin: "weather"}

	t.Run("a value by key, exactly as given, replaced", func(t *testing.T) {
		require.NoError(t, store.Set(ctx, node, "cursor", json.RawMessage(`{"page": 2, "odds": 0.4200}`), 1024, now))

		got, ok, err := store.Get(ctx, node, "cursor")
		require.NoError(t, err)
		require.True(t, ok)
		assert.JSONEq(t, `{"page": 2, "odds": 0.4200}`, string(got))
		assert.Equal(t, `{"page": 2, "odds": 0.4200}`, string(got), "its text kept, 0.4200 and all")

		require.NoError(t, store.Set(ctx, node, "cursor", json.RawMessage(`3`), 1024, now))
		got, _, _ = store.Get(ctx, node, "cursor")
		assert.Equal(t, "3", string(got))

		_, ok, err = store.Get(ctx, node, "none")
		require.NoError(t, err)
		assert.False(t, ok)

		other := ports.PluginNode{Workflow: w.ID, Node: "forecast", Plugin: "other"}
		_, ok, _ = store.Get(ctx, other, "cursor")
		assert.False(t, ok, "another plugin's node of the same ID sees none of it")
	})

	t.Run("keys in byte order, by prefix and page", func(t *testing.T) {
		for _, k := range []string{"seen:b", "seen:a", "seen:B", "other"} {
			require.NoError(t, store.Set(ctx, node, k, json.RawMessage(`true`), 1024, now))
		}

		keys, err := store.Keys(ctx, node, "seen:", "", 10)
		require.NoError(t, err)
		assert.Equal(t, []string{"seen:B", "seen:a", "seen:b"}, keys)

		keys, err = store.Keys(ctx, node, "seen:", "seen:B", 1)
		require.NoError(t, err)
		assert.Equal(t, []string{"seen:a"}, keys, "the next page")

		require.NoError(t, store.Delete(ctx, node, "seen:a"))
		keys, _ = store.Keys(ctx, node, "", "", 10)
		assert.Equal(t, []string{"cursor", "other", "seen:B", "seen:b"}, keys)
	})

	t.Run("no more than the node's bytes, two at once among them", func(t *testing.T) {
		small := ports.PluginNode{Workflow: w.ID, Node: "small", Plugin: "weather"}

		require.NoError(t, store.Set(ctx, small, "a", json.RawMessage(`"12345678"`), 24, now), "11 bytes")
		require.ErrorIs(t, store.Set(ctx, small, "b", json.RawMessage(`"1234567890123"`), 24, now), domain.ErrStoreFull, "11 and 16")
		require.NoError(t, store.Set(ctx, small, "a", json.RawMessage(`"12345678901234567890"`), 24, now), "23 bytes: a replaced, its own bytes not counted twice")
		require.ErrorIs(t, store.Set(ctx, small, "big", json.RawMessage(`"x"`), 2, now), domain.ErrStoreFull, "past max alone")

		race := ports.PluginNode{Workflow: w.ID, Node: "race", Plugin: "weather"}

		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			kept int
		)

		for i := range 8 {
			wg.Go(func() {
				if store.Set(ctx, race, fmt.Sprintf("k%d", i), json.RawMessage(`"123456"`), 30, now) == nil {
					mu.Lock()
					kept++
					mu.Unlock()
				}
			})
		}

		wg.Wait()
		assert.Equal(t, 3, kept, "10 bytes each, 30 in all")
	})

	t.Run("it counts as the node's memory, and goes as it does", func(t *testing.T) {
		require.NoError(t, store.Set(ctx, ports.PluginNode{Workflow: w.ID, Node: "dedupe", Plugin: "weather"}, "k", json.RawMessage(`1`), 1024, now))
		_, err := mem.Remember(ctx, w.ID, "dedupe", []string{"x", "y"}, now, now.Add(time.Hour))
		require.NoError(t, err)

		n, err := mem.Count(ctx, w.ID, "dedupe", now)
		require.NoError(t, err)
		assert.Equal(t, 3, n, "two remembered, one stored")

		n, err = mem.Count(ctx, w.ID, "forecast", now)
		require.NoError(t, err)
		assert.Equal(t, 4, n, "stored only")

		require.NoError(t, mem.Forget(ctx, w.ID, "dedupe"))
		n, _ = mem.Count(ctx, w.ID, "dedupe", now)
		assert.Zero(t, n)

		require.NoError(t, mem.ForgetOthers(ctx, w.ID, []domain.NodeID{"forecast"}))
		keys, _ := store.Keys(ctx, ports.PluginNode{Workflow: w.ID, Node: "small", Plugin: "weather"}, "", "", 10)
		assert.Empty(t, keys, "a node gone, its store too")

		keys, _ = store.Keys(ctx, node, "", "", 10)
		assert.NotEmpty(t, keys, "the ones kept keep theirs")

		require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, w.ID))
		keys, _ = store.Keys(ctx, node, "", "", 10)
		assert.Empty(t, keys, "gone with the workflow")
	})
}
