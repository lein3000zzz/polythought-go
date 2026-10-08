// Package postgres implements the core repositories on PostgreSQL. Queries are built with
// go-jet from code generated out of the migrations (see `make db-gen`).
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB owns a pgx pool and a database/sql view of the same pool, which go-jet and goose need.
type DB struct {
	Pool *pgxpool.Pool
	SQL  *sql.DB
}

func Open(ctx context.Context, dsn string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: open pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	return &DB{Pool: pool, SQL: stdlib.OpenDBFromPool(pool)}, nil
}

// Close matches observability.ShutdownFunc so it can be registered for shutdown.
func (db *DB) Close(context.Context) error {
	err := db.SQL.Close()
	db.Pool.Close()

	return err
}

// Migrate applies every pending migration and returns the versions it applied.
func (db *DB) Migrate(ctx context.Context) ([]int64, error) {
	fsys, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db.SQL, fsys)
	if err != nil {
		return nil, fmt.Errorf("postgres: migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}

	applied := make([]int64, len(results))
	for i, r := range results {
		applied[i] = r.Source.Version
	}

	return applied, nil
}
