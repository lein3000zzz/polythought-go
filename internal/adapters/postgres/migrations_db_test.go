//go:build db

package postgres

import (
	"context"
	"io/fs"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/pgtest"
)

// TestEmailVerificationMigration checks that accounts from before email verification
// count as verified (D167), so turning it on locks none of them out.
func TestEmailVerificationMigration(t *testing.T) {
	ctx := context.Background()

	pg, err := pgtest.Start(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	db, err := Open(ctx, pg.DSN)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close(ctx) })

	fsys, err := fs.Sub(migrationFiles, "migrations")
	require.NoError(t, err)

	provider, err := goose.NewProvider(goose.DialectPostgres, db.SQL, fsys)
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, 21)
	require.NoError(t, err)

	created := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO users (id, email, email_normalized, password_hash, role, created_at, updated_at)
		VALUES ('0192f0c4-0000-7000-8000-000000000001', 'old@example.com', 'old@example.com', 'x', 'user', $1, $1)`, created)
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, 22)
	require.NoError(t, err)

	var verified time.Time
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT email_verified_at FROM users WHERE email = 'old@example.com'`).Scan(&verified))
	assert.True(t, verified.Equal(created), "verified as of when it was made")
}
