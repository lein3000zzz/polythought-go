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

func TestWorkflowTrading(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	repo := postgres.NewWorkflowTrading(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	wallet := seedWallet(t, db, alice, "0x1111111111111111111111111111111111111111")

	w := newWorkflow(alice.ID, "copy")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

	_, err := repo.Trading(ctx, w.ID)
	require.ErrorIs(t, err, domain.ErrNotFound, "never said: on paper")

	live := domain.WorkflowTrading{WorkflowID: w.ID, Mode: domain.ModeLive, WalletID: wallet.ID, UpdatedAt: at}
	require.NoError(t, repo.SetTrading(ctx, live))

	got, err := repo.Trading(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, live, got)

	paper := domain.WorkflowTrading{WorkflowID: w.ID, Mode: domain.ModePaper, WalletID: wallet.ID, UpdatedAt: at.Add(time.Minute)}
	require.NoError(t, repo.SetTrading(ctx, paper))

	got, err = repo.Trading(ctx, w.ID)
	require.NoError(t, err)
	assert.Equal(t, paper, got, "changed in place")

	from, err := repo.TradingFrom(ctx, wallet.ID)
	require.NoError(t, err)
	assert.Equal(t, []domain.WorkflowTrading{paper}, from)

	require.ErrorIs(t, repo.SetTrading(ctx, domain.WorkflowTrading{WorkflowID: w.ID, Mode: domain.ModeLive, WalletID: domain.WalletID(newID()), UpdatedAt: at}),
		domain.ErrNotFound, "a wallet that is not there")

	t.Run("the wallet unlinked, the workflow keeps its mode without it", func(t *testing.T) {
		require.NoError(t, postgres.NewWallets(db).Delete(ctx, alice.ID, wallet.ID))

		got, err := repo.Trading(ctx, w.ID)
		require.NoError(t, err)
		assert.Empty(t, got.WalletID)
	})
}

func TestLiveBooks(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	ledger := postgres.NewLedger(db)
	workflows := postgres.NewWorkflows(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	wallet := seedWallet(t, db, alice, "0x1111111111111111111111111111111111111111")

	w := newWorkflow(alice.ID, "copy live")
	require.NoError(t, workflows.Create(ctx, w))

	order := func(mode domain.TradingMode, status domain.OrderStatus, created time.Time, fills ...domain.OrderFill) domain.PlacedOrder {
		o := domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{
				WorkflowID: w.ID, OwnerID: alice.ID, NodeID: "buy", Mode: mode, TokenID: "t-yes", MarketID: "0xm",
				Side: domain.SideBuy, Type: domain.OrderFAK, Price: num("0.55"), Dollars: num("5.5"),
			},
			ID: domain.OrderID(newID()), Status: status, Fills: fills, CreatedAt: created,
		}

		if mode == domain.ModeLive {
			o.WalletID, o.ClobID = wallet.ID, "0x"+newID()
		}

		return o
	}

	holding := func(mode domain.TradingMode, shares string) domain.Holding {
		return domain.Holding{
			WorkflowID: w.ID, Mode: mode, TokenID: "t-yes", MarketID: "0xm", Shares: num(shares), Cost: num("5.5"),
			Realized: decimal.Zero, UpdatedAt: at,
		}
	}

	matched := domain.OrderFill{Price: num("0.55"), Shares: num("10"), Dollars: num("5.5"), Fee: num("0.01"), TradeID: "trade-1", Status: domain.FillMatched}
	live := order(domain.ModeLive, domain.OrderFilled, at, matched)
	require.NoError(t, ledger.Record(ctx, live, &ports.LedgerChange{Holding: holding(domain.ModeLive, "10")}))

	pending := order(domain.ModeLive, domain.OrderPending, at.Add(time.Second))
	require.NoError(t, ledger.Record(ctx, pending, nil))

	done := order(domain.ModeLive, domain.OrderFilled, at.Add(2*time.Second), domain.OrderFill{
		Price: num("0.55"), Shares: num("1"), Dollars: num("0.55"), Fee: num("0"), TradeID: "trade-2", Status: domain.FillConfirmed,
	})
	require.NoError(t, ledger.Record(ctx, done, nil))

	paper := order(domain.ModePaper, domain.OrderFilled, at.Add(3*time.Second), domain.OrderFill{Price: num("0.55"), Shares: num("10"), Dollars: num("5.5"), Fee: num("0")})
	require.NoError(t, ledger.Record(ctx, paper, &ports.LedgerChange{Holding: holding(domain.ModePaper, "10")}))

	t.Run("a live order keeps its wallet, its CLOB ID and its trades", func(t *testing.T) {
		got, err := ledger.OrdersOf(ctx, domain.HoldingKey{WorkflowID: w.ID, Mode: domain.ModeLive, TokenID: "t-yes"})
		require.NoError(t, err)
		require.Len(t, got, 3, "the live ones, oldest first")

		assert.Equal(t, live.ID, got[0].ID)
		assert.Equal(t, wallet.ID, got[0].WalletID)
		assert.Equal(t, live.ClobID, got[0].ClobID)
		require.Len(t, got[0].Fills, 1)
		assert.Equal(t, "trade-1", got[0].Fills[0].TradeID)
		assert.Equal(t, domain.FillMatched, got[0].Fills[0].Status)

		h, err := ledger.Holding(ctx, domain.HoldingKey{WorkflowID: w.ID, Mode: domain.ModeLive, TokenID: "t-yes"})
		require.NoError(t, err)
		assert.Equal(t, alice.ID, h.OwnerID)
	})

	t.Run("followed: pending, or with trades not final", func(t *testing.T) {
		got, err := ledger.Following(ctx, at.Add(-time.Hour))
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, live.ID, got[0].ID)
		assert.Equal(t, pending.ID, got[1].ID)

		got, err = ledger.Following(ctx, at.Add(time.Millisecond))
		require.NoError(t, err)
		require.Len(t, got, 1, "only since then")
	})

	t.Run("an update replaces the status and fills, and sets the holding", func(t *testing.T) {
		failed := live
		failed.Status, failed.Reason = domain.OrderFailed, "its trade failed on chain"
		failed.Fills = []domain.OrderFill{matched}
		failed.Fills[0].Status = domain.FillFailed

		empty := holding(domain.ModeLive, "0")
		empty.Cost = decimal.Zero
		require.NoError(t, ledger.Update(ctx, failed, &empty))

		got, err := ledger.OrdersOf(ctx, domain.HoldingKey{WorkflowID: w.ID, Mode: domain.ModeLive, TokenID: "t-yes"})
		require.NoError(t, err)
		assert.Equal(t, domain.OrderFailed, got[0].Status)
		assert.Equal(t, domain.FillFailed, got[0].Fills[0].Status)

		h, err := ledger.Holding(ctx, empty.Key())
		require.NoError(t, err)
		assert.True(t, h.Shares.IsZero())

		following, err := ledger.Following(ctx, at.Add(-time.Hour))
		require.NoError(t, err)
		require.Len(t, following, 1, "its trades are final now")

		// A failed fill is undone: it neither spent nor traded.
		spent, err := ledger.Spent(ctx, w.ID, domain.ModeLive, at.Add(-time.Hour))
		require.NoError(t, err)
		assert.Equal(t, "0.55", spent.String(), "the confirmed buy of 1 at 0.55 alone")

		totals, err := ledger.TraderTotals(ctx, w.ID, domain.ModeLive, time.Time{})
		require.NoError(t, err)
		require.Len(t, totals, 1)
		assert.Equal(t, 1, totals[0].Orders)
		assert.Equal(t, "0.55", totals[0].Volume.String())

		require.ErrorIs(t, ledger.Update(ctx, order(domain.ModeLive, domain.OrderFilled, at), nil), domain.ErrNotFound)
	})

	t.Run("one CLOB order is recorded once", func(t *testing.T) {
		again := order(domain.ModeLive, domain.OrderFilled, at)
		again.ClobID = live.ClobID

		require.Error(t, ledger.Record(ctx, again, nil))
	})

	t.Run("deleting the workflow keeps its live books, named by it, and drops the paper ones", func(t *testing.T) {
		require.NoError(t, workflows.Delete(ctx, alice.ID, w.ID))

		got, err := ledger.OrdersOf(ctx, domain.HoldingKey{WorkflowID: w.ID, Mode: domain.ModeLive, TokenID: "t-yes"})
		require.NoError(t, err)
		assert.Len(t, got, 3)

		paperLeft, err := ledger.OrdersOf(ctx, domain.HoldingKey{WorkflowID: w.ID, Mode: domain.ModePaper, TokenID: "t-yes"})
		require.NoError(t, err)
		assert.Empty(t, paperLeft)

		holdings, err := ledger.Holdings(ctx, w.ID)
		require.NoError(t, err)
		require.Len(t, holdings, 1)
		assert.Equal(t, domain.ModeLive, holdings[0].Mode)

		var name string
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT workflow_name FROM orders WHERE id = $1`, string(live.ID)).Scan(&name))
		assert.Equal(t, "copy live", name)
		require.NoError(t, db.SQL.QueryRowContext(ctx, `SELECT workflow_name FROM holdings WHERE workflow_id = $1`, string(w.ID)).Scan(&name))
		assert.Equal(t, "copy live", name)
	})
}
