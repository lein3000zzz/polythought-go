package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

func TestCancelOrders(t *testing.T) {
	ctx := context.Background()

	t.Run("the workflow's own, by order, market, outcome or all (D207)", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		one, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		two, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.41", "10"))
		require.NoError(t, err)

		got, err := svc.CancelOrders(ctx, ports.CancelRequest{WorkflowID: "wf", OwnerID: "alice", OrderID: one.ID})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Empty(t, got[0].Reason)
		assert.Equal(t, domain.OrderCanceled, got[0].Order.Status)
		assert.Equal(t, "cancelled by Cancel orders, 0 of 10 shares filled", got[0].Order.Reason)

		again, err := svc.CancelOrders(ctx, ports.CancelRequest{WorkflowID: "wf", OwnerID: "alice", OrderID: one.ID})
		require.NoError(t, err)
		assert.Equal(t, "it no longer rests on the book: canceled", again[0].Reason)

		theirs, err := svc.CancelOrders(ctx, ports.CancelRequest{WorkflowID: "wf-other", OwnerID: "alice", OrderID: two.ID})
		require.NoError(t, err)
		assert.Equal(t, "the workflow has no such order", theirs[0].Reason)

		none, err := svc.CancelOrders(ctx, ports.CancelRequest{WorkflowID: "wf", OwnerID: "alice", MarketID: "0xother"})
		require.NoError(t, err)
		assert.Empty(t, none)

		all, err := svc.CancelOrders(ctx, ports.CancelRequest{WorkflowID: "wf", OwnerID: "alice", All: true})
		require.NoError(t, err)
		require.Len(t, all, 1)
		assert.Equal(t, two.ID, all[0].Order.ID)
	})

	t.Run("live: what Polymarket would not cancel says why", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)

		l.exchange.cancel = ports.LiveCancel{Reason: "Order already matched"}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveResting}}

		got, err := l.svc.CancelOrders(ctx, ports.CancelRequest{WorkflowID: "wf", OwnerID: "alice", TokenID: "t-yes"})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, o.ID, got[0].Order.ID)
		assert.Equal(t, "Order already matched", got[0].Reason)
	})
}

func TestCancelledOnTheirOwn(t *testing.T) {
	ctx := context.Background()

	resting := func(t *testing.T) (live, domain.PlacedOrder) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)

		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveDone}}

		return l, o
	}

	t.Run("back to paper, none left behind (D213)", func(t *testing.T) {
		l, o := resting(t)

		s, err := l.svc.Setup(ctx, owner, "wf")
		require.NoError(t, err)
		assert.Equal(t, 1, s.Resting)

		s, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-1"})
		require.NoError(t, err)
		assert.Equal(t, domain.ModePaper, s.Mode)
		assert.Zero(t, s.Resting)
		assert.Equal(t, []string{"0xabc"}, l.exchange.cancelled)

		got, err := l.ledger.Order(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
	})

	t.Run("those Polymarket would not cancel are counted", func(t *testing.T) {
		l, _ := resting(t)
		l.exchange.cancel = ports.LiveCancel{Reason: "Polymarket did not say"}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveResting}}

		left, err := l.svc.CancelResting(ctx, "wf", "cancelled: the workflow was switched off")
		require.NoError(t, err)
		assert.Equal(t, 1, left)
	})

	t.Run("its wallet unlinked: named first, then freed and back on paper (D218)", func(t *testing.T) {
		l, o := resting(t)

		names, err := l.svc.TradingLive(ctx, "alice", "w-1")
		require.NoError(t, err)
		assert.Len(t, names, 1)

		require.NoError(t, l.svc.ReleaseWallet(ctx, "alice", "w-1"))
		assert.Equal(t, domain.ModePaper, l.repo.m["wf"].Mode)
		assert.Empty(t, l.repo.m["wf"].WalletID)

		got, err := l.ledger.Order(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
		assert.Contains(t, got.Reason, "cancelled")
	})
}

func next(t *testing.T, events <-chan domain.OrderEvent) domain.OrderEvent {
	t.Helper()

	select {
	case e := <-events:
		return e
	case <-time.After(time.Second):
		require.FailNow(t, "no order event")
		return domain.OrderEvent{}
	}
}

func TestOrderEvents(t *testing.T) {
	ctx := context.Background()

	t.Run("on paper: a later fill and a cancel (D215, D216)", func(t *testing.T) {
		now := at
		l, f := newLedger(), &feed{}
		svc := paperService(l, f, &now)

		listen, stop := context.WithCancel(ctx)
		defer stop()
		events := svc.OrderEvents(listen, "wf")

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)

		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		go func() {
			defer close(done)
			svc.RestEvery(rctx, time.Millisecond)
		}()

		f.trade(t, "0.39", "4")

		filled := next(t, events)
		assert.Equal(t, domain.OrderEventFilled, filled.Kind)
		assert.Equal(t, o.ID, filled.Order.ID)
		require.Len(t, filled.Fills, 1)
		assert.Equal(t, "4", filled.Fills[0].Shares.String())

		cancel()
		<-done

		_, err = svc.Cancel(ctx, owner, "wf", o.ID)
		require.NoError(t, err)

		cancelled := next(t, events)
		assert.Equal(t, domain.OrderEventCanceled, cancelled.Kind)
		assert.Equal(t, domain.OrderCanceled, cancelled.Order.Status)
	})

	t.Run("live: a failed trade (D193)", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveMatched, TradeIDs: []string{"trade-1"}}
		l.exchange.setTrade(matched("trade-1", "10"))

		events := l.svc.OrderEvents(ctx, "wf")

		_, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)

		failed := matched("trade-1", "10")
		failed.Status = domain.FillFailed
		l.exchange.setTrade(failed)

		require.NoError(t, l.svc.Follow(ctx))

		e := next(t, events)
		assert.Equal(t, domain.OrderEventFailed, e.Kind)
		require.Len(t, e.Fills, 1)
		assert.Equal(t, domain.FillFailed, e.Fills[0].Status)
	})

	t.Run("none for another workflow, and none once it stops listening", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		listen, stop := context.WithCancel(ctx)
		elsewhere := svc.OrderEvents(listen, "wf-other")

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		_, err = svc.Cancel(ctx, owner, "wf", o.ID)
		require.NoError(t, err)

		stop()

		for range elsewhere {
			require.FailNow(t, "an event of another workflow")
		}
	})
}

func TestHeartbeats(t *testing.T) {
	ctx := context.Background()

	l := liveService(t, func(d *trading.Deps) { d.Keys = keys{beating: map[domain.WalletID]bool{"w-1": true}} })
	l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

	_, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
	require.NoError(t, err)

	l.exchange.states = []ports.LiveOrderState{{State: ports.LiveResting}}

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		l.svc.WatchLive(wctx, time.Hour)
	}()

	// The first arms Polymarket's switch; it beats while the order rests (D214).
	require.Eventually(t, func() bool {
		l.exchange.mu.Lock()
		defer l.exchange.mu.Unlock()

		return len(l.exchange.beats) >= 1
	}, time.Second, time.Millisecond)

	cancel()
	<-done

	l.exchange.mu.Lock()
	defer l.exchange.mu.Unlock()
	assert.Equal(t, "", l.exchange.beats[0], "the first with no ID")
}
