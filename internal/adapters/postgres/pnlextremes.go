package postgres

import (
	"context"
	"errors"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.PnLExtremes = (*PnLExtremes)(nil)

// PnLExtremes keeps each workflow's highest and lowest profit and loss in a mode (D430).
type PnLExtremes struct {
	db *DB
}

func NewPnLExtremes(db *DB) *PnLExtremes {
	return &PnLExtremes{db: db}
}

// Record keeps the higher and the lower of each sample and what was kept, in the database
// itself, so samplers at once lose none; one counting from a later Since starts over.
func (r *PnLExtremes) Record(ctx context.Context, samples []domain.PnLSample) error {
	if len(samples) == 0 {
		return nil
	}

	rows := make([]model.PnlExtremes, 0, len(samples))

	for _, s := range samples {
		wid, err := parseID(s.WorkflowID)
		if err != nil {
			return err
		}

		rows = append(rows, model.PnlExtremes{
			WorkflowID: wid, Mode: string(s.Mode), Since: s.Since.UTC(),
			High: s.Profit, HighAt: s.At.UTC(), Low: s.Profit, LowAt: s.At.UTC(),
		})
	}

	t := table.PnlExtremes
	over := t.Since.LT(t.EXCLUDED.Since) // counting from a later start: kept no more
	higher := over.OR(t.EXCLUDED.High.GT(t.High))
	lower := over.OR(t.EXCLUDED.Low.LT(t.Low))

	// A sample counting from an earlier start than kept is an old one: nothing changes.
	_, err := t.INSERT(t.AllColumns).MODELS(rows).
		ON_CONFLICT(t.WorkflowID, t.Mode).
		DO_UPDATE(jet.SET(
			t.High.SET(jet.FloatExp(jet.CASE().WHEN(higher).THEN(t.EXCLUDED.High).ELSE(t.High))),
			t.HighAt.SET(jet.TimestampzExp(jet.CASE().WHEN(higher).THEN(t.EXCLUDED.HighAt).ELSE(t.HighAt))),
			t.Low.SET(jet.FloatExp(jet.CASE().WHEN(lower).THEN(t.EXCLUDED.Low).ELSE(t.Low))),
			t.LowAt.SET(jet.TimestampzExp(jet.CASE().WHEN(lower).THEN(t.EXCLUDED.LowAt).ELSE(t.LowAt))),
			t.Since.SET(t.EXCLUDED.Since),
		).WHERE(t.Since.LT_EQ(t.EXCLUDED.Since))).
		ExecContext(ctx, r.db.conn(ctx))

	return err
}

func (r *PnLExtremes) Owner(ctx context.Context, owner domain.UserID) ([]domain.PnLExtremes, error) {
	oid, err := parseID(owner)
	if err != nil {
		return nil, nil
	}

	t, w := table.PnlExtremes, table.Workflows

	var rows []model.PnlExtremes

	err = jet.SELECT(t.AllColumns).FROM(t.INNER_JOIN(w, w.ID.EQ(t.WorkflowID))).
		WHERE(w.OwnerID.EQ(jet.UUID(oid))).ORDER_BY(t.WorkflowID, t.Mode).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.PnLExtremes, len(rows))
	for i, m := range rows {
		out[i] = domain.PnLExtremes{
			WorkflowID: domain.WorkflowID(m.WorkflowID.String()), Mode: domain.TradingMode(m.Mode), Since: m.Since.UTC(),
			High: m.High, HighAt: m.HighAt.UTC(), Low: m.Low, LowAt: m.LowAt.UTC(),
		}
	}

	return out, nil
}
