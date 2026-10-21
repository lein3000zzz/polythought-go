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

// NodeMemory keeps what nodes remember (D121) in node_memory. What plugin nodes store,
// in plugin_store (D288), counts as their memory: Count, Forget and ForgetOthers take it
// in.
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
			ON_CONFLICT(m.WorkflowID, m.NodeID, m.Key, m.Member).
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

func (r *NodeMemory) Admit(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, key string, members []string, limit int, since, now, expires time.Time) ([]string, error) {
	wid, err := parseID(wf)
	if err != nil {
		return nil, err
	}

	if len(members) == 0 {
		return nil, nil
	}

	m := table.NodeMemory

	var admitted []string

	err = r.db.WithinTx(ctx, func(ctx context.Context) error {
		// One count at a time per key, so two runs at once cannot both fill the last place.
		if err := r.lockKey(ctx, wf, node, key); err != nil {
			return err
		}

		held, err := r.held(ctx, wid, node, key, since, now)
		if err != nil {
			return err
		}

		room := limit - held
		if room <= 0 {
			return nil
		}

		admitted = members[:min(room, len(members))]

		rows := make([]model.NodeMemory, len(admitted))
		for i, member := range admitted {
			rows[i] = model.NodeMemory{WorkflowID: wid, NodeID: string(node), Key: key, Member: member, At: now, ExpiresAt: expires}
		}

		if _, err := m.INSERT(m.AllColumns).MODELS(rows).ON_CONFLICT(m.WorkflowID, m.NodeID, m.Key, m.Member).DO_NOTHING().
			ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		return r.trim(ctx, wid, node)
	})
	if err != nil {
		return nil, err
	}

	return admitted, nil
}

func (r *NodeMemory) Gather(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, key, member string, since, now, expires time.Time) (int, error) {
	wid, err := parseID(wf)
	if err != nil {
		return 0, err
	}

	m := table.NodeMemory

	var n int

	err = r.db.WithinTx(ctx, func(ctx context.Context) error {
		// One at a time per key, so two members at once each count the other.
		if err := r.lockKey(ctx, wf, node, key); err != nil {
			return err
		}

		_, err := m.INSERT(m.AllColumns).
			MODEL(model.NodeMemory{WorkflowID: wid, NodeID: string(node), Key: key, Member: member, At: now, ExpiresAt: expires}).
			ON_CONFLICT(m.WorkflowID, m.NodeID, m.Key, m.Member).
			DO_UPDATE(jet.SET(m.At.SET(m.EXCLUDED.At), m.ExpiresAt.SET(m.EXCLUDED.ExpiresAt))).
			ExecContext(ctx, r.db.conn(ctx))
		if err != nil {
			return err
		}

		if n, err = r.held(ctx, wid, node, key, since, now); err != nil {
			return err
		}

		return r.trim(ctx, wid, node)
	})

	return n, err
}

// lockKey holds a node's key until the transaction ends.
func (r *NodeMemory) lockKey(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, key string) error {
	lock := string(wf) + "\x1f" + string(node) + "\x1f" + key // Postgres text takes no NUL
	_, err := r.db.conn(ctx).ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lock)

	return err
}

// held is how many entries a node's key holds, remembered after since and alive at now.
func (r *NodeMemory) held(ctx context.Context, wid uuid.UUID, node domain.NodeID, key string, since, now time.Time) (int, error) {
	m := table.NodeMemory

	var out struct{ Count int64 }

	err := jet.SELECT(jet.COUNT(jet.STAR).AS("count")).FROM(m).
		WHERE(r.ofNode(wid, node).AND(m.Key.EQ(jet.String(key))).
			AND(m.At.GT(jet.TimestampzT(since))).AND(m.ExpiresAt.GT(jet.TimestampzT(now)))).
		QueryContext(ctx, r.db.conn(ctx), &out)

	return int(out.Count), err
}

// trim forgets the oldest entries of a node past the limit (D138): those older than the
// last it keeps, newest first, of the same age the ones after it by key and member.
func (r *NodeMemory) trim(ctx context.Context, wid uuid.UUID, node domain.NodeID) error {
	m := table.NodeMemory

	var last []model.NodeMemory

	err := m.SELECT(m.At, m.Key, m.Member).FROM(m).
		WHERE(r.ofNode(wid, node)).
		ORDER_BY(m.At.DESC(), m.Key.DESC(), m.Member.DESC()).
		OFFSET(ports.MaxRemembered-1).LIMIT(1).
		QueryContext(ctx, r.db.conn(ctx), &last)
	if err != nil || len(last) == 0 {
		return err // within the limit
	}

	at, key, member := jet.TimestampzT(last[0].At), jet.String(last[0].Key), jet.String(last[0].Member)
	older := m.At.LT(at).OR(m.At.EQ(at).AND(m.Key.LT(key).OR(m.Key.EQ(key).AND(m.Member.LT(member)))))

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

	s := table.PluginStore
	stored := jet.SELECT(jet.COUNT(jet.STAR)).FROM(s).
		WHERE(s.WorkflowID.EQ(jet.UUID(wid)).AND(s.NodeID.EQ(jet.String(string(node)))))

	err = jet.SELECT(jet.COUNT(jet.STAR).ADD(jet.IntExp(stored)).AS("count")).
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

	m, s := table.NodeMemory, table.PluginStore

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := m.DELETE().WHERE(r.ofNode(wid, node)).ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		_, err := s.DELETE().WHERE(s.WorkflowID.EQ(jet.UUID(wid)).AND(s.NodeID.EQ(jet.String(string(node))))).
			ExecContext(ctx, r.db.conn(ctx))

		return err
	})
}

func (r *NodeMemory) ForgetOthers(ctx context.Context, wf domain.WorkflowID, keep []domain.NodeID) error {
	wid, err := parseID(wf)
	if err != nil {
		return err
	}

	m, s := table.NodeMemory, table.PluginStore
	memory, stored := m.WorkflowID.EQ(jet.UUID(wid)), s.WorkflowID.EQ(jet.UUID(wid))

	if len(keep) > 0 {
		ids := make([]jet.Expression, len(keep))
		for i, n := range keep {
			ids[i] = jet.String(string(n))
		}

		memory, stored = memory.AND(m.NodeID.NOT_IN(ids...)), stored.AND(s.NodeID.NOT_IN(ids...))
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := m.DELETE().WHERE(memory).ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		_, err := s.DELETE().WHERE(stored).ExecContext(ctx, r.db.conn(ctx))

		return err
	})
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
