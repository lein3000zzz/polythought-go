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
		require.NoError(t, ledger.Update(ctx, failed, &ports.LedgerChange{Holding: empty}))

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

func TestRestingOrders(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	ledger := postgres.NewLedger(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	w := newWorkflow(alice.ID, "rests")
	other := newWorkflow(alice.ID, "other")
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))
	require.NoError(t, postgres.NewWorkflows(db).Create(ctx, other))

	_, err := ledger.OpenPaperAccount(ctx, domain.PaperAccount{WorkflowID: w.ID, OwnerID: alice.ID, Cash: num("100"), StartedWith: num("100"), StartedAt: at})
	require.NoError(t, err)

	resting := func(wf domain.WorkflowID, mode domain.TradingMode) domain.PlacedOrder {
		return domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{
				WorkflowID: wf, OwnerID: alice.ID, NodeID: "buy", Mode: mode, TokenID: "t-yes", MarketID: "0xm",
				Side: domain.SideBuy, Type: domain.OrderGTD, Price: num("0.4"), Shares: num("10"), Expires: at.Add(time.Hour),
			},
			ID: domain.OrderID(newID()), Status: domain.OrderOpen, Reason: "rests on the book", CreatedAt: at,
		}
	}

	paper, live, elsewhere := resting(w.ID, domain.ModePaper), resting(w.ID, domain.ModeLive), resting(other.ID, domain.ModePaper)
	for _, o := range []domain.PlacedOrder{paper, live, elsewhere} {
		require.NoError(t, ledger.Record(ctx, o, nil))
	}

	got, err := ledger.Order(ctx, paper.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.OrderOpen, got.Status)
	assert.Equal(t, at.Add(time.Hour), got.Expires)

	_, err = ledger.Order(ctx, domain.OrderID(newID()))
	require.ErrorIs(t, err, domain.ErrNotFound)

	open, err := ledger.OpenOrders(ctx, domain.ModePaper)
	require.NoError(t, err)
	assert.Len(t, open, 2, "every workflow's, on paper")

	mine, err := ledger.WorkflowOpenOrders(ctx, w.ID, domain.ModePaper)
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, paper.ID, mine[0].ID)

	t.Run("a fill later moves the paper cash with the holding", func(t *testing.T) {
		filled := paper
		filled.Status, filled.Reason = domain.OrderFilled, ""
		filled.Fills = []domain.OrderFill{{Price: num("0.4"), Shares: num("10"), Dollars: num("4"), Fee: decimal.Zero}}

		require.NoError(t, ledger.Update(ctx, filled, &ports.LedgerChange{Cash: num("-4"), Holding: domain.Holding{
			WorkflowID: w.ID, Mode: domain.ModePaper, TokenID: "t-yes", MarketID: "0xm", Shares: num("10"), Cost: num("4"),
			Realized: decimal.Zero, UpdatedAt: at,
		}}))

		a, err := ledger.PaperAccount(ctx, w.ID)
		require.NoError(t, err)
		assert.Equal(t, "96", a.Cash.String())

		mine, err := ledger.WorkflowOpenOrders(ctx, w.ID, domain.ModePaper)
		require.NoError(t, err)
		assert.Empty(t, mine, "filled: no longer resting")
	})

	t.Run("no more cash than there is", func(t *testing.T) {
		big := elsewhere
		big.Status = domain.OrderFilled
		require.ErrorIs(t, ledger.Update(ctx, big, &ports.LedgerChange{Cash: num("-1"), Holding: domain.Holding{
			WorkflowID: other.ID, Mode: domain.ModePaper, TokenID: "t-yes", Shares: num("1"), Cost: num("1"), Realized: decimal.Zero, UpdatedAt: at,
		}}), domain.ErrNotEnoughCash, "it has no paper account")
	})

	t.Run("cancelled and expired, as the statuses allow", func(t *testing.T) {
		for _, st := range []domain.OrderStatus{domain.OrderCanceled, domain.OrderExpired} {
			o := live
			o.Status = st
			require.NoError(t, ledger.Update(ctx, o, nil))
		}
	})
}

