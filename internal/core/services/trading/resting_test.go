package trading_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// feed is the live market feed: the test sends trades into the latest subscription.
type feed struct {
	mu   sync.Mutex
	subs []*feedSub
}

type feedSub struct {
	tokens []domain.TokenID
	ch     chan domain.MarketUpdate
	once   sync.Once
}

func (f *feed) Subscribe(_ context.Context, tokens []domain.TokenID) (ports.FeedSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	s := &feedSub{tokens: tokens, ch: make(chan domain.MarketUpdate, 16)}
	f.subs = append(f.subs, s)

	return s, nil
}

func (s *feedSub) Updates() <-chan domain.MarketUpdate { return s.ch }
func (s *feedSub) Close()                              { s.once.Do(func() { close(s.ch) }) }

// trade sends a trade on t-yes to the subscription watching it, once there is one.
func (f *feed) trade(t *testing.T, price, size string) {
	t.Helper()

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()

		return len(f.subs) > 0 && len(f.subs[len(f.subs)-1].tokens) > 0
	}, time.Second, time.Millisecond)

	f.mu.Lock()
	s := f.subs[len(f.subs)-1]
	f.mu.Unlock()

	s.ch <- domain.TradeUpdate{Trade: domain.Trade{TokenID: "t-yes", Price: d(price), Size: d(size)}}
}

// stream is a wallet's user channel: the test sends its events.
type stream struct{ events chan ports.LiveOrderEvent }

func (s stream) Watch(ctx context.Context, _ ports.OrderKey) <-chan ports.LiveOrderEvent {
	out := make(chan ports.LiveOrderEvent)

	go func() {
		defer close(out)

		for {
			select {
			case e := <-s.events:
				select {
				case out <- e:
				case <-ctx.Done():
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return out
}

func paperService(l *ledger, f *feed, now *time.Time, opts ...func(*trading.Deps)) *trading.Service {
	deps := trading.Deps{
		Ledger: l, Rules: &rules{}, Markets: markets{}, Workflows: workflows{}, Logger: observability.NoopUtility().Logger,
		Books: books{book: domain.OrderBook{
			Asks: []domain.Level{{Price: d("0.50"), Size: d("4")}},
			Bids: []domain.Level{{Price: d("0.48"), Size: d("100")}},
		}},
		PaperBalance: func() decimal.Decimal { return d("100") },
		Now:          func() time.Time { return *now },
		Feed:         f,
	}

	for _, o := range opts {
		o(&deps)
	}

	return trading.New(deps)
}

func rest(typ domain.OrderType, side domain.Side, price, shares string) domain.OrderRequest {
	req := order(side, alice, shares)
	req.Type, req.Price = typ, d(price)

	return req
}

func TestPaperRestingOrders(t *testing.T) {
	ctx := context.Background()

	t.Run("below the book it rests, its cash held (D206, D210)", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)
		assert.Equal(t, "rests on the book at 0.4, 0 of 100 shares filled", o.Reason)
		assert.Empty(t, o.Fills)
		assert.Equal(t, "100", l.accounts["wf"].Cash.String(), "held, not spent")

		s, err := svc.Summary(ctx, owner, "wf", "")
		require.NoError(t, err)
		assert.Equal(t, "40", s.Account.Held.String())

		// The rest of the cash is free; what is held is not.
		more, err := svc.Place(ctx, rest(domain.OrderFAK, domain.SideBuy, "0.50", "130"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, more.Status)
		assert.Equal(t, "not enough paper cash: $65.00 for it, $60.00 free, the rest held by resting orders", more.Reason)
	})

	t.Run("what crosses fills at once, as a taker; the rest rests", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.50", "10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)
		require.Len(t, o.Fills, 1)
		assert.Equal(t, "4", o.Fills[0].Shares.String(), "the 4 the book had")
		assert.True(t, o.Fills[0].Fee.IsPositive(), "a taker's fee")
		assert.Equal(t, "rests on the book at 0.5, 4 of 10 shares filled", o.Reason)
	})

	t.Run("post-only: refused if it would match at once", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		crossing := rest(domain.OrderGTC, domain.SideBuy, "0.50", "10")
		crossing.PostOnly = true

		o, err := svc.Place(ctx, crossing)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status)
		assert.Equal(t, "post-only: it would match the book at once", o.Reason)

		below := rest(domain.OrderGTC, domain.SideBuy, "0.45", "10")
		below.PostOnly = true

		o, err = svc.Place(ctx, below)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)
	})

	t.Run("a buy of dollars rests as shares at its price", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		req := rest(domain.OrderGTC, domain.SideBuy, "0.30", "0")
		req.Shares, req.Dollars = decimal.Zero, d("10")

		o, err := svc.Place(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)
		assert.Equal(t, "33.33", o.Shares.String())
		assert.True(t, o.Dollars.IsZero())
	})

	t.Run("a GTD expiring within 3 minutes is refused", func(t *testing.T) {
		now := at
		svc := paperService(newLedger(), &feed{}, &now)

		req := rest(domain.OrderGTD, domain.SideBuy, "0.40", "10")
		req.Expires = at.Add(2 * time.Minute)

		o, err := svc.Place(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status)
		assert.Contains(t, o.Reason, "3 minutes from now at the earliest")
	})

	t.Run("resting sells hold back their shares", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)
		l.accounts["wf"] = domain.PaperAccount{WorkflowID: "wf", OwnerID: "alice", Cash: d("100"), StartedWith: d("100")}
		key := domain.HoldingKey{WorkflowID: "wf", Mode: domain.ModePaper, Trader: alice, TokenID: "t-yes"}
		l.holdings[key] = domain.Holding{WorkflowID: "wf", Mode: domain.ModePaper, Trader: alice, TokenID: "t-yes", Shares: d("10"), Cost: d("4")}

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideSell, "0.60", "8"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)

		o, err = svc.Place(ctx, rest(domain.OrderFAK, domain.SideSell, "0.45", "5"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status)
		assert.Equal(t, "not enough shares: 5 to sell, 2 held copying label, 8 more held back by resting sells", o.Reason)
	})
}

