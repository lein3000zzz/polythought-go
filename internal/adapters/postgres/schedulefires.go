package postgres

import (
	"context"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.ScheduleFires = (*ScheduleFires)(nil)

// ScheduleFires keeps when each schedule that makes up missed runs last fired (D440).
type ScheduleFires struct {
	db *DB
}

func NewScheduleFires(db *DB) *ScheduleFires {
	return &ScheduleFires{db: db}
}

func (r *ScheduleFires) LastFire(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID) (time.Time, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return time.Time{}, nil
	}

	t := table.ScheduleFires

	var row model.ScheduleFires

	err = t.SELECT(t.Due).
		WHERE(t.WorkflowID.EQ(jet.UUID(wid)).AND(t.NodeID.EQ(jet.String(string(node))))).
		QueryContext(ctx, r.db.conn(ctx), &row)
	if errors.Is(err, qrm.ErrNoRows) {
		return time.Time{}, nil
	}

	if err != nil {
		return time.Time{}, err
	}

	return row.Due.UTC(), nil
}

func (r *ScheduleFires) Fired(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID, due time.Time) error {
	wid, err := parseID(workflow)
	if err != nil {
		return domain.ErrNotFound
	}

	t := table.ScheduleFires

	_, err = t.INSERT(t.AllColumns).
		MODEL(model.ScheduleFires{WorkflowID: wid, NodeID: string(node), Due: due.UTC()}).
		ON_CONFLICT(t.WorkflowID, t.NodeID).
		DO_UPDATE(jet.SET(t.Due.SET(t.EXCLUDED.Due))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}
