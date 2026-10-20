package postgres

import (
	"context"
	"errors"
	"time"

	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/model"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/gen/public/table"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.Ledger = (*Ledger)(nil)

// Ledger keeps orders, paper accounts and holdings. Amounts are numeric, read and written
// as decimals: nothing rounds on the way.
type Ledger struct {
	db *DB
}

func NewLedger(db *DB) *Ledger {
	return &Ledger{db: db}
}

func (r *Ledger) PaperAccount(ctx context.Context, workflow domain.WorkflowID) (domain.PaperAccount, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return domain.PaperAccount{}, err
	}

	pa := table.PaperAccounts

	var m model.PaperAccounts

	err = jet.SELECT(pa.AllColumns).FROM(pa).WHERE(pa.WorkflowID.EQ(jet.UUID(wid))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.PaperAccount{}, notFound(err)
	}

	return toPaperAccount(m), nil
}

func (r *Ledger) OpenPaperAccount(ctx context.Context, a domain.PaperAccount) (domain.PaperAccount, error) {
	wid, err := parseID(a.WorkflowID)
	if err != nil {
		return domain.PaperAccount{}, err
	}

	oid, err := parseID(a.OwnerID)
	if err != nil {
		return domain.PaperAccount{}, err
	}

	pa := table.PaperAccounts

	_, err = pa.INSERT(pa.AllColumns).
		MODEL(model.PaperAccounts{WorkflowID: wid, OwnerID: oid, Cash: a.Cash, StartedWith: a.StartedWith, StartedAt: a.StartedAt}).
		ON_CONFLICT(pa.WorkflowID).DO_NOTHING().
		ExecContext(ctx, r.db.conn(ctx))
	if err != nil {
		return domain.PaperAccount{}, err
	}

	return r.PaperAccount(ctx, a.WorkflowID)
}

func (r *Ledger) Holding(ctx context.Context, key domain.HoldingKey) (domain.Holding, error) {
	wid, err := parseID(key.WorkflowID)
	if err != nil {
		return domain.Holding{}, err
	}

	h := table.Holdings

	var m model.Holdings

	err = jet.SELECT(h.AllColumns).FROM(h).
		WHERE(h.WorkflowID.EQ(jet.UUID(wid)).
			AND(h.Mode.EQ(jet.String(string(key.Mode)))).
			AND(h.Trader.EQ(jet.String(string(key.Trader)))).
			AND(h.TokenID.EQ(jet.String(string(key.TokenID))))).
		QueryContext(ctx, r.db.conn(ctx), &m)
	if err != nil {
		return domain.Holding{}, notFound(err)
	}

	return toHolding(m), nil
}

// Holdings lists a workflow's positions, those sold out of included: what they made.
func (r *Ledger) Holdings(ctx context.Context, workflow domain.WorkflowID) ([]domain.Holding, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return nil, err
	}

	h := table.Holdings

	var rows []model.Holdings

	err = jet.SELECT(h.AllColumns).FROM(h).WHERE(h.WorkflowID.EQ(jet.UUID(wid))).
		ORDER_BY(h.UpdatedAt.DESC(), h.TokenID).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.Holding, len(rows))
	for i, m := range rows {
		out[i] = toHolding(m)
	}

	return out, nil
}

func (r *Ledger) OpenHoldings(ctx context.Context, mode domain.TradingMode) ([]domain.Holding, error) {
	h := table.Holdings

	var rows []model.Holdings

	err := jet.SELECT(h.AllColumns).FROM(h).
		WHERE(h.Mode.EQ(jet.String(string(mode))).AND(h.Shares.GT(jet.Decimal("0")))).
		ORDER_BY(h.WorkflowID, h.MarketID).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.Holding, len(rows))
	for i, m := range rows {
		out[i] = toHolding(m)
	}

	return out, nil
}

