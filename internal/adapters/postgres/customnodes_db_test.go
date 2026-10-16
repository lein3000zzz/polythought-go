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

func newCustomNode(owner domain.UserID, name string) domain.CustomNode {
	return domain.CustomNode{
		ID: domain.CustomNodeID(newID()), OwnerID: owner, Name: name, Label: "Spread split",
		Description: "Wide books one way, tight ones the other.",
		Settings: []domain.CustomSetting{
			{Name: "threshold", Label: "Threshold", Kind: "decimal", Required: true, Default: json.Number("0.0200")},
			{Name: "on", Kind: "string", Field: true, FieldKinds: []string{"decimal", "number"}, Default: "spread"},
			{Name: "side", Kind: "string", Options: []string{"buy", "sell"}},
		},
		Outputs: []string{"wide", "tight"},
		Items: domain.CustomItems{Fields: []domain.CustomField{
			{Name: "count", Kind: "number", Description: "How many"},
			{Name: "best", Kind: "object", Nullable: true, Fields: []domain.CustomField{{Name: "price", Kind: "decimal"}}},
			{Name: "tags", Kind: "list", Optional: true, Of: &domain.CustomField{Kind: "text", Options: []string{"a"}, Fields: []domain.CustomField{}}},
		}},
		Source:  "export function run(items) { return { wide: items } }",
		Version: 1, CreatedAt: at, UpdatedAt: at,
	}
}

func TestCustomNodes(t *testing.T) {
	ctx := context.Background()

	t.Run("round trip, listed by name, numbers kept exact", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCustomNodes(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

		b := newCustomNode(alice.ID, "split")
		a := newCustomNode(alice.ID, "average")
		a.Settings, a.Items = []domain.CustomSetting{}, domain.CustomItems{Passed: true, Fields: []domain.CustomField{}}

		require.NoError(t, repo.Create(ctx, b))
		require.NoError(t, repo.Create(ctx, a))

		got, err := repo.Get(ctx, alice.ID, b.ID)
		require.NoError(t, err)

		// Leaf fields come back with empty, not nil, nested fields.
		want := b
		want.Items.Fields[0].Fields = []domain.CustomField{}
		want.Items.Fields[1].Fields[0].Fields = []domain.CustomField{}
		want.Items.Fields[2].Fields = []domain.CustomField{}
		assert.Equal(t, want, got)

		list, err := repo.List(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, a, list[0])
		assert.Equal(t, "split", list[1].Name)

		n, err := repo.CountForOwner(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})

	t.Run("names are unique per owner, not across owners", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCustomNodes(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

		require.NoError(t, repo.Create(ctx, newCustomNode(alice.ID, "split")))
		assert.ErrorIs(t, repo.Create(ctx, newCustomNode(alice.ID, "split")), domain.ErrCustomNodeTaken)
		assert.NoError(t, repo.Create(ctx, newCustomNode(bob.ID, "split")))
	})

	t.Run("update checks the version, and keeps the name", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCustomNodes(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

		n := newCustomNode(alice.ID, "split")
		require.NoError(t, repo.Create(ctx, n))

		edited := n
		edited.Name = "renamed"
		edited.Label, edited.Outputs, edited.Source = "Split", []string{"main"}, "export function run(items) { return items }"
		edited.Settings, edited.Items = []domain.CustomSetting{}, domain.CustomItems{Passed: true, Fields: []domain.CustomField{}}
		edited.Version, edited.UpdatedAt = 2, at.Add(time.Hour)

		require.NoError(t, repo.Update(ctx, edited, 1))
		assert.ErrorIs(t, repo.Update(ctx, edited, 1), domain.ErrConflict, "someone saved first")

		got, err := repo.Get(ctx, alice.ID, n.ID)
		require.NoError(t, err)

		edited.Name = "split"
		assert.Equal(t, edited, got)

		gone := newCustomNode(alice.ID, "gone")
		assert.ErrorIs(t, repo.Update(ctx, gone, 1), domain.ErrNotFound)
	})

	t.Run("someone else's are not found", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCustomNodes(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

		n := newCustomNode(alice.ID, "split")
		require.NoError(t, repo.Create(ctx, n))

		_, err := repo.Get(ctx, bob.ID, n.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.ErrorIs(t, repo.Delete(ctx, bob.ID, n.ID), domain.ErrNotFound)

		mine, err := repo.List(ctx, bob.ID)
		require.NoError(t, err)
		assert.Empty(t, mine)

		require.NoError(t, repo.Delete(ctx, alice.ID, n.ID))
		_, err = repo.Get(ctx, alice.ID, n.ID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
	})
}
