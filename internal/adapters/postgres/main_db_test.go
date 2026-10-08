//go:build db

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/pgtest"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// testDB is one migrated database shared by the package; tests reset it with fresh(t).
var testDB *postgres.DB

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()

	pg, err := pgtest.Start(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = pg.Terminate(ctx) }()

	testDB, err = postgres.Open(ctx, pg.DSN)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = testDB.Close(ctx) }()

	if _, err := testDB.Migrate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return m.Run()
}

// fresh empties every table and returns the shared database.
func fresh(t *testing.T) *postgres.DB {
	t.Helper()

	_, err := testDB.Pool.Exec(context.Background(), `TRUNCATE users, sessions, api_tokens, invites, wallets, polymarket_credentials CASCADE`)
	require.NoError(t, err)

	return testDB
}

// at is a fixed instant at the database's microsecond precision.
var at = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

func seedUser(t *testing.T, db *postgres.DB, email string, role domain.Role) domain.User {
	t.Helper()

	u := domain.User{ID: domain.UserID(newID()), Email: email, Role: role, CreatedAt: at, UpdatedAt: at}
	require.NoError(t, postgres.NewUsers(db).Create(context.Background(), u, "$hash-"+email))

	return u
}