func (r *Ledger) TraderTotals(ctx context.Context, workflow domain.WorkflowID, mode domain.TradingMode, since time.Time) ([]domain.TraderTotal, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return nil, err
	}

	o, f := table.Orders, table.OrderFills

	var rows []struct {
		Trader string          `alias:"trader"`
		Orders int64           `alias:"orders"`
		Volume decimal.Decimal `alias:"volume"`
	}

	err = jet.SELECT(o.Trader.AS("trader"), jet.COUNT(jet.DISTINCT(o.ID)).AS("orders"), jet.SUM(f.Dollars).AS("volume")).
		FROM(o.INNER_JOIN(f, f.OrderID.EQ(o.ID))).
		WHERE(o.WorkflowID.EQ(jet.UUID(wid)).
			AND(o.Mode.EQ(jet.String(string(mode)))).
			AND(o.OrderType.NOT_EQ(jet.String(string(domain.OrderSettle)))).
			AND(o.CreatedAt.GT_EQ(jet.TimestampzT(since))).
			AND(f.Status.NOT_EQ(jet.String(string(domain.FillFailed))))). // undone (D193)
		GROUP_BY(o.Trader).
		ORDER_BY(o.Trader).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.TraderTotal, len(rows))
	for i, row := range rows {
		out[i] = domain.TraderTotal{Trader: domain.WalletAddress(row.Trader), Orders: int(row.Orders), Volume: row.Volume}
	}

	return out, nil
}

func (r *Ledger) ResetPaperAccount(ctx context.Context, a domain.PaperAccount) error {
	wid, err := parseID(a.WorkflowID)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		h := table.Holdings
		if _, err := h.DELETE().
			WHERE(h.WorkflowID.EQ(jet.UUID(wid)).AND(h.Mode.EQ(jet.String(string(domain.ModePaper))))).
			ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		pa := table.PaperAccounts

		res, err := pa.UPDATE(pa.Cash, pa.StartedWith, pa.StartedAt, pa.ResetAt).
			MODEL(model.PaperAccounts{Cash: a.Cash, StartedWith: a.StartedWith, StartedAt: a.StartedAt, ResetAt: timePtr(a.ResetAt)}).
			WHERE(pa.WorkflowID.EQ(jet.UUID(wid))).
			ExecContext(ctx, r.db.conn(ctx))

		return affectedOne(res, err)
	})
}

func (r *Ledger) Spent(ctx context.Context, workflow domain.WorkflowID, mode domain.TradingMode, since time.Time) (decimal.Decimal, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return decimal.Zero, err
	}

	o, f := table.Orders, table.OrderFills

	var row struct {
		Spent decimal.Decimal `alias:"spent"`
	}

	err = jet.SELECT(jet.COALESCE(jet.SUM(f.Dollars.ADD(f.Fee)), jet.Decimal("0")).AS("spent")).
		FROM(f.INNER_JOIN(o, o.ID.EQ(f.OrderID))).
		WHERE(o.WorkflowID.EQ(jet.UUID(wid)).
			AND(o.Mode.EQ(jet.String(string(mode)))).
			AND(o.Side.EQ(jet.String(string(domain.SideBuy)))).
			AND(o.CreatedAt.GT_EQ(jet.TimestampzT(since))).
			AND(f.Status.NOT_EQ(jet.String(string(domain.FillFailed))))). // undone (D193)
		QueryContext(ctx, r.db.conn(ctx), &row)
	if err != nil {
		return decimal.Zero, err
	}

	return row.Spent, nil
}

func (r *Ledger) Record(ctx context.Context, o domain.PlacedOrder, change *ports.LedgerChange) error {
	m, err := toOrderModel(o)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := table.Orders.INSERT(table.Orders.AllColumns).MODEL(m).ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		if err := r.insertFills(ctx, m.ID, o.Fills); err != nil {
			return err
		}

		if change == nil {
			return nil
		}

		if err := r.moveCash(ctx, o, m.WorkflowID, change.Cash); err != nil {
			return err
		}

		change.Holding.OwnerID = o.OwnerID

		return r.saveHolding(ctx, change.Holding, m.WorkflowID)
	})
}

func (r *Ledger) insertFills(ctx context.Context, order uuid.UUID, fills []domain.OrderFill) error {
	if len(fills) == 0 {
		return nil
	}

	rows := make([]model.OrderFills, len(fills))
	for i, f := range fills {
		rows[i] = model.OrderFills{
			OrderID: order, Seq: int32(i), Price: f.Price, Shares: f.Shares, Dollars: f.Dollars, Fee: f.Fee,
			TradeID: f.TradeID, Status: string(f.Status),
		}
	}

	_, err := table.OrderFills.INSERT(table.OrderFills.AllColumns).MODELS(rows).ExecContext(ctx, r.db.conn(ctx))

	return err
}

