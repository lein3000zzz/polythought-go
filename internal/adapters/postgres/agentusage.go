package postgres

import (
	"context"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.AgentUsageRepository = (*AgentUsage)(nil)

// AgentUsage keeps what users' agent nodes spent, a row per user per month (D268).
type AgentUsage struct {
	db *DB
}

func NewAgentUsage(db *DB) *AgentUsage {
	return &AgentUsage{db: db}
}

// Add adds to the month in one statement, so calls recorded at once each count.
func (r *AgentUsage) Add(ctx context.Context, owner domain.UserID, delta domain.AgentUsage, at time.Time) (domain.AgentUsage, error) {
	uid, err := parseID(owner)
	if err != nil {
		return domain.AgentUsage{}, err
	}

	au := table.AgentUsage
	row := model.AgentUsage{
		UserID: uid, Month: domain.MonthOf(delta.Month), InputTokens: delta.InputTokens, OutputTokens: delta.OutputTokens,
		Dollars: delta.Dollars, Calls: int32(delta.Calls), CostedCalls: int32(delta.CostedCalls), UpdatedAt: at, //nolint:gosec // a call's own counts
	}

	var got model.AgentUsage

	err = au.INSERT(au.AllColumns).MODEL(row).
		ON_CONFLICT(au.UserID, au.Month).
		DO_UPDATE(jet.SET(
			au.InputTokens.SET(au.InputTokens.ADD(au.EXCLUDED.InputTokens)),
			au.OutputTokens.SET(au.OutputTokens.ADD(au.EXCLUDED.OutputTokens)),
			au.Dollars.SET(au.Dollars.ADD(au.EXCLUDED.Dollars)),
			au.Calls.SET(au.Calls.ADD(au.EXCLUDED.Calls)),
			au.CostedCalls.SET(au.CostedCalls.ADD(au.EXCLUDED.CostedCalls)),
			au.UpdatedAt.SET(au.EXCLUDED.UpdatedAt),
		)).
		RETURNING(au.AllColumns).
		QueryContext(ctx, r.db.conn(ctx), &got)
	if err != nil {
		return domain.AgentUsage{}, err
	}

	return usageOf(got), nil
}

func (r *AgentUsage) Month(ctx context.Context, owner domain.UserID, month time.Time) (domain.AgentUsage, error) {
	month = domain.MonthOf(month)
	none := domain.AgentUsage{Month: month, Dollars: decimal.Zero}

	uid, err := parseID(owner)
	if err != nil {
		return none, err
	}

	au := table.AgentUsage

	var got model.AgentUsage

	err = au.SELECT(au.AllColumns).
		WHERE(au.UserID.EQ(jet.UUID(uid)).AND(au.Month.EQ(jet.TimestampzT(month)))).
		QueryContext(ctx, r.db.conn(ctx), &got)
	if errors.Is(err, qrm.ErrNoRows) {
		return none, nil
	}

	if err != nil {
		return none, err
	}

	return usageOf(got), nil
}

func usageOf(m model.AgentUsage) domain.AgentUsage {
	return domain.AgentUsage{
		Month: m.Month.UTC(), InputTokens: m.InputTokens, OutputTokens: m.OutputTokens, Dollars: m.Dollars,
		Calls: int(m.Calls), CostedCalls: int(m.CostedCalls),
	}
}
