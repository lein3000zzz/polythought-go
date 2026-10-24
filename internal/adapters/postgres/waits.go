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

	var question *string

	if w.Question != nil {
		data, err := json.Marshal(w.Question)
		if err != nil {
			return err
		}

		question = new(string(data))
	}

	var task *string

	if w.Task != nil {
		data, err := json.Marshal(w.Task)
		if err != nil {
			return err
		}

		task = new(string(data))
	}

	_, err = table.Waits.INSERT(table.Waits.AllColumns).MODEL(model.Waits{
		ExecutionID: eid, WorkflowID: wid, OwnerID: oid, NodeID: string(w.NodeID),
		Until: w.Until, Key: w.Key, Question: question, Task: task, State: string(w.State), CreatedAt: w.CreatedAt,
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Waits) Get(ctx context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	oid, err := parseID(owner)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	eid, err := parseID(id)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	var m model.Waits

	err = table.Waits.SELECT(table.Waits.AllColumns).
		WHERE(table.Waits.ExecutionID.EQ(jet.UUID(eid)).AND(table.Waits.OwnerID.EQ(jet.UUID(oid)))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return ports.WaitRecord{}, notFound(err)
	}

	return waitOf(m)
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
	return r.takeOne(ctx, owner, id, nil)
}

func (r *Waits) TakeWithKey(ctx context.Context, owner domain.UserID, id domain.ExecutionID, key string) (ports.WaitRecord, error) {
	if key == "" {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return r.takeOne(ctx, owner, id, table.Waits.Key.EQ(jet.String(key)))
}

func (r *Waits) takeOne(ctx context.Context, owner domain.UserID, id domain.ExecutionID, also jet.BoolExpression) (ports.WaitRecord, error) {
	oid, err := parseID(owner)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	eid, err := parseID(id)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	where := table.Waits.ExecutionID.EQ(jet.UUID(eid)).AND(table.Waits.OwnerID.EQ(jet.UUID(oid)))
	if also != nil {
		where = where.AND(also)
	}

	got, err := r.take(ctx, where)
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

func (r *Waits) LeaseTask(ctx context.Context, owner domain.UserID, holder string, now, until time.Time) (ports.WaitRecord, error) {
	oid, err := parseID(owner)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	w := table.Waits

	// The oldest first, and one another taker is handing out is skipped rather than waited
	// for, as it is about to be held.
	free := jet.SELECT(w.ExecutionID).
		FROM(w).
		WHERE(w.OwnerID.EQ(jet.UUID(oid)).
			AND(w.Task.IS_NOT_NULL()).
			AND(w.LeaseHolder.IS_NULL().OR(w.LeaseUntil.LT_EQ(jet.TimestampzT(now))))).
		ORDER_BY(w.CreatedAt.ASC()).
		LIMIT(1).
		FOR(jet.UPDATE().SKIP_LOCKED())

	var rows []model.Waits

	err = w.UPDATE(w.LeaseHolder, w.LeaseUntil).
		SET(jet.String(holder), jet.TimestampzT(until)).
		WHERE(w.ExecutionID.IN(free)).
		RETURNING(w.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil {
		return ports.WaitRecord{}, err
	}

	if len(rows) == 0 {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return waitOf(rows[0])
}

func (r *Waits) RenewLeases(ctx context.Context, owner domain.UserID, holder string, until time.Time) error {
	oid, err := parseID(owner)
	if err != nil {
		return err
	}

	w := table.Waits

	_, err = w.UPDATE(w.LeaseUntil).
		SET(jet.TimestampzT(until)).
		WHERE(w.OwnerID.EQ(jet.UUID(oid)).AND(w.LeaseHolder.EQ(jet.String(holder)))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Waits) TakeTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID, holder string) (ports.WaitRecord, error) {
	if holder == "" {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return r.takeOne(ctx, owner, id, table.Waits.Task.IS_NOT_NULL().AND(table.Waits.LeaseHolder.EQ(jet.String(holder))))
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
		if out[i], err = waitOf(m); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func waitOf(m model.Waits) (ports.WaitRecord, error) {
	w := ports.WaitRecord{
		ExecutionID: domain.ExecutionID(m.ExecutionID.String()), WorkflowID: domain.WorkflowID(m.WorkflowID.String()),
		OwnerID: domain.UserID(m.OwnerID.String()), NodeID: domain.NodeID(m.NodeID),
		Until: m.Until.UTC(), Key: m.Key, State: json.RawMessage(m.State), CreatedAt: m.CreatedAt.UTC(),
	}

	if m.Question != nil {
		w.Question = &ports.WaitQuestion{}
		if err := json.Unmarshal([]byte(*m.Question), w.Question); err != nil {
			return ports.WaitRecord{}, err
		}
	}

	if m.Task != nil {
		w.Task = &ports.WaitTask{}
		if err := json.Unmarshal([]byte(*m.Task), w.Task); err != nil {
			return ports.WaitRecord{}, err
		}
	}

	if m.LeaseHolder != nil && m.LeaseUntil != nil {
		w.Lease = &ports.TaskLease{Holder: *m.LeaseHolder, Until: m.LeaseUntil.UTC()}
	}

	return w, nil
}
