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

var _ ports.PluginStore = (*PluginStore)(nil)

// PluginStore keeps what plugin nodes store (D288) in plugin_store, which NodeMemory
// counts and forgets with node memory.
type PluginStore struct {
	db *DB
}

func NewPluginStore(db *DB) *PluginStore {
	return &PluginStore{db: db}
}

func (r *PluginStore) Get(ctx context.Context, n ports.PluginNode, key string) (json.RawMessage, bool, error) {
	wid, err := parseID(n.Workflow)
	if err != nil {
		return nil, false, err
	}

	s := table.PluginStore

	var row model.PluginStore

	err = s.SELECT(s.Value).FROM(s).WHERE(r.ofNode(wid, n).AND(s.Key.EQ(jet.String(key)))).
		QueryContext(ctx, r.db.conn(ctx), &row)

	switch {
	case errors.Is(err, qrm.ErrNoRows):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}

	return row.Value, true, nil
}

func (r *PluginStore) Set(ctx context.Context, n ports.PluginNode, key string, value json.RawMessage, max int, now time.Time) error {
	wid, err := parseID(n.Workflow)
	if err != nil {
		return err
	}

	size := len(key) + len(value)
	if size > max {
		return domain.ErrStoreFull
	}

	s := table.PluginStore

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		// One write at a time per node, so two at once cannot both take its last bytes.
		lock := string(n.Workflow) + "\x1f" + string(n.Node) + "\x1f" + n.Plugin
		if _, err := r.db.conn(ctx).ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lock); err != nil {
			return err
		}

		var used struct{ Bytes int64 }

		err := jet.SELECT(jet.COALESCE(jet.SUMi(s.Size), jet.Int(0)).AS("bytes")).FROM(s).
			WHERE(r.ofNode(wid, n).AND(s.Key.NOT_EQ(jet.String(key)))).
			QueryContext(ctx, r.db.conn(ctx), &used)
		if err != nil {
			return err
		}

		if int(used.Bytes)+size > max {
			return domain.ErrStoreFull
		}

		row := model.PluginStore{
			WorkflowID: wid, NodeID: string(n.Node), PluginID: n.Plugin, Key: key, Value: value,
			Size: int32(size), UpdatedAt: now, //nolint:gosec // within max, an int
		}

		_, err = s.INSERT(s.AllColumns).MODEL(row).
			ON_CONFLICT(s.WorkflowID, s.NodeID, s.PluginID, s.Key).
			DO_UPDATE(jet.SET(s.Value.SET(s.EXCLUDED.Value), s.Size.SET(s.EXCLUDED.Size), s.UpdatedAt.SET(s.EXCLUDED.UpdatedAt))).
			ExecContext(ctx, r.db.conn(ctx))

		return err
	})
}

func (r *PluginStore) Delete(ctx context.Context, n ports.PluginNode, key string) error {
	wid, err := parseID(n.Workflow)
	if err != nil {
		return err
	}

	s := table.PluginStore
	_, err = s.DELETE().WHERE(r.ofNode(wid, n).AND(s.Key.EQ(jet.String(key)))).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *PluginStore) Keys(ctx context.Context, n ports.PluginNode, prefix, after string, limit int) ([]string, error) {
	wid, err := parseID(n.Workflow)
	if err != nil {
		return nil, err
	}

	s := table.PluginStore
	where := r.ofNode(wid, n).AND(s.Key.GT(jet.String(after)))

	if prefix != "" {
		where = where.AND(jet.BoolExp(jet.Func("starts_with", s.Key, jet.String(prefix))))
	}

	var rows []model.PluginStore

	err = s.SELECT(s.Key).FROM(s).WHERE(where).ORDER_BY(s.Key.ASC()).LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	keys := make([]string, len(rows))
	for i, row := range rows {
		keys[i] = row.Key
	}

	return keys, nil
}

func (r *PluginStore) ofNode(wid uuid.UUID, n ports.PluginNode) jet.BoolExpression {
	s := table.PluginStore

	return s.WorkflowID.EQ(jet.UUID(wid)).AND(s.NodeID.EQ(jet.String(string(n.Node)))).AND(s.PluginID.EQ(jet.String(n.Plugin)))
}
