//go:build db

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
)

func TestReleaseNotices(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)

	_, err := db.Pool.Exec(ctx, `TRUNCATE release_notices`)
	require.NoError(t, err)

	notices := postgres.NewReleaseNotices(db)

	first, err := notices.Note(ctx, "0.0.2", at)
	require.NoError(t, err)
	assert.True(t, first, "not told before")

	first, err = notices.Note(ctx, "0.0.2", at.Add(24*time.Hour))
	require.NoError(t, err)
	assert.False(t, first, "told once")

	first, err = notices.Note(ctx, "0.0.3", at.Add(24*time.Hour))
	require.NoError(t, err)
	assert.True(t, first, "each release its own")

	var told time.Time
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT told_at FROM release_notices WHERE version = '0.0.2'`).Scan(&told))
	assert.True(t, told.Equal(at), "when it was first told")
}
