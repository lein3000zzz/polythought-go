package postgres

import (
	"context"
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

var _ ports.WorkflowHistory = (*WorkflowHistory)(nil)

// WorkflowHistory keeps who switched each workflow and why (D433).
type WorkflowHistory struct {
	db *DB
}

func NewWorkflowHistory(db *DB) *WorkflowHistory {
	return &WorkflowHistory{db: db}
}

func (r *WorkflowHistory) Record(ctx context.Context, s domain.Switch) error {
	id, err := parseID(s.ID)
	if err != nil {
		return err
	}

	wid, err := parseID(s.WorkflowID)
	if err != nil {
		return err
	}

	t := table.WorkflowSwitches

	_, err = t.INSERT(t.AllColumns).MODEL(model.WorkflowSwitches{
		ID: id, WorkflowID: wid, Kind: string(s.Kind),
		ByGrant: string(s.By.GrantID), ByToken: string(s.By.TokenID), ByName: s.By.Name, ByConfirmed: s.By.Confirmed,
		Polythought: s.Polythought, Reason: s.Reason, WalletID: string(s.WalletID), WalletAddress: string(s.WalletAddress),
		At: s.At.UTC(),
	}).ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *WorkflowHistory) List(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID, f ports.SwitchFilter) (ports.Page[domain.Switch], error) {
	oid, err := parseID(owner)
	if err != nil {
		return ports.Page[domain.Switch]{}, domain.ErrNotFound
	}

	wid, err := parseID(workflow)
	if err != nil {
		return ports.Page[domain.Switch]{}, domain.ErrNotFound
	}

	w := table.Workflows

	var found []struct {
		ID uuid.UUID `alias:"workflows.id"`
	}

	err = jet.SELECT(w.ID).FROM(w).WHERE(w.ID.EQ(jet.UUID(wid)).AND(w.OwnerID.EQ(jet.UUID(oid)))).
		QueryContext(ctx, r.db.conn(ctx), &found)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return ports.Page[domain.Switch]{}, err
	}

	if len(found) == 0 {
		return ports.Page[domain.Switch]{}, domain.ErrNotFound
	}

	t := table.WorkflowSwitches
	where := t.WorkflowID.EQ(jet.UUID(wid))

	if len(f.Kinds) > 0 {
		kinds := make([]jet.Expression, len(f.Kinds))
		for i, k := range f.Kinds {
			kinds[i] = jet.String(string(k))
		}

		where = where.AND(t.Kind.IN(kinds...))
	}

	if f.Cursor != "" {
		before, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.Switch]{}, domain.ErrInvalidInput
		}

		where = where.AND(t.ID.LT(jet.UUID(before)))
	}

	limit := pageLimit(f.Limit)

	var rows []model.WorkflowSwitches

	err = jet.SELECT(t.AllColumns).FROM(t).WHERE(where).ORDER_BY(t.ID.DESC()).LIMIT(int64(limit)).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return ports.Page[domain.Switch]{}, err
	}

	page := ports.Page[domain.Switch]{Items: make([]domain.Switch, len(rows))}
	for i, m := range rows {
		page.Items[i] = domain.Switch{
			ID: domain.SwitchID(m.ID.String()), WorkflowID: domain.WorkflowID(m.WorkflowID.String()), Kind: domain.SwitchKind(m.Kind),
			By:          domain.Actor{GrantID: domain.GrantID(m.ByGrant), TokenID: domain.APITokenID(m.ByToken), Name: m.ByName, Confirmed: m.ByConfirmed},
			Polythought: m.Polythought, Reason: m.Reason,
			WalletID: domain.WalletID(m.WalletID), WalletAddress: domain.WalletAddress(m.WalletAddress), At: m.At.UTC(),
		}
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

func (r *WorkflowHistory) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	t := table.WorkflowSwitches

	res, err := t.DELETE().WHERE(t.At.LT(jet.TimestampzT(before.UTC()))).ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}
