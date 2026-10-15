//go:build db

// Tests against a real Postgres in Docker. Run with `make test-db`.

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/pgtest"
)

func TestMigrate(t *testing.T) {
	ctx := context.Background()

	pg, err := pgtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	db, err := postgres.Open(ctx, pg.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(ctx) })

	tests := []struct {
		name        string
		wantApplied []int64
	}{
		{name: "fresh database gets every migration", wantApplied: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{name: "second run is a no-op", wantApplied: []int64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			applied, err := db.Migrate(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.wantApplied, applied)
		})
	}

	t.Run("schema is in place", func(t *testing.T) {
		var tables []string

		rows, err := db.Pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename`)
		require.NoError(t, err)

		for rows.Next() {
			var name string
			require.NoError(t, rows.Scan(&name))
			tables = append(tables, name)
		}

		require.NoError(t, rows.Err())
		assert.Equal(t, []string{
			"api_tokens", "credentials", "executions", "goose_db_version", "invites", "notification_prefs", "polymarket_credentials", "sessions", "settings", "settings_audit", "telegram_link_codes", "telegram_links", "templates", "users", "waits", "wallets", "workflows",
		}, tables)
	})

	t.Run("open rejects a bad dsn", func(t *testing.T) {
		_, err := postgres.Open(ctx, "postgres://nobody@127.0.0.1:1/none?connect_timeout=1")
		assert.Error(t, err)
	})
}
