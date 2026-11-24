package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

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

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		_, err := table.Waits.INSERT(table.Waits.AllColumns).MODEL(model.Waits{
			ExecutionID: eid, WorkflowID: wid, OwnerID: oid, NodeID: string(w.NodeID),
			Until: w.Until, Key: w.Key, Question: question, Task: task, State: string(w.State), CreatedAt: w.CreatedAt,
		}).ExecContext(ctx, r.db.conn(ctx))
		if err != nil || w.Task == nil {
			return err
		}

		parts := partsOf(*w.Task)
		rows := make([]model.TaskParts, len(parts))

		for i, p := range parts {
			rows[i] = model.TaskParts{ExecutionID: eid, Part: int32(p.Index), FirstItem: int32(p.First), Items: int32(p.Items)}
		}

		t := table.TaskParts
		_, err = t.INSERT(t.ExecutionID, t.Part, t.FirstItem, t.Items).MODELS(rows).ExecContext(ctx, r.db.conn(ctx))

		return err
	})
}

// partsOf are a task's parts as its splits have them (D436): one for a task not split.
func partsOf(t ports.WaitTask) []ports.TaskPart {
	starts := append([]int{0}, t.Splits...)
	parts := make([]ports.TaskPart, len(starts))

	for i, first := range starts {
		end := len(t.Items)
		if i+1 < len(starts) {
			end = starts[i+1]
		}

		parts[i] = ports.TaskPart{Index: i, First: first, Items: end - first}
	}

	return parts
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

	w, err := waitOf(m)
	if err != nil || w.Task == nil {
		return w, err
	}

	w.Parts, err = r.parts(ctx, eid)

	return w, err
}

// parts are a task's parts, in order.
func (r *Waits) parts(ctx context.Context, eid uuid.UUID) ([]ports.TaskPart, error) {
	t := table.TaskParts

	var rows []model.TaskParts

	err := t.SELECT(t.AllColumns).WHERE(t.ExecutionID.EQ(jet.UUID(eid))).ORDER_BY(t.Part.ASC()).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]ports.TaskPart, len(rows))
	for i, m := range rows {
		if out[i], err = partOf(m); err != nil {
			return nil, err
		}
	}

	return out, nil
}

