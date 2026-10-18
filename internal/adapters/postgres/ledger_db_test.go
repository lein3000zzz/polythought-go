//go:build db

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func num(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestLedger(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	ledger := postgres.NewLedger(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "copy")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	const trader = domain.WalletAddress("0x5f3C00000000000000000000000000000000A1b2")

	order := func(side domain.Side, status domain.OrderStatus, fills ...domain.OrderFill) domain.PlacedOrder {
		return domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{
				WorkflowID: w.ID, OwnerID: alice.ID, NodeID: "buy", Mode: domain.ModePaper,
				Trader: trader, TraderLabel: "Alice", TokenID: "t-yes", MarketID: "0xm", Side: side,
				Type: domain.OrderFAK, Price: num("0.55"), Dollars: num("10.25"),
			},
			ID: domain.OrderID(newID()), Status: status, Fills: fills, CreatedAt: at,
		}
	}

	holding := domain.Holding{
		WorkflowID: w.ID, Mode: domain.ModePaper, Trader: trader, TraderLabel: "Alice", TokenID: "t-yes", MarketID: "0xm",
		Shares: num("18.63"), Cost: num("10.2496"), Realized: decimal.Zero, UpdatedAt: at,
	}

	t.Run("a paper account opens once, with what it started with", func(t *testing.T) {
		_, err := ledger.PaperAccount(ctx, w.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		a, err := ledger.OpenPaperAccount(ctx, domain.PaperAccount{
			WorkflowID: w.ID, OwnerID: alice.ID, Cash: num("1000"), StartedWith: num("1000"), StartedAt: at,
		})
		require.NoError(t, err)
		assert.True(t, num("1000").Equal(a.Cash))

		again, err := ledger.OpenPaperAccount(ctx, domain.PaperAccount{
			WorkflowID: w.ID, OwnerID: alice.ID, Cash: num("5"), StartedWith: num("5"), StartedAt: at,
		})
		require.NoError(t, err)
		assert.True(t, num("1000").Equal(again.Cash), "opened before: kept as it is")
	})

	t.Run("a filled order moves cash and the holding, exactly", func(t *testing.T) {
		buy := order(domain.SideBuy, domain.OrderFilled,
			domain.OrderFill{Price: num("0.55"), Shares: num("10"), Dollars: num("5.5"), Fee: num("0.0099")},
			domain.OrderFill{Price: num("0.56"), Shares: num("8.63"), Dollars: num("4.8328"), Fee: num("0.00069")},
		)

		require.NoError(t, ledger.Record(ctx, buy, &ports.LedgerChange{Cash: num("-10.34339"), Holding: holding}))

		a, err := ledger.PaperAccount(ctx, w.ID)
		require.NoError(t, err)
		assert.Equal(t, "989.65661", a.Cash.String())

		h, err := ledger.Holding(ctx, holding.Key())
		require.NoError(t, err)
		assert.Equal(t, "18.63", h.Shares.String())
		assert.Equal(t, "10.2496", h.Cost.String())
		assert.Equal(t, "Alice", h.TraderLabel)

		page, err := ledger.Orders(ctx, w.ID, ports.PageFilter{})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)

		got := page.Items[0]
		assert.Equal(t, buy.ID, got.ID)
		assert.Equal(t, trader, got.Trader)
		assert.Equal(t, domain.OrderFilled, got.Status)
		require.Len(t, got.Fills, 2, "in order")
		assert.Equal(t, "0.00069", got.Fills[1].Fee.String())
		assert.Equal(t, "10.25", got.Dollars.String())
	})

	t.Run("cash never goes below zero, and nothing of the order stays", func(t *testing.T) {
		big := order(domain.SideBuy, domain.OrderFilled, domain.OrderFill{Price: num("0.5"), Shares: num("4000"), Dollars: num("2000"), Fee: num("0")})

		err := ledger.Record(ctx, big, &ports.LedgerChange{Cash: num("-2000"), Holding: holding})
		require.ErrorIs(t, err, domain.ErrNotEnoughCash)

		page, err := ledger.Orders(ctx, w.ID, ports.PageFilter{})
		require.NoError(t, err)
		assert.Len(t, page.Items, 1, "rolled back")
	})

	t.Run("nor do shares", func(t *testing.T) {
		short := holding
		short.Shares = num("-1")

		err := ledger.Record(ctx, order(domain.SideSell, domain.OrderFilled, domain.OrderFill{Price: num("0.5"), Shares: num("1"), Dollars: num("0.5"), Fee: num("0")}),
			&ports.LedgerChange{Cash: num("0.5"), Holding: short})
		require.ErrorIs(t, err, domain.ErrNotEnoughShares)
	})

	t.Run("a refused order is kept with why, and pages go newest first", func(t *testing.T) {
		refused := order(domain.SideBuy, domain.OrderRefused)
		refused.ExecutionID = domain.ExecutionID(newID()) // still running: recorded when it ends
		refused.Reason = "not enough paper cash"
		require.NoError(t, ledger.Record(ctx, refused, nil))

		first, err := ledger.Orders(ctx, w.ID, ports.PageFilter{Limit: 1})
		require.NoError(t, err)
		require.Len(t, first.Items, 1)
		assert.Equal(t, refused.ID, first.Items[0].ID)
		assert.Equal(t, "not enough paper cash", first.Items[0].Reason)
		assert.Equal(t, refused.ExecutionID, first.Items[0].ExecutionID)
		assert.Empty(t, first.Items[0].Fills)

		rest, err := ledger.Orders(ctx, w.ID, ports.PageFilter{Limit: 1, Cursor: first.NextCursor})
		require.NoError(t, err)
		require.Len(t, rest.Items, 1)
		assert.Equal(t, domain.OrderFilled, rest.Items[0].Status)

		holdings, err := ledger.Holdings(ctx, w.ID)
		require.NoError(t, err)
		assert.Len(t, holdings, 1)
	})

	t.Run("totals by trader since a time, settlements no copies", func(t *testing.T) {
		settled := order(domain.SideSell, domain.OrderFilled, domain.OrderFill{Price: num("1"), Shares: num("1"), Dollars: num("1"), Fee: num("0")})
		settled.Type = domain.OrderSettle
		require.NoError(t, ledger.Record(ctx, settled, nil))

		totals, err := ledger.TraderTotals(ctx, w.ID, domain.ModePaper, at.Add(-time.Hour))
		require.NoError(t, err)
		require.Len(t, totals, 1)
		assert.Equal(t, trader, totals[0].Trader)
		assert.Equal(t, 1, totals[0].Orders, "the filled buy; refused ones filled nothing")
		assert.Equal(t, "10.3328", totals[0].Volume.String())

		later, err := ledger.TraderTotals(ctx, w.ID, domain.ModePaper, at.Add(time.Hour))
		require.NoError(t, err)
		assert.Empty(t, later)
	})

	t.Run("open holdings, every workflow's", func(t *testing.T) {
		open, err := ledger.OpenHoldings(ctx, domain.ModePaper)
		require.NoError(t, err)
		require.Len(t, open, 1)
		assert.Equal(t, w.ID, open[0].WorkflowID)

		none, err := ledger.OpenHoldings(ctx, domain.ModeLive)
		require.NoError(t, err)
		assert.Empty(t, none)
	})

	t.Run("what buys spent since a time, fees in, sells and refused orders out", func(t *testing.T) {
		spent, err := ledger.Spent(ctx, w.ID, domain.ModePaper, at.Add(-time.Hour))
		require.NoError(t, err)
		assert.Equal(t, "10.34339", spent.String(), "the filled buy's 10.3328 and 0.01059 in fees")

		spent, err = ledger.Spent(ctx, w.ID, domain.ModePaper, at.Add(time.Hour))
		require.NoError(t, err)
		assert.True(t, spent.IsZero(), "nothing since")

		spent, err = ledger.Spent(ctx, w.ID, domain.ModeLive, at.Add(-time.Hour))
		require.NoError(t, err)
		assert.True(t, spent.IsZero(), "nor live")
	})
}

