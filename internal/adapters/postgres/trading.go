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

var _ ports.WorkflowTradingRepository = (*WorkflowTrading)(nil)

// WorkflowTrading keeps how workflows trade (D191): a row once a workflow says, none for
// one on paper from the start (D22).
type WorkflowTrading struct {
	db *DB
}

func NewWorkflowTrading(db *DB) *WorkflowTrading {
	return &WorkflowTrading{db: db}
}

func (r *WorkflowTrading) Trading(ctx context.Context, workflow domain.WorkflowID) (domain.WorkflowTrading, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return domain.WorkflowTrading{}, err
	}

	t := table.WorkflowTrading

	var m model.WorkflowTrading

	err = jet.SELECT(t.AllColumns).FROM(t).WHERE(t.WorkflowID.EQ(jet.UUID(wid))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.WorkflowTrading{}, notFound(err)
	}

	return toWorkflowTrading(m), nil
}

func (r *WorkflowTrading) SetTrading(ctx context.Context, w domain.WorkflowTrading) error {
	wid, err := parseID(w.WorkflowID)
	if err != nil {
		return err
	}

	var wallet *uuid.UUID
	if w.WalletID != "" {
		id, err := parseID(w.WalletID)
		if err != nil {
			return err
		}

		wallet = &id
	}

	t := table.WorkflowTrading

	_, err = t.INSERT(t.AllColumns).
		MODEL(model.WorkflowTrading{WorkflowID: wid, Mode: string(w.Mode), WalletID: wallet, UpdatedAt: w.UpdatedAt}).
		ON_CONFLICT(t.WorkflowID).
		DO_UPDATE(jet.SET(t.Mode.SET(t.EXCLUDED.Mode), t.WalletID.SET(t.EXCLUDED.WalletID), t.UpdatedAt.SET(t.EXCLUDED.UpdatedAt))).
		ExecContext(ctx, r.db.conn(ctx))
	if isForeignKeyViolation(err) {
		return domain.ErrNotFound // the workflow or the wallet is gone
	}

	return err
}

func (r *WorkflowTrading) TradingFrom(ctx context.Context, wallet domain.WalletID) ([]domain.WorkflowTrading, error) {
	id, err := parseID(wallet)
	if err != nil {
		return nil, nil
	}

	t := table.WorkflowTrading

	var rows []model.WorkflowTrading

	err = jet.SELECT(t.AllColumns).FROM(t).WHERE(t.WalletID.EQ(jet.UUID(id))).ORDER_BY(t.WorkflowID).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.WorkflowTrading, len(rows))
	for i, m := range rows {
		out[i] = toWorkflowTrading(m)
	}

	return out, nil
}

func (r *WorkflowTrading) OwnerTrading(ctx context.Context, owner domain.UserID) ([]domain.WorkflowTrading, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, nil
	}

	t, w := table.WorkflowTrading, table.Workflows

	var rows []model.WorkflowTrading

	err = jet.SELECT(t.AllColumns).FROM(t.INNER_JOIN(w, w.ID.EQ(t.WorkflowID))).
		WHERE(w.OwnerID.EQ(jet.UUID(oid))).ORDER_BY(t.WorkflowID).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.WorkflowTrading, len(rows))
	for i, m := range rows {
		out[i] = toWorkflowTrading(m)
	}

	return out, nil
}

func toWorkflowTrading(m model.WorkflowTrading) domain.WorkflowTrading {
	t := domain.WorkflowTrading{
		WorkflowID: domain.WorkflowID(m.WorkflowID.String()), Mode: domain.TradingMode(m.Mode), UpdatedAt: m.UpdatedAt.UTC(),
	}

	if m.WalletID != nil {
		t.WalletID = domain.WalletID(m.WalletID.String())
	}

	return t
}