func TestPaperRestingOrdersFillAndEnd(t *testing.T) {
	ctx := context.Background()

	run := func(t *testing.T, l *ledger, f *feed, now *time.Time) *trading.Service {
		t.Helper()

		svc := paperService(l, f, now)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})

		go func() {
			defer close(done)
			svc.RestEvery(ctx, time.Millisecond)
		}()

		t.Cleanup(func() {
			cancel()
			<-done
		})

		return svc
	}

	t.Run("trades through its price fill it, at its price, as a maker (D209)", func(t *testing.T) {
		now := at
		l, f := newLedger(), &feed{}
		svc := run(t, l, f, &now)

		cheap, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		eager, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.42", "10"))
		require.NoError(t, err)

		f.trade(t, "0.40", "50") // at its price: its place in the queue is unknown
		f.trade(t, "0.41", "12") // through 0.42 only

		require.Eventually(t, func() bool {
			o, _ := l.Order(ctx, eager.ID)
			return o.Status == domain.OrderFilled
		}, time.Second, time.Millisecond)

		o, err := l.Order(ctx, eager.ID)
		require.NoError(t, err)
		require.Len(t, o.Fills, 1)
		assert.Equal(t, "0.42", o.Fills[0].Price.String(), "its own price")
		assert.True(t, o.Fills[0].Fee.IsZero(), "a maker pays no fee")

		c, err := l.Order(ctx, cheap.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, c.Status, "a trade at 0.40 and one at 0.41 do not go through 0.40")
		assert.Empty(t, c.Fills)

		assert.Equal(t, "95.8", l.accounts["wf"].Cash.String(), "10 at 0.42 spent")
		assert.Equal(t, "10", l.holdings[domain.HoldingKey{WorkflowID: "wf", Mode: domain.ModePaper, Trader: alice, TokenID: "t-yes"}].Shares.String())

		f.trade(t, "0.39", "4")

		require.Eventually(t, func() bool {
			o, _ := l.Order(ctx, cheap.ID)
			return len(o.Fills) == 1
		}, time.Second, time.Millisecond)

		c, err = l.Order(ctx, cheap.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, c.Status)
		assert.Equal(t, "rests on the book at 0.4, 4 of 10 shares filled", c.Reason)
	})

	t.Run("a GTD expires at its time, keeping what it filled", func(t *testing.T) {
		now := at
		l, f := newLedger(), &feed{}
		svc := paperService(l, f, &now)

		req := rest(domain.OrderGTD, domain.SideBuy, "0.40", "10")
		req.Expires = at.Add(time.Hour)

		o, err := svc.Place(ctx, req)
		require.NoError(t, err)
		require.Equal(t, domain.OrderOpen, o.Status)

		now = at.Add(time.Hour)
		run(t, l, f, &now)

		require.Eventually(t, func() bool {
			got, _ := l.Order(ctx, o.ID)
			return got.Status == domain.OrderExpired
		}, time.Second, time.Millisecond)
	})

	t.Run("cancelled by its owner, its cash free again", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"))
		require.NoError(t, err)

		_, err = svc.Cancel(ctx, auth.Principal{User: domain.User{ID: "bob"}}, "wf", o.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		got, err := svc.Cancel(ctx, owner, "wf", o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
		assert.Equal(t, "cancelled by its owner, 0 of 100 shares filled", got.Reason)

		s, err := svc.Summary(ctx, owner, "wf", "")
		require.NoError(t, err)
		assert.True(t, s.Account.Held.IsZero())

		_, err = svc.Cancel(ctx, owner, "wf", o.ID)
		require.ErrorIs(t, err, domain.ErrNotOpen)
	})

	t.Run("a reset cancels them, with the cash they were of", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"))
		require.NoError(t, err)

		_, err = svc.Reset(ctx, owner, "wf")
		require.NoError(t, err)

		got, err := l.Order(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
		assert.Contains(t, got.Reason, "the paper account was reset")
	})

	t.Run("its market resolved: cancelled", func(t *testing.T) {
		now := at
		l := newLedger()
		svc := paperService(l, &feed{}, &now)

		o, err := svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"))
		require.NoError(t, err)

		resolved := trading.New(trading.Deps{
			Ledger: l, Rules: &rules{}, Markets: markets{resolved: true}, Workflows: workflows{},
			Logger: observability.NoopUtility().Logger, Books: books{}, PaperBalance: func() decimal.Decimal { return d("100") },
			Now: func() time.Time { return now },
		})
		require.NoError(t, resolved.Settle(ctx))

		got, err := l.Order(ctx, o.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderCanceled, got.Status)
		assert.Contains(t, got.Reason, "its market resolved")
	})
}