func TestLedgerReset(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	ledger := postgres.NewLedger(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "copy")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	_, err := ledger.OpenPaperAccount(ctx, domain.PaperAccount{WorkflowID: w.ID, OwnerID: alice.ID, Cash: num("1000"), StartedWith: num("1000"), StartedAt: at})
	require.NoError(t, err)

	buy := domain.PlacedOrder{
		OrderRequest: domain.OrderRequest{
			WorkflowID: w.ID, OwnerID: alice.ID, NodeID: "buy", Mode: domain.ModePaper, TokenID: "t", Side: domain.SideBuy,
			Type: domain.OrderFAK, Price: num("0.5"), Shares: num("10"),
		},
		ID: domain.OrderID(newID()), Status: domain.OrderFilled, CreatedAt: at,
		Fills: []domain.OrderFill{{Price: num("0.5"), Shares: num("10"), Dollars: num("5"), Fee: num("0")}},
	}
	require.NoError(t, ledger.Record(ctx, buy, &ports.LedgerChange{Cash: num("-5"), Holding: domain.Holding{
		WorkflowID: w.ID, Mode: domain.ModePaper, TokenID: "t", Shares: num("10"), Cost: num("5"), Realized: decimal.Zero, UpdatedAt: at,
	}}))

	later := at.Add(time.Hour)
	require.NoError(t, ledger.ResetPaperAccount(ctx, domain.PaperAccount{
		WorkflowID: w.ID, Cash: num("500"), StartedWith: num("500"), StartedAt: later, ResetAt: later,
	}))

	a, err := ledger.PaperAccount(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, "500", a.Cash.String())
	assert.Equal(t, later, a.ResetAt)

	holdings, err := ledger.Holdings(ctx, w.ID)
	require.NoError(t, err)
	assert.Empty(t, holdings, "emptied")

	page, err := ledger.Orders(ctx, w.ID, ports.PageFilter{})
	require.NoError(t, err)
	assert.Len(t, page.Items, 1, "the orders stay (D107)")

	err = ledger.ResetPaperAccount(ctx, domain.PaperAccount{WorkflowID: domain.WorkflowID(newID()), StartedAt: later})
	require.ErrorIs(t, err, domain.ErrNotFound, "no account to reset")
}
