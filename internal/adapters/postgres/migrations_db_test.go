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

// TestLiveTradingMigration checks that holdings from before live trading get their owner
// from their workflow, as those of workflows deleted later need it (D199).
func TestLiveTradingMigration(t *testing.T) {
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

	_, err = provider.UpTo(ctx, 23)
	require.NoError(t, err)

	const (
		user     = "0192f0c4-0000-7000-8000-000000000001"
		workflow = "0192f0c4-0000-7000-8000-000000000002"
	)

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO users (id, email, email_normalized, password_hash, role, created_at, updated_at)
		VALUES ($1, 'old@example.com', 'old@example.com', 'x', 'user', $2, $2)`, user, now)
	require.NoError(t, err)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO workflows (id, owner_id, name, active, definition, version, created_at, updated_at)
		VALUES ($1, $2, 'copy', false, '{}', 1, $3, $3)`, workflow, user, now)
	require.NoError(t, err)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO holdings (workflow_id, mode, token_id, shares, cost, realized, updated_at)
		VALUES ($1, 'paper', 't-yes', 10, 5.5, 0, $2)`, workflow, now)
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, 24)
	require.NoError(t, err)

	var owner string
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT owner_id FROM holdings WHERE workflow_id = $1`, workflow).Scan(&owner))
	assert.Equal(t, user, owner)

	_, err = provider.DownTo(ctx, 23)
	require.NoError(t, err, "and back")
}

// TestInviteUsesMigration checks that an invite used before invites admitted more than
// one sign-up keeps its sign-up, and is used up (D371); and that going back keeps it.
func TestInviteUsesMigration(t *testing.T) {
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

	_, err = provider.UpTo(ctx, 36)
	require.NoError(t, err)

	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO users (id, email, email_normalized, password_hash, role, created_at, updated_at, email_verified_at)
		VALUES ('0192f0c4-0000-7000-8000-000000000001', 'joiner@example.com', 'joiner@example.com', 'x', 'user', $1, $1, $1)`, at)
	require.NoError(t, err)
	_, err = db.SQL.ExecContext(ctx, `INSERT INTO invites (id, code_hash, created_at, expires_at, used_by, used_at) VALUES
		('0192f0c4-0000-7000-8000-0000000000a1', 'used', $1, $2, '0192f0c4-0000-7000-8000-000000000001', $1),
		('0192f0c4-0000-7000-8000-0000000000a2', 'open', $1, $2, NULL, NULL)`, at, at.Add(time.Hour))
	require.NoError(t, err)

	_, err = provider.UpTo(ctx, 37)
	require.NoError(t, err)

	uses := map[string]int{}
	rows, err := db.SQL.QueryContext(ctx, `SELECT code_hash, uses, max_uses FROM invites`)
	require.NoError(t, err)

	for rows.Next() {
		var (
			code      string
			used, max int
		)

		require.NoError(t, rows.Scan(&code, &used, &max))
		assert.Equal(t, 1, max)
		uses[code] = used
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, map[string]int{"used": 1, "open": 0}, uses)

	var who string
	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT user_id FROM invite_uses WHERE invite_id = '0192f0c4-0000-7000-8000-0000000000a1'`).Scan(&who))
	assert.Equal(t, "0192f0c4-0000-7000-8000-000000000001", who)

	_, err = provider.DownTo(ctx, 36)
	require.NoError(t, err)

	require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT used_by FROM invites WHERE code_hash = 'used'`).Scan(&who))
	assert.Equal(t, "0192f0c4-0000-7000-8000-000000000001", who)
}
