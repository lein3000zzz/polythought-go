package postgres

import (
	"context"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.NodeMemory = (*NodeMemory)(nil)

// NodeMemory keeps what nodes remember (D121) in node_memory.
type NodeMemory struct {
	db *DB
}

func NewNodeMemory(db *DB) *NodeMemory {
	return &NodeMemory{db: db}
}

func (r *NodeMemory) Remember(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, keys []string, now, expires time.Time) ([]string, error) {
	wid, err := parseID(wf)
	if err != nil {
		return nil, err
	}

	var (
		unique []string
		rows   []model.NodeMemory
		seen   = make(map[string]bool, len(keys))
	)

	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
			rows = append(rows, model.NodeMemory{WorkflowID: wid, NodeID: string(node), Key: k, At: now, ExpiresAt: expires})
		}
	}

	if len(rows) == 0 {
		return nil, nil
	}

	m := table.NodeMemory

	var fresh []string

	err = r.db.WithinTx(ctx, func(ctx context.Context) error {
		// New keys go in; one held but expired is remembered again from now. Either way
		// it comes back: the rest were held already.
		var got []model.NodeMemory

		err := m.INSERT(m.AllColumns).MODELS(rows).
			ON_CONFLICT(m.WorkflowID, m.NodeID, m.Key).
			DO_UPDATE(jet.SET(m.At.SET(m.EXCLUDED.At), m.ExpiresAt.SET(m.EXCLUDED.ExpiresAt)).
				WHERE(m.ExpiresAt.LT_EQ(m.EXCLUDED.At))).
			RETURNING(m.Key).
			QueryContext(ctx, r.db.conn(ctx), &got)
		if err != nil {
			return err
		}

		if len(got) == 0 {
			return nil
		}

		returned := make(map[string]bool, len(got))
		for _, g := range got {
			returned[g.Key] = true
		}

		for _, k := range unique {
			if returned[k] {
				fresh = append(fresh, k)
			}
		}

		return r.trim(ctx, wid, node)
	})

	return fresh, err
}

// trim forgets the oldest keys of a node past the limit (D138): those older than the
// last it keeps, newest first, of the same age the ones after it by key.
func (r *NodeMemory) trim(ctx context.Context, wid uuid.UUID, node domain.NodeID) error {
	m := table.NodeMemory

	var last []model.NodeMemory

	err := m.SELECT(m.At, m.Key).FROM(m).
		WHERE(r.ofNode(wid, node)).
		ORDER_BY(m.At.DESC(), m.Key.DESC()).
		OFFSET(ports.MaxRemembered-1).LIMIT(1).
		QueryContext(ctx, r.db.conn(ctx), &last)
	if err != nil || len(last) == 0 {
		return err // within the limit
	}

	at, key := jet.TimestampzT(last[0].At), jet.String(last[0].Key)
	older := m.At.LT(at).OR(m.At.EQ(at).AND(m.Key.LT(key)))

	_, err = m.DELETE().WHERE(r.ofNode(wid, node).AND(older)).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *NodeMemory) Count(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, now time.Time) (int, error) {
	wid, err := parseID(wf)
	if err != nil {
		return 0, err
	}

	m := table.NodeMemory

	var out struct{ Count int64 }

	err = jet.SELECT(jet.COUNT(jet.STAR).AS("count")).
		FROM(m).
		WHERE(r.ofNode(wid, node).AND(m.ExpiresAt.GT(jet.TimestampzT(now)))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

func (r *NodeMemory) Forget(ctx context.Context, wf domain.WorkflowID, node domain.NodeID) error {
	wid, err := parseID(wf)
	if err != nil {
		return err
	}

	_, err = table.NodeMemory.DELETE().WHERE(r.ofNode(wid, node)).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *NodeMemory) ForgetOthers(ctx context.Context, wf domain.WorkflowID, keep []domain.NodeID) error {
	wid, err := parseID(wf)
	if err != nil {
		return err
	}

	m := table.NodeMemory
	where := m.WorkflowID.EQ(jet.UUID(wid))

	if len(keep) > 0 {
		ids := make([]jet.Expression, len(keep))
		for i, n := range keep {
			ids[i] = jet.String(string(n))
		}

		where = where.AND(m.NodeID.NOT_IN(ids...))
	}

	_, err = m.DELETE().WHERE(where).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *NodeMemory) Sweep(ctx context.Context, now time.Time) (int, error) {
	m := table.NodeMemory

	res, err := m.DELETE().WHERE(m.ExpiresAt.LT_EQ(jet.TimestampzT(now))).ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	n, err := res.RowsAffected()

	return int(n), err
}

func (r *NodeMemory) ofNode(wid uuid.UUID, node domain.NodeID) jet.BoolExpression {
	m := table.NodeMemory
	return m.WorkflowID.EQ(jet.UUID(wid)).AND(m.NodeID.EQ(jet.String(string(node))))
}
