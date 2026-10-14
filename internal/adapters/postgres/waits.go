package postgres

import (
	"context"
	"encoding/json"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.WaitRepository = (*Waits)(nil)

type Waits struct {
	db *DB
}

func NewWaits(db *DB) *Waits {
	return &Waits{db: db}
}

func (r *Waits) Add(ctx context.Context, w ports.WaitRecord) error {
	eid, err := parseID(w.ExecutionID)
	if err != nil {
		return err
	}

	wid, err := parseID(w.WorkflowID)
	if err != nil {
		return err
	}

	oid, err := parseID(w.OwnerID)
	if err != nil {
		return err
	}

	_, err = table.Waits.INSERT(table.Waits.AllColumns).MODEL(model.Waits{
		ExecutionID: eid, WorkflowID: wid, OwnerID: oid, NodeID: string(w.NodeID),
		Until: w.Until, State: string(w.State), CreatedAt: w.CreatedAt,
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Waits) CountForOwner(ctx context.Context, owner domain.UserID) (int, error) {
	oid, err := parseID(owner)
	if err != nil {
		return 0, err
	}

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(table.Waits).
		WHERE(table.Waits.OwnerID.EQ(jet.UUID(oid))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *Waits) Take(ctx context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	oid, err := parseID(owner)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	eid, err := parseID(id)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	got, err := r.take(ctx, table.Waits.ExecutionID.EQ(jet.UUID(eid)).AND(table.Waits.OwnerID.EQ(jet.UUID(oid))))
	if err != nil {
		return ports.WaitRecord{}, err
	}

	if len(got) == 0 {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return got[0], nil
}

func (r *Waits) TakeDue(ctx context.Context, now time.Time, limit int) ([]ports.WaitRecord, error) {
	w := table.Waits

	// The earliest first, and rows another taker holds are skipped rather than waited for.
	due := jet.SELECT(w.ExecutionID).
		FROM(w).
		WHERE(w.Until.LT_EQ(jet.TimestampzT(now))).
		ORDER_BY(w.Until.ASC()).
		LIMIT(int64(limit)).
		FOR(jet.UPDATE().SKIP_LOCKED())

	return r.take(ctx, w.ExecutionID.IN(due))
}

func (r *Waits) TakeForWorkflow(ctx context.Context, owner domain.UserID, id domain.WorkflowID) ([]ports.WaitRecord, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	wid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	return r.take(ctx, table.Waits.WorkflowID.EQ(jet.UUID(wid)).AND(table.Waits.OwnerID.EQ(jet.UUID(oid))))
}

// take deletes the matching waits and returns them: whoever deletes a row has it.
func (r *Waits) take(ctx context.Context, where jet.BoolExpression) ([]ports.WaitRecord, error) {
	var rows []model.Waits

	err := table.Waits.DELETE().
		WHERE(where).
		RETURNING(table.Waits.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ports.WaitRecord, len(rows))
	for i, m := range rows {
		out[i] = ports.WaitRecord{
			ExecutionID: domain.ExecutionID(m.ExecutionID.String()), WorkflowID: domain.WorkflowID(m.WorkflowID.String()),
			OwnerID: domain.UserID(m.OwnerID.String()), NodeID: domain.NodeID(m.NodeID),
			Until: m.Until.UTC(), State: json.RawMessage(m.State), CreatedAt: m.CreatedAt.UTC(),
		}
	}

	return out, nil
}