func TestLiveSafety(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	users := postgres.NewUsers(db)
	ledger := postgres.NewLedger(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	t.Run("the kill switch, and an admin's caps, which updates leave it be (D220, D222)", func(t *testing.T) {
		require.NoError(t, users.SetLiveStopped(ctx, alice.ID, at))

		u, err := users.ByID(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, at, u.LiveStoppedAt)
		assert.Equal(t, domain.LiveCaps{}, u.LiveCaps, "the instance's")

		perOrder, perDay := 250, 0
		u.LiveCaps = domain.LiveCaps{PerOrder: &perOrder, PerDay: &perDay}
		u.LiveStoppedAt = time.Time{}
		require.NoError(t, users.Update(ctx, u))

		got, err := users.ByID(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 250, *got.LiveCaps.PerOrder)
		assert.Equal(t, 0, *got.LiveCaps.PerDay, "0 is none, not the instance's")
		assert.Equal(t, at, got.LiveStoppedAt, "only SetLiveStopped turns it off")

		require.NoError(t, users.SetLiveStopped(ctx, alice.ID, time.Time{}))

		got, err = users.ByID(ctx, alice.ID)
		require.NoError(t, err)
		assert.False(t, got.LiveStopped())

		assert.ErrorIs(t, users.SetLiveStopped(ctx, domain.UserID(newID()), at), domain.ErrNotFound)
	})

	t.Run("what a user's live buys spent and may still spend, all their workflows' (D223)", func(t *testing.T) {
		wallet := seedWallet(t, db, alice, "0x2222222222222222222222222222222222222222")
		theirs := seedWallet(t, db, bob, "0x3333333333333333333333333333333333333333")

		one, two, bobs := newWorkflow(alice.ID, "one"), newWorkflow(alice.ID, "two"), newWorkflow(bob.ID, "bob's")
		for _, w := range []domain.Workflow{one, two, bobs} {
			require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))
		}

		order := func(w domain.Workflow, mode domain.TradingMode, side domain.Side, status domain.OrderStatus, created time.Time, fills ...domain.OrderFill) domain.PlacedOrder {
			o := domain.PlacedOrder{
				OrderRequest: domain.OrderRequest{
					WorkflowID: w.ID, OwnerID: w.OwnerID, NodeID: "buy", Mode: mode, TokenID: "t-yes", MarketID: "0xm",
					Side: side, Type: domain.OrderGTC, Price: num("0.5"), Shares: num("10"),
				},
				ID: domain.OrderID(newID()), Status: status, Fills: fills, CreatedAt: created,
			}

			if mode == domain.ModeLive {
				o.WalletID, o.ClobID = wallet.ID, "0x"+newID()
				if w.OwnerID == bob.ID {
					o.WalletID = theirs.ID
				}
			}

			return o
		}

		fill := func(status domain.FillStatus) domain.OrderFill {
			return domain.OrderFill{Price: num("0.5"), Shares: num("10"), Dollars: num("5"), Fee: num("0.1"), TradeID: "trade-" + newID(), Status: status}
		}

		open := order(two, domain.ModeLive, domain.SideBuy, domain.OrderOpen, at)
		pending := order(one, domain.ModeLive, domain.SideBuy, domain.OrderPending, at)

		for _, o := range []domain.PlacedOrder{
			order(one, domain.ModeLive, domain.SideBuy, domain.OrderFilled, at, fill(domain.FillConfirmed)),                   // counts
			order(two, domain.ModeLive, domain.SideBuy, domain.OrderFilled, at, fill(domain.FillMatched)),                     // counts
			order(one, domain.ModeLive, domain.SideBuy, domain.OrderFailed, at, fill(domain.FillFailed)),                      // undone
			order(one, domain.ModeLive, domain.SideBuy, domain.OrderFilled, at.Add(-2*time.Hour), fill(domain.FillConfirmed)), // before
			order(one, domain.ModeLive, domain.SideSell, domain.OrderFilled, at, fill(domain.FillConfirmed)),                  // a sell
			order(one, domain.ModePaper, domain.SideBuy, domain.OrderFilled, at, domain.OrderFill{Price: num("0.5"), Shares: num("10"), Dollars: num("5"), Fee: num("0")}),
			order(bobs, domain.ModeLive, domain.SideBuy, domain.OrderFilled, at, fill(domain.FillConfirmed)), // bob's
			order(bobs, domain.ModeLive, domain.SideBuy, domain.OrderOpen, at),
			open, pending,
		} {
			require.NoError(t, ledger.Record(ctx, o, nil))
		}

		spent, err := ledger.OwnerSpent(ctx, alice.ID, domain.ModeLive, at.Add(-time.Hour))
		require.NoError(t, err)
		assert.Equal(t, "10.2", spent.String(), "two buys of $5, with $0.10 in fees each")

		unsettled, err := ledger.OwnerUnsettled(ctx, alice.ID, domain.ModeLive)
		require.NoError(t, err)

		ids := make([]domain.OrderID, len(unsettled))
		for i, o := range unsettled {
			ids[i] = o.ID
		}

		assert.ElementsMatch(t, []domain.OrderID{open.ID, pending.ID}, ids)
	})
}

