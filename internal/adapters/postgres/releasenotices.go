package postgres

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.ReleaseNotices = (*ReleaseNotices)(nil)

// ReleaseNotices keeps the releases admins were told of (D424).
type ReleaseNotices struct {
	db *DB
}

func NewReleaseNotices(db *DB) *ReleaseNotices {
	return &ReleaseNotices{db: db}
}

func (r *ReleaseNotices) Note(ctx context.Context, version string, at time.Time) (bool, error) {
	t := table.ReleaseNotices

	res, err := t.INSERT(t.AllColumns).
		MODEL(model.ReleaseNotices{Version: version, ToldAt: at}).
		ON_CONFLICT(t.Version).DO_NOTHING().
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return false, err
	}

	n, err := res.RowsAffected()

	return n == 1, err
}
