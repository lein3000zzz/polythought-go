package postgres

import (
	"context"
	"errors"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.HookRepository = (*Hooks)(nil)

// Hooks keeps the Inbound webhook triggers' secret addresses (D439).
type Hooks struct {
	db *DB
}

func NewHooks(db *DB) *Hooks {
	return &Hooks{db: db}
}

type hookRow struct {
	model.WorkflowHooks

	OwnerID uuid.UUID `alias:"workflows.owner_id"`
}

func (r *Hooks) Hook(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID) (domain.Hook, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return domain.Hook{}, domain.ErrNotFound
	}

	t := table.WorkflowHooks

	return r.one(ctx, t.WorkflowID.EQ(jet.UUID(wid)).AND(t.NodeID.EQ(jet.String(string(node)))))
}

func (r *Hooks) ByHash(ctx context.Context, hash []byte) (domain.Hook, error) {
	return r.one(ctx, table.WorkflowHooks.SecretHash.EQ(jet.Bytea(hash)))
}

func (r *Hooks) one(ctx context.Context, where jet.BoolExpression) (domain.Hook, error) {
	t, w := table.WorkflowHooks, table.Workflows

	var row hookRow

	err := jet.SELECT(t.AllColumns, w.OwnerID).
		FROM(t.INNER_JOIN(w, w.ID.EQ(t.WorkflowID))).
		WHERE(where).
		QueryContext(ctx, r.db.conn(ctx), &row)
	if errors.Is(err, qrm.ErrNoRows) {
		return domain.Hook{}, domain.ErrNotFound
	}

	if err != nil {
		return domain.Hook{}, err
	}

	return domain.Hook{
		WorkflowID: domain.WorkflowID(row.WorkflowID.String()),
		NodeID:     domain.NodeID(row.NodeID),
		OwnerID:    domain.UserID(row.OwnerID.String()),
		Sealed:     row.Sealed,
		CreatedAt:  row.CreatedAt.UTC(),
	}, nil
}

func (r *Hooks) Put(ctx context.Context, h domain.Hook, hash []byte) error {
	wid, err := parseID(h.WorkflowID)
	if err != nil {
		return domain.ErrNotFound
	}

	t := table.WorkflowHooks

	_, err = t.INSERT(t.AllColumns).
		MODEL(model.WorkflowHooks{
			WorkflowID: wid, NodeID: string(h.NodeID), SecretHash: hash, Sealed: h.Sealed, CreatedAt: h.CreatedAt.UTC(),
		}).
		ON_CONFLICT(t.WorkflowID, t.NodeID).
		DO_UPDATE(jet.SET(
			t.SecretHash.SET(t.EXCLUDED.SecretHash),
			t.Sealed.SET(t.EXCLUDED.Sealed),
			t.CreatedAt.SET(t.EXCLUDED.CreatedAt),
		)).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}
