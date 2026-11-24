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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Users' datasets: rows counted, looked up by key, the oldest dropped past what a dataset
// keeps, a replace swapped in at once, and swept by age.
func TestDatasets(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	store := postgres.NewDatasets(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	d := domain.Dataset{ID: "0193a1b2-0000-7000-8000-000000000001", OwnerID: alice.ID, Name: "forecasts", KeyField: "city", CreatedAt: at, UpdatedAt: at}
	require.NoError(t, store.Create(ctx, d))
	require.ErrorIs(t, store.Create(ctx, domain.Dataset{ID: "0193a1b2-0000-7000-8000-000000000002", OwnerID: alice.ID, Name: "forecasts", CreatedAt: at}),
		domain.ErrDatasetExists)
	require.NoError(t, store.Create(ctx, domain.Dataset{ID: "0193a1b2-0000-7000-8000-000000000003", OwnerID: bob.ID, Name: "forecasts", CreatedAt: at}),
		"another user's may share the name")

	row := func(key, json string, when time.Time) ports.NewRow {
		return ports.NewRow{Key: key, At: when, Row: []byte(json)}
	}

	require.NoError(t, store.Append(ctx, d.ID, []ports.NewRow{
		row("NYC", `{"city":"NYC","high":50}`, at), row("LDN", `{"city":"LDN","high":9}`, at), row("NYC", `{"city":"NYC","high":52}`, at.Add(time.Hour)),
	}, 0, at.Add(time.Hour)))

	got, err := store.Get(ctx, alice.ID, "forecasts")
	require.NoError(t, err)
	assert.Equal(t, 3, got.Rows)
	assert.Equal(t, int64(24+23+24), got.Bytes)
	assert.True(t, got.UpdatedAt.Equal(at.Add(time.Hour)))

	latest, err := store.Latest(ctx, d.ID, "NYC")
	require.NoError(t, err)
	assert.JSONEq(t, `{"city":"NYC","high":52}`, string(latest.Row), "the last appended")

	_, err = store.Latest(ctx, d.ID, "Mars")
	require.ErrorIs(t, err, domain.ErrNotFound)

	page, err := store.Rows(ctx, d.ID, 0, 2)
	require.NoError(t, err)
	require.Len(t, page, 2)

	rest, err := store.Rows(ctx, d.ID, page[1].Seq, 10)
	require.NoError(t, err)
	require.Len(t, rest, 1, "after the page")

	newest, err := store.Newest(ctx, d.ID, 2)
	require.NoError(t, err)
	require.Len(t, newest, 2)
	assert.Equal(t, []string{"LDN", "NYC"}, []string{newest[0].Key, newest[1].Key}, "the last two, oldest first")
	assert.JSONEq(t, `{"city":"NYC","high":52}`, string(newest[1].Row))

	// Keeping two: the oldest goes.
	require.NoError(t, store.Append(ctx, d.ID, []ports.NewRow{row("PAR", `{"city":"PAR"}`, at.Add(2*time.Hour))}, 2, at.Add(2*time.Hour)))

	all, err := store.Rows(ctx, d.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "NYC", all[0].Key)
	assert.Equal(t, "PAR", all[1].Key)

	// A replace: staged rows wait, then take the old ones' place at once.
	stage, err := store.Stage(ctx, d.ID, at)
	require.NoError(t, err)
	require.NoError(t, store.StageRows(ctx, d.ID, stage, []ports.NewRow{row("TKY", `{"city":"TKY"}`, at)}))

	n, bytes, err := store.StageSize(ctx, d.ID, stage)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, int64(14), bytes)

	got, err = store.Get(ctx, alice.ID, "forecasts")
	require.NoError(t, err)
	assert.Equal(t, 2, got.Rows, "staged rows are not the dataset's yet")

	newest, err = store.Newest(ctx, d.ID, 10)
	require.NoError(t, err)
	assert.Len(t, newest, 2, "nor read")

	used, err := store.Bytes(ctx, alice.ID)
	require.NoError(t, err)
	assert.Equal(t, got.Bytes+14, used, "but count toward the user's storage")

	require.NoError(t, store.Swap(ctx, d.ID, stage, at.Add(3*time.Hour)))

	all, err = store.Rows(ctx, d.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "TKY", all[0].Key)

	require.ErrorIs(t, store.Swap(ctx, d.ID, stage, at), domain.ErrNotFound, "swapped already")
	require.ErrorIs(t, store.StageRows(ctx, "0193a1b2-0000-7000-8000-000000000003", stage, nil), domain.ErrNotFound, "another dataset's stage")

	// Kept a day: the old row goes, and a stage left a day.
	require.NoError(t, store.SetKeep(ctx, d.ID, 1, 0, at))
	require.NoError(t, store.Append(ctx, d.ID, []ports.NewRow{row("NEW", `{"city":"NEW"}`, at.Add(72*time.Hour))}, 0, at))

	left, err := store.Stage(ctx, d.ID, at)
	require.NoError(t, err)

	dropped, err := store.Sweep(ctx, at.Add(72*time.Hour), at.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(1), dropped)

	all, err = store.Rows(ctx, d.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "NEW", all[0].Key)
	require.ErrorIs(t, store.Unstage(ctx, d.ID, left), domain.ErrNotFound, "the stale stage swept")

	byOwner, err := store.BytesByOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(14), byOwner[alice.ID])
	assert.Equal(t, int64(0), byOwner[bob.ID])

	list, err := store.List(ctx, alice.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, store.Delete(ctx, d.ID))

	_, err = store.Get(ctx, alice.ID, "forecasts")
	require.ErrorIs(t, err, domain.ErrNotFound)
}