func TestOrderLog(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	ledger := postgres.NewLedger(db)
	workflows := postgres.NewWorkflows(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)
	main := seedWallet(t, db, alice, "0x4444444444444444444444444444444444444444")
	spare := seedWallet(t, db, alice, "0x5555555555555555555555555555555555555555")
	theirs := seedWallet(t, db, bob, "0x6666666666666666666666666666666666666666")

	kept, gone, bobs := newWorkflow(alice.ID, "kept"), newWorkflow(alice.ID, "gone"), newWorkflow(bob.ID, "bob's")
	for _, w := range []domain.Workflow{kept, gone, bobs} {
		require.NoError(t, workflows.Create(ctx, w))
	}

	at := at
	order := func(w domain.Workflow, wallet domain.Wallet, mode domain.TradingMode, status domain.OrderStatus) domain.PlacedOrder {
		at = at.Add(time.Second)

		o := domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{
				WorkflowID: w.ID, OwnerID: w.OwnerID, NodeID: "buy", Mode: mode, TokenID: "t-yes", MarketID: "0xm",
				Side: domain.SideBuy, Type: domain.OrderFAK, Price: num("0.5"), Shares: num("10"),
			},
			ID: domain.OrderID(newID()), Status: status, CreatedAt: at,
		}

		if mode == domain.ModeLive {
			o.WalletID, o.ClobID = wallet.ID, "0x"+newID()
		}

		require.NoError(t, ledger.Record(ctx, o, nil))

		return o
	}

	first := order(gone, main, domain.ModeLive, domain.OrderFilled)
	second := order(kept, spare, domain.ModeLive, domain.OrderRefused)
	third := order(kept, main, domain.ModeLive, domain.OrderFilled)
	order(kept, main, domain.ModePaper, domain.OrderFilled)
	order(bobs, theirs, domain.ModeLive, domain.OrderFilled)

	require.NoError(t, workflows.Delete(ctx, alice.ID, gone.ID))

	ids := func(p ports.Page[ports.LoggedOrder]) []domain.OrderID {
		out := make([]domain.OrderID, len(p.Items))
		for i, o := range p.Items {
			out[i] = o.ID
		}

		return out
	}

	live := ports.OrderLogFilter{Mode: domain.ModeLive}

	t.Run("every live order of theirs, the newest first, a deleted workflow's under its name (D228, D199)", func(t *testing.T) {
		page, err := ledger.OwnerOrders(ctx, alice.ID, live)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderID{third.ID, second.ID, first.ID}, ids(page))
		assert.Equal(t, "kept", page.Items[0].Workflow)
		assert.False(t, page.Items[0].Deleted)
		assert.Equal(t, "gone", page.Items[2].Workflow)
		assert.True(t, page.Items[2].Deleted)
	})

	t.Run("by wallet, workflow and status", func(t *testing.T) {
		f := live
		f.WalletID = spare.ID
		page, err := ledger.OwnerOrders(ctx, alice.ID, f)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderID{second.ID}, ids(page))

		f = live
		f.WorkflowID = gone.ID
		page, err = ledger.OwnerOrders(ctx, alice.ID, f)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderID{first.ID}, ids(page), "a deleted workflow's too")

		f = live
		f.Status = domain.OrderFilled
		page, err = ledger.OwnerOrders(ctx, alice.ID, f)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderID{third.ID, first.ID}, ids(page))

		f = live
		f.WalletID = "not-a-wallet"
		page, err = ledger.OwnerOrders(ctx, alice.ID, f)
		require.NoError(t, err)
		assert.Empty(t, page.Items)
	})

	t.Run("a page at a time", func(t *testing.T) {
		f := live
		f.Limit = 2
		page, err := ledger.OwnerOrders(ctx, alice.ID, f)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderID{third.ID, second.ID}, ids(page))
		require.NotEmpty(t, page.NextCursor)

		f.Cursor = page.NextCursor
		page, err = ledger.OwnerOrders(ctx, alice.ID, f)
		require.NoError(t, err)
		assert.Equal(t, []domain.OrderID{first.ID}, ids(page))
	})
}