// saveHolding sets a holding as given; its owner is the one it was opened by.
func (r *Ledger) saveHolding(ctx context.Context, holding domain.Holding, workflow uuid.UUID) error {
	hm, err := toHoldingModel(holding, workflow)
	if err != nil {
		return err
	}

	h := table.Holdings

	_, err = h.INSERT(h.AllColumns).MODEL(hm).
		ON_CONFLICT(h.WorkflowID, h.Mode, h.Trader, h.TokenID).
		DO_UPDATE(jet.SET(
			h.TraderLabel.SET(h.EXCLUDED.TraderLabel), h.MarketID.SET(h.EXCLUDED.MarketID),
			h.Shares.SET(h.EXCLUDED.Shares), h.Cost.SET(h.EXCLUDED.Cost),
			h.Realized.SET(h.EXCLUDED.Realized), h.UpdatedAt.SET(h.EXCLUDED.UpdatedAt),
		)).
		ExecContext(ctx, r.db.conn(ctx))
	if isCheckViolation(err) {
		return domain.ErrNotEnoughShares
	}

	return err
}

func (r *Ledger) Update(ctx context.Context, o domain.PlacedOrder, change *ports.LedgerChange) error {
	id, err := uuid.Parse(string(o.ID))
	if err != nil {
		return err
	}

	wid, err := parseID(o.WorkflowID)
	if err != nil {
		return err
	}

	return r.db.WithinTx(ctx, func(ctx context.Context) error {
		t := table.Orders

		res, err := t.UPDATE(t.Status, t.Reason).SET(jet.String(string(o.Status)), jet.String(o.Reason)).
			WHERE(t.ID.EQ(jet.UUID(id))).
			ExecContext(ctx, r.db.conn(ctx))
		if err := affectedOne(res, err); err != nil {
			return err
		}

		f := table.OrderFills
		if _, err := f.DELETE().WHERE(f.OrderID.EQ(jet.UUID(id))).ExecContext(ctx, r.db.conn(ctx)); err != nil {
			return err
		}

		if err := r.insertFills(ctx, id, o.Fills); err != nil {
			return err
		}

		if change == nil {
			return nil
		}

		if err := r.moveCash(ctx, o, wid, change.Cash); err != nil {
			return err
		}

		h := change.Holding
		h.OwnerID = o.OwnerID

		return r.saveHolding(ctx, h, wid)
	})
}

func (r *Ledger) Order(ctx context.Context, id domain.OrderID) (domain.PlacedOrder, error) {
	oid, err := parseID(id)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	got, err := r.ordersWhere(ctx, table.Orders.ID.EQ(jet.UUID(oid)), table.Orders.ID.ASC())
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	if len(got) == 0 {
		return domain.PlacedOrder{}, domain.ErrNotFound
	}

	return got[0], nil
}

func (r *Ledger) OpenOrders(ctx context.Context, mode domain.TradingMode) ([]domain.PlacedOrder, error) {
	o := table.Orders

	return r.ordersWhere(ctx, o.Status.EQ(jet.String(string(domain.OrderOpen))).AND(o.Mode.EQ(jet.String(string(mode)))), o.ID.ASC())
}

func (r *Ledger) WorkflowOpenOrders(ctx context.Context, workflow domain.WorkflowID, mode domain.TradingMode) ([]domain.PlacedOrder, error) {
	wid, err := parseID(workflow)
	if err != nil {
		return nil, err
	}

	o := table.Orders

	return r.ordersWhere(ctx, o.Status.EQ(jet.String(string(domain.OrderOpen))).
		AND(o.Mode.EQ(jet.String(string(mode)))).
		AND(o.WorkflowID.EQ(jet.UUID(wid))), o.ID.ASC())
}

// moveCash adds cash to a paper order's account; live cash is the wallet's. The check
// stops the books going below zero whatever got here first.
func (r *Ledger) moveCash(ctx context.Context, o domain.PlacedOrder, workflow uuid.UUID, cash decimal.Decimal) error {
	if o.Mode != domain.ModePaper || cash.IsZero() {
		return nil
	}

	pa := table.PaperAccounts

	res, err := pa.UPDATE(pa.Cash).
		SET(pa.Cash.ADD(jet.Decimal(cash.String()))).
		WHERE(pa.WorkflowID.EQ(jet.UUID(workflow)).AND(pa.Cash.ADD(jet.Decimal(cash.String())).GT_EQ(jet.Decimal("0")))).
		ExecContext(ctx, r.db.conn(ctx))
	if err := affectedOne(res, err); errors.Is(err, domain.ErrNotFound) {
		return domain.ErrNotEnoughCash
	} else if err != nil {
		return err
	}

	return nil
}

