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

// Each Inbound webhook trigger's secret address (D439): found by its hash, renewed in
// place, gone with its workflow.
func TestHooks(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	store := postgres.NewHooks(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	wf := newWorkflow(alice.ID, "hooked")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, wf))

	_, err := store.Hook(ctx, wf.ID, "hook")
	require.ErrorIs(t, err, domain.ErrNotFound, "none until asked for")

	require.NoError(t, store.Put(ctx, domain.Hook{WorkflowID: wf.ID, NodeID: "hook", Sealed: []byte("sealed-1"), CreatedAt: at}, []byte("hash-1")))

	h, err := store.Hook(ctx, wf.ID, "hook")
	require.NoError(t, err)
	assert.Equal(t, domain.Hook{WorkflowID: wf.ID, NodeID: "hook", OwnerID: alice.ID, Sealed: []byte("sealed-1"), CreatedAt: at.UTC()}, h)

	byHash, err := store.ByHash(ctx, []byte("hash-1"))
	require.NoError(t, err)
	assert.Equal(t, h, byHash)

	// Renewed: the old secret finds nothing.
	later := at.Add(1e9)
	require.NoError(t, store.Put(ctx, domain.Hook{WorkflowID: wf.ID, NodeID: "hook", Sealed: []byte("sealed-2"), CreatedAt: later}, []byte("hash-2")))

	_, err = store.ByHash(ctx, []byte("hash-1"))
	require.ErrorIs(t, err, domain.ErrNotFound)

	h, err = store.ByHash(ctx, []byte("hash-2"))
	require.NoError(t, err)
	assert.Equal(t, []byte("sealed-2"), h.Sealed)
	assert.True(t, h.CreatedAt.Equal(later))

	// Another trigger of the workflow has its own.
	require.NoError(t, store.Put(ctx, domain.Hook{WorkflowID: wf.ID, NodeID: "other", Sealed: []byte("s"), CreatedAt: at}, []byte("hash-3")))

	h, err = store.ByHash(ctx, []byte("hash-3"))
	require.NoError(t, err)
	assert.Equal(t, domain.NodeID("other"), h.NodeID)

	_, err = store.Hook(ctx, "not-a-uuid", "hook")
	require.ErrorIs(t, err, domain.ErrNotFound)

	require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, wf.ID))

	_, err = store.ByHash(ctx, []byte("hash-2"))
	require.ErrorIs(t, err, domain.ErrNotFound, "gone with its workflow")
}