func TestLiveRestingOrders(t *testing.T) {
	ctx := context.Background()

	t.Run("placed resting: post-only and its expiry sent, open", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		req := rest(domain.OrderGTD, domain.SideBuy, "0.40", "10")
		req.PostOnly, req.Expires = true, at.Add(time.Hour)

		o, err := l.svc.Place(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)
		assert.Equal(t, "rests on the book at 0.4, 0 of 10 shares filled", o.Reason)

		require.Len(t, l.exchange.sent, 1)
		assert.True(t, l.exchange.sent[0].PostOnly)
		assert.Equal(t, at.Add(time.Hour), l.exchange.sent[0].Expires)
		assert.Equal(t, domain.OrderGTD, l.exchange.sent[0].Type)
	})

	t.Run("matched in part at once, as a taker; the rest rests", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting, TradeIDs: []string{"trade-1"}}
		l.exchange.setTrade(matched("trade-1", "4"))

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.50", "10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, o.Status)
		require.Len(t, o.Fills, 1)
		assert.True(t, o.Fills[0].Fee.IsPositive())
		assert.Equal(t, "4", l.ledger.holdings[liveKey].Shares.String())
	})

	t.Run("the user channel says it filled: read again, a maker's fill, no fee (D195)", func(t *testing.T) {
		events := make(chan ports.LiveOrderEvent, 4)
		l := liveService(t, func(d *trading.Deps) { d.Stream = stream{events: events} })
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"))
		require.NoError(t, err)
		require.Equal(t, domain.OrderOpen, o.Status)

		wctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		go func() {
			defer close(done)
			l.svc.WatchLive(wctx, time.Hour)
		}()

		t.Cleanup(func() {
			cancel()
			<-done
		})

		maker := ports.LiveTrade{ID: "trade-9", Status: domain.FillMatched, Maker: true, Fills: []domain.OrderFill{
			{Price: d("0.40"), Shares: d("10"), Dollars: d("4"), TradeID: "trade-9"},
		}}
		l.exchange.setTrade(maker)
		l.exchange.mu.Lock()
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveMatched, Matched: d("10"), TradeIDs: []string{"trade-9"}}}
		l.exchange.lookups = 0
		l.exchange.mu.Unlock()

		events <- ports.LiveOrderEvent{Orders: []string{"0xabc"}}

		require.Eventually(t, func() bool {
			got, _ := l.ledger.Order(ctx, o.ID)
			return got.Status == domain.OrderFilled
		}, time.Second, time.Millisecond)

		got, err := l.ledger.Order(ctx, o.ID)
		require.NoError(t, err)
		require.Len(t, got.Fills, 1)
		assert.True(t, got.Fills[0].Fee.IsZero(), "a maker pays no fee")
		assert.Equal(t, "10", l.ledger.holdings[liveKey].Shares.String())
	})

	t.Run("cancelled by its owner, keeping what it filled", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting, TradeIDs: []string{"trade-1"}}
		l.exchange.setTrade(matched("trade-1", "4"))

		o, err := l.svc.Place(ctx, rest(domain.OrderGTC, domain.SideBuy, "0.50", "10"))
		require.NoError(t, err)

		l.exchange.cancel = ports.LiveCancel{Canceled: true}
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveDone, Matched: d("4"), TradeIDs: []string{"trade-1"}}}

		got, err := l.svc.Cancel(ctx, owner, "wf", o.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"0xabc"}, l.exchange.cancelled)
		assert.Equal(t, domain.OrderCanceled, got.Status)
		assert.Equal(t, "cancelled by its owner, 4 of 10 shares filled", got.Reason, "why it was asked, not the CLOB's word")
		require.Len(t, got.Fills, 1)
	})

	t.Run("a GTD done after its time expired", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveResting}

		req := rest(domain.OrderGTD, domain.SideBuy, "0.40", "10")
		req.Expires = at.Add(time.Hour)

		o, err := l.svc.Place(ctx, req)
		require.NoError(t, err)

		*l.now = at.Add(time.Hour)
		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveDone}}

		wctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})

		go func() {
			defer close(done)
			l.svc.WatchLive(wctx, time.Hour)
		}()

		require.Eventually(t, func() bool {
			got, _ := l.ledger.Order(ctx, o.ID)
			return got.Status == domain.OrderExpired
		}, time.Second, time.Millisecond)

		cancel()
		<-done
	})
}