func partOf(m model.TaskParts) (ports.TaskPart, error) {
	p := ports.TaskPart{Index: int(m.Part), First: int(m.FirstItem), Items: int(m.Items), AnsweredBy: m.AnsweredBy}

	if m.LeaseHolder != nil && m.LeaseUntil != nil {
		p.Lease = &ports.TaskLease{Holder: *m.LeaseHolder, Until: m.LeaseUntil.UTC()}
		if m.LeaseName != nil {
			p.Lease.Name = *m.LeaseName
		}
	}

	if m.Answers != nil {
		if err := json.Unmarshal([]byte(*m.Answers), &p.Answers); err != nil {
			return ports.TaskPart{}, err
		}

		if p.Answers == nil {
			p.Answers = []any{} // answered, if with nothing
		}
	}

	return p, nil
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

func (r *Waits) LeaseTask(ctx context.Context, owner domain.UserID, lease ports.TaskLease, now time.Time) (ports.WaitRecord, int, error) {
	oid, err := parseID(owner)
	if err != nil {
		return ports.WaitRecord{}, 0, err
	}

	var (
		got  ports.WaitRecord
		part int
	)

	err = r.db.WithinTx(ctx, func(ctx context.Context) error {
		p, w := table.TaskParts, table.Waits

		// The oldest task's first part free, and one another taker is handing out is
		// skipped rather than waited for, as it is about to be held.
		var free []model.TaskParts

		err := jet.SELECT(p.ExecutionID, p.Part).
			FROM(p.INNER_JOIN(w, w.ExecutionID.EQ(p.ExecutionID))).
			WHERE(w.OwnerID.EQ(jet.UUID(oid)).
				AND(p.Answers.IS_NULL()).
				AND(p.LeaseHolder.IS_NULL().OR(p.LeaseUntil.LT_EQ(jet.TimestampzT(now))))).
			ORDER_BY(w.CreatedAt.ASC(), p.Part.ASC()).
			LIMIT(1).
			FOR(jet.UPDATE().OF(p).SKIP_LOCKED()).
			QueryContext(ctx, r.db.conn(ctx), &free)
		if err != nil && !errors.Is(err, qrm.ErrNoRows) {
			return err
		}

		if len(free) == 0 {
			return domain.ErrNotFound
		}

		eid := free[0].ExecutionID
		part = int(free[0].Part)

		_, err = p.UPDATE(p.LeaseHolder, p.LeaseName, p.LeaseUntil).
			SET(jet.String(lease.Holder), jet.String(lease.Name), jet.TimestampzT(lease.Until)).
			WHERE(p.ExecutionID.EQ(jet.UUID(eid)).AND(p.Part.EQ(jet.Int32(int32(part))))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		got, err = r.Get(ctx, owner, domain.ExecutionID(eid.String()))

		return err
	})
	if err != nil {
		return ports.WaitRecord{}, 0, err
	}

	return got, part, nil
}

func (r *Waits) RenewLeases(ctx context.Context, owner domain.UserID, holder string, until time.Time) error {
	oid, err := parseID(owner)
	if err != nil {
		return err
	}

	p, w := table.TaskParts, table.Waits

	_, err = p.UPDATE(p.LeaseUntil).
		SET(jet.TimestampzT(until)).
		WHERE(p.LeaseHolder.EQ(jet.String(holder)).AND(p.Answers.IS_NULL()).
			AND(p.ExecutionID.IN(jet.SELECT(w.ExecutionID).FROM(w).WHERE(w.OwnerID.EQ(jet.UUID(oid)))))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Waits) ReleaseTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID) error {
	rec, err := r.Get(ctx, owner, id)
	if err != nil {
		return err
	}

	if rec.Task == nil {
		return domain.ErrNotFound
	}

	eid, err := parseID(id)
	if err != nil {
		return err
	}

	p := table.TaskParts

	_, err = p.UPDATE(p.LeaseHolder, p.LeaseName, p.LeaseUntil).
		SET(jet.NULL, jet.NULL, jet.NULL).
		WHERE(p.ExecutionID.EQ(jet.UUID(eid)).AND(p.Answers.IS_NULL())).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *Waits) AnswerPart(ctx context.Context, owner domain.UserID, id domain.ExecutionID, part int, holder string, answers []any, by string) ([]ports.TaskPart, error) {
	if holder == "" {
		return nil, domain.ErrNotFound
	}

	oid, err := parseID(owner)
	if err != nil {
		return nil, err
	}

	eid, err := parseID(id)
	if err != nil {
		return nil, err
	}

	if answers == nil {
		answers = []any{}
	}

	data, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}

	var parts []ports.TaskPart

	err = r.db.WithinTx(ctx, func(ctx context.Context) error {
		w, p := table.Waits, table.TaskParts

		// The wait first, so that the parts answered at once see each other's answers.
		var locked []model.Waits

		err := jet.SELECT(w.ExecutionID).FROM(w).
			WHERE(w.ExecutionID.EQ(jet.UUID(eid)).AND(w.OwnerID.EQ(jet.UUID(oid))).AND(w.Task.IS_NOT_NULL())).
			FOR(jet.UPDATE()).
			QueryContext(ctx, r.db.conn(ctx), &locked)
		if err != nil && !errors.Is(err, qrm.ErrNoRows) {
			return err
		}

		if len(locked) == 0 {
			return domain.ErrNotFound
		}

		err = affectedOne(p.UPDATE(p.Answers, p.AnsweredBy).
			SET(jet.Json(string(data)), jet.String(by)).
			WHERE(p.ExecutionID.EQ(jet.UUID(eid)).AND(p.Part.EQ(jet.Int32(int32(part)))).
				AND(p.LeaseHolder.EQ(jet.String(holder))).AND(p.Answers.IS_NULL())).
			ExecContext(ctx, r.db.conn(ctx)))
		if err != nil {
			return err
		}

		parts, err = r.parts(ctx, eid)

		return err
	})

	return parts, err
}

func (r *Waits) TakeTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID, holder string) (ports.WaitRecord, error) {
	if holder == "" {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	p := table.TaskParts

	return r.takeOne(ctx, owner, id, table.Waits.Task.IS_NOT_NULL().AND(jet.EXISTS(
		jet.SELECT(p.Part).FROM(p).WHERE(p.ExecutionID.EQ(table.Waits.ExecutionID).AND(p.LeaseHolder.EQ(jet.String(holder)))),
	)))
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

	return w, nil
}