func (r *Ledger) OrdersOf(ctx context.Context, key domain.HoldingKey) ([]domain.PlacedOrder, error) {
	wid, err := parseID(key.WorkflowID)
	if err != nil {
		return nil, err
	}

	o := table.Orders

	return r.ordersWhere(ctx, o.WorkflowID.EQ(jet.UUID(wid)).
		AND(o.Mode.EQ(jet.String(string(key.Mode)))).
		AND(o.Trader.EQ(jet.String(string(key.Trader)))).
		AND(o.TokenID.EQ(jet.String(string(key.TokenID)))), o.ID.ASC())
}

func (r *Ledger) Following(ctx context.Context, since time.Time) ([]domain.PlacedOrder, error) {
	o, f := table.Orders, table.OrderFills
	unsettled := jet.SELECT(f.OrderID).FROM(f).WHERE(f.Status.IN(
		jet.String(string(domain.FillMatched)), jet.String(string(domain.FillMined)), jet.String(string(domain.FillRetrying)),
	))

	return r.ordersWhere(ctx, o.Mode.EQ(jet.String(string(domain.ModeLive))).
		AND(o.CreatedAt.GT_EQ(jet.TimestampzT(since))).
		AND(o.Status.EQ(jet.String(string(domain.OrderPending))).OR(o.ID.IN(unsettled))), o.ID.ASC())
}

// ordersWhere are the orders where says, in order, their fills with them.
func (r *Ledger) ordersWhere(ctx context.Context, where jet.BoolExpression, order jet.OrderByClause) ([]domain.PlacedOrder, error) {
	o, f := table.Orders, table.OrderFills

	var rows []struct {
		model.Orders

		Fills []model.OrderFills
	}

	err := jet.SELECT(o.AllColumns, f.AllColumns).
		FROM(o.LEFT_JOIN(f, f.OrderID.EQ(o.ID))).
		WHERE(where).
		ORDER_BY(order, f.Seq).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return nil, err
	}

	out := make([]domain.PlacedOrder, len(rows))
	for i, row := range rows {
		out[i] = toPlacedOrder(row.Orders, row.Fills)
	}

	return out, nil
}

// Orders lists the newest first, their fills with them.
func (r *Ledger) Orders(ctx context.Context, workflow domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.PlacedOrder], error) {
	wid, err := parseID(workflow)
	if err != nil {
		return ports.Page[domain.PlacedOrder]{}, err
	}

	limit := pageLimit(f.Limit)
	o := table.Orders
	where := o.WorkflowID.EQ(jet.UUID(wid))

	if f.Cursor != "" {
		before, err := uuid.Parse(f.Cursor)
		if err != nil {
			return ports.Page[domain.PlacedOrder]{}, domain.ErrInvalidInput
		}

		where = where.AND(o.ID.LT(jet.UUID(before)))
	}

	var rows []struct {
		model.Orders

		Fills []model.OrderFills
	}

	err = jet.SELECT(o.AllColumns, table.OrderFills.AllColumns).
		FROM(o.LEFT_JOIN(table.OrderFills, table.OrderFills.OrderID.EQ(o.ID))).
		WHERE(o.ID.IN(
			jet.SELECT(o.ID).FROM(o).WHERE(where).ORDER_BY(o.ID.DESC()).LIMIT(int64(limit)),
		)).
		ORDER_BY(o.ID.DESC(), table.OrderFills.Seq).
		QueryContext(ctx, r.db.conn(ctx), &rows)
	if err != nil && !errors.Is(err, qrm.ErrNoRows) {
		return ports.Page[domain.PlacedOrder]{}, err
	}

	page := ports.Page[domain.PlacedOrder]{Items: make([]domain.PlacedOrder, len(rows))}
	for i, row := range rows {
		page.Items[i] = toPlacedOrder(row.Orders, row.Fills)
	}

	if len(rows) == limit {
		page.NextCursor = rows[len(rows)-1].ID.String()
	}

	return page, nil
}

