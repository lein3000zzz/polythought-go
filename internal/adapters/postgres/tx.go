package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.Transactor = (*DB)(nil)

type txKey struct{}

// executor is what jet statements run on: the pool, or the transaction in ctx.
type executor interface {
	qrm.Queryable
	qrm.Executable
}

func (db *DB) conn(ctx context.Context) executor {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}

	return db.SQL
}

// WithinTx runs fn in a transaction. Nested calls join the outer transaction.
func (db *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return fn(ctx)
	}

	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres: begin: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}

		if err != nil {
			_ = tx.Rollback()
			return
		}

		if cerr := tx.Commit(); cerr != nil {
			err = fmt.Errorf("postgres: commit: %w", cerr)
		}
	}()

	return fn(context.WithValue(ctx, txKey{}, tx))
}

const uniqueViolation = "23505"

func isUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && (constraint == "" || pgErr.ConstraintName == constraint)
}

// notFound maps jet's empty-result error to domain.ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.ErrNotFound
	}

	return err
}

// affectedOne turns "no row matched" into domain.ErrNotFound for updates and deletes.
func affectedOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}

	n, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if n == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// parseID reads a domain ID. Malformed IDs cannot exist in the database, so they are
// simply not found; callers pass user input through here.
func parseID[T ~string](id T) (uuid.UUID, error) {
	u, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.UUID{}, domain.ErrNotFound
	}

	return u, nil
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

func timeOf(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}

	return t.UTC()
}