func toOrderModel(o domain.PlacedOrder) (model.Orders, error) {
	id, err := uuid.Parse(string(o.ID))
	if err != nil {
		return model.Orders{}, err
	}

	wid, err := parseID(o.WorkflowID)
	if err != nil {
		return model.Orders{}, err
	}

	oid, err := parseID(o.OwnerID)
	if err != nil {
		return model.Orders{}, err
	}

	var execution *uuid.UUID
	if o.ExecutionID != "" {
		e, err := uuid.Parse(string(o.ExecutionID))
		if err != nil {
			return model.Orders{}, err
		}

		execution = &e
	}

	var wallet *uuid.UUID
	if o.WalletID != "" {
		w, err := uuid.Parse(string(o.WalletID))
		if err != nil {
			return model.Orders{}, err
		}

		wallet = &w
	}

	return model.Orders{
		ID: id, WorkflowID: wid, OwnerID: oid, ExecutionID: execution, NodeID: string(o.NodeID),
		Mode: string(o.Mode), Trader: string(o.Trader), TraderLabel: o.TraderLabel,
		TokenID: string(o.TokenID), MarketID: string(o.MarketID), Side: string(o.Side),
		OrderType: string(o.Type), PostOnly: o.PostOnly, Price: o.Price, Shares: o.Shares, Dollars: o.Dollars,
		ExpiresAt: timePtr(o.Expires), Status: string(o.Status), Reason: o.Reason, CreatedAt: o.CreatedAt,
		WalletID: wallet, ClobID: o.ClobID,
	}, nil
}

func toPlacedOrder(m model.Orders, fills []model.OrderFills) domain.PlacedOrder {
	o := domain.PlacedOrder{
		OrderRequest: domain.OrderRequest{
			WorkflowID: domain.WorkflowID(m.WorkflowID.String()), OwnerID: domain.UserID(m.OwnerID.String()),
			NodeID: domain.NodeID(m.NodeID), Mode: domain.TradingMode(m.Mode),
			Trader: domain.WalletAddress(m.Trader), TraderLabel: m.TraderLabel,
			TokenID: domain.TokenID(m.TokenID), MarketID: domain.MarketID(m.MarketID), Side: domain.Side(m.Side),
			Type: domain.OrderType(m.OrderType), PostOnly: m.PostOnly, Price: m.Price, Shares: m.Shares, Dollars: m.Dollars,
		},
		ID: domain.OrderID(m.ID.String()), Status: domain.OrderStatus(m.Status), Reason: m.Reason,
		CreatedAt: m.CreatedAt.UTC(), ClobID: m.ClobID,
	}

	if m.WalletID != nil {
		o.WalletID = domain.WalletID(m.WalletID.String())
	}

	if m.ExecutionID != nil {
		o.ExecutionID = domain.ExecutionID(m.ExecutionID.String())
	}

	if m.ExpiresAt != nil {
		o.Expires = m.ExpiresAt.UTC()
	}

	for _, f := range fills {
		o.Fills = append(o.Fills, domain.OrderFill{
			Price: f.Price, Shares: f.Shares, Dollars: f.Dollars, Fee: f.Fee,
			TradeID: f.TradeID, Status: domain.FillStatus(f.Status),
		})
	}

	return o
}

func toPaperAccount(m model.PaperAccounts) domain.PaperAccount {
	a := domain.PaperAccount{
		WorkflowID: domain.WorkflowID(m.WorkflowID.String()), OwnerID: domain.UserID(m.OwnerID.String()),
		Cash: m.Cash, StartedWith: m.StartedWith, StartedAt: m.StartedAt.UTC(),
	}

	if m.ResetAt != nil {
		a.ResetAt = m.ResetAt.UTC()
	}

	return a
}

func toHolding(m model.Holdings) domain.Holding {
	return domain.Holding{
		WorkflowID: domain.WorkflowID(m.WorkflowID.String()), OwnerID: domain.UserID(m.OwnerID.String()),
		Mode:   domain.TradingMode(m.Mode),
		Trader: domain.WalletAddress(m.Trader), TraderLabel: m.TraderLabel,
		TokenID: domain.TokenID(m.TokenID), MarketID: domain.MarketID(m.MarketID),
		Shares: m.Shares, Cost: m.Cost, Realized: m.Realized, UpdatedAt: m.UpdatedAt.UTC(),
	}
}

func toHoldingModel(h domain.Holding, workflow uuid.UUID) (model.Holdings, error) {
	owner, err := parseID(h.OwnerID)
	if err != nil {
		return model.Holdings{}, err
	}

	return model.Holdings{
		WorkflowID: workflow, OwnerID: owner, Mode: string(h.Mode), Trader: string(h.Trader), TraderLabel: h.TraderLabel,
		TokenID: string(h.TokenID), MarketID: string(h.MarketID),
		Shares: h.Shares, Cost: h.Cost, Realized: h.Realized, UpdatedAt: h.UpdatedAt,
	}, nil
}
