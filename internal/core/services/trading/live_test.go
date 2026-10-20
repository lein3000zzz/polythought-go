package trading_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
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

// exchange is Polymarket's CLOB as the live path sees it: what placing answers, how the
// order stands each time it is looked up (the last stays), its trades, the wallet's cash.
type exchange struct {
	mu      sync.Mutex
	placed  ports.LivePlacement
	err     error
	states  []ports.LiveOrderState
	trades  map[string]ports.LiveTrade
	cash    string
	sent    []ports.LiveOrder
	lookups int

	cancel    ports.LiveCancel
	cancelled []string
	beats     []string

	gate chan struct{} // when set, placing waits for it to close
}

func (e *exchange) Place(_ context.Context, k ports.OrderKey, o ports.LiveOrder) (ports.LivePlacement, error) {
	e.mu.Lock()
	e.sent = append(e.sent, o)
	gate := e.gate
	e.mu.Unlock()

	if gate != nil {
		<-gate
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.placed, e.err
}

func (e *exchange) cancelledIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()

	return slices.Clone(e.cancelled)
}

func (e *exchange) sentCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return len(e.sent)
}

func (e *exchange) Order(context.Context, ports.OrderKey, string) (ports.LiveOrderState, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.lookups++
	if len(e.states) == 0 {
		return ports.LiveOrderState{State: ports.LiveNotFound}, nil
	}

	return e.states[min(e.lookups, len(e.states))-1], nil
}

func (e *exchange) Heartbeat(_ context.Context, _ ports.OrderKey, last string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.beats = append(e.beats, last)

	return "hb-" + strconv.Itoa(len(e.beats)), nil
}

func (e *exchange) Cancel(_ context.Context, _ ports.OrderKey, id string) (ports.LiveCancel, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.cancelled = append(e.cancelled, id)

	return e.cancel, nil
}

func (e *exchange) Trades(_ context.Context, _ ports.OrderKey, _ string, _ domain.TokenID, ids []string) ([]ports.LiveTrade, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var out []ports.LiveTrade
	for _, id := range ids {
		if t, ok := e.trades[id]; ok {
			out = append(out, t)
		}
	}

	return out, nil
}

func (e *exchange) Cash(context.Context, ports.OrderKey) (decimal.Decimal, error) {
	if e.cash == "" {
		return decimal.Zero, errors.New("the CLOB is down")
	}

	return d(e.cash), nil
}

func (e *exchange) setTrade(t ports.LiveTrade) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.trades[t.ID] = t
}

// keys gives every wallet a key, unless told it cannot trade; those beating have
// heartbeats on.
type keys struct {
	err     error
	beating map[domain.WalletID]bool
}

func (k keys) OrderKey(_ context.Context, owner domain.UserID, w domain.WalletID) (ports.OrderKey, error) {
	if k.err != nil {
		return ports.OrderKey{}, k.err
	}

	return ports.OrderKey{Wallet: domain.Wallet{ID: w, UserID: owner, Heartbeat: k.beating[w]}, Key: "0xkey"}, nil
}

type tradingRepo struct {
	mu sync.Mutex
	m  map[domain.WorkflowID]domain.WorkflowTrading
}

func (r *tradingRepo) Trading(_ context.Context, w domain.WorkflowID) (domain.WorkflowTrading, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.m[w]
	if !ok {
		return t, domain.ErrNotFound
	}

	return t, nil
}

func (r *tradingRepo) SetTrading(_ context.Context, t domain.WorkflowTrading) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.m[t.WorkflowID] = t

	return nil
}

func (r *tradingRepo) TradingFrom(_ context.Context, w domain.WalletID) ([]domain.WorkflowTrading, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []domain.WorkflowTrading
	for _, t := range r.m {
		if t.WalletID == w {
			out = append(out, t)
		}
	}

	return out, nil
}

// walletDir has alice's wallets: w-1 with a key, w-2 also, w-3 without.
type walletDir struct{}

func (walletDir) Wallet(_ context.Context, owner domain.UserID, id domain.WalletID) (domain.Wallet, error) {
	w, ok := map[domain.WalletID]domain.Wallet{
		"w-1": {ID: "w-1", UserID: "alice", Label: "Main", HasCredentials: true, HasKey: true},
		"w-2": {ID: "w-2", UserID: "alice", Label: "Other", HasCredentials: true, HasSessionKey: true},
		"w-3": {ID: "w-3", UserID: "alice", Label: "Watch only"},
	}[id]
	if !ok || owner != w.UserID {
		return domain.Wallet{}, domain.ErrNotFound
	}

	return w, nil
}

func (walletDir) Credentials(context.Context, domain.UserID, domain.WalletID) (domain.PolymarketCredentials, error) {
	return domain.PolymarketCredentials{}, nil
}

type notifier struct {
	mu   sync.Mutex
	told []domain.PlacedOrder
}

func (n *notifier) LiveTradeFailed(_ context.Context, _ domain.UserID, o domain.PlacedOrder) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.told = append(n.told, o)

	return nil
}

const builder = "0x00000000000000000000000000000000000000000000000000000000000000b1"

type live struct {
	svc      *trading.Service
	ledger   *ledger
	exchange *exchange
	repo     *tradingRepo
	notifier *notifier
	on       *bool
	now      *time.Time
}

// liveService has alice's workflow wf trading live from w-1, Live trading on.
func liveService(t *testing.T, opts ...func(*trading.Deps)) live {
	t.Helper()

	on, now := true, at
	l := live{
		ledger: newLedger(), on: &on, now: &now, notifier: &notifier{},
		exchange: &exchange{trades: map[string]ports.LiveTrade{}, cash: "250"},
		repo: &tradingRepo{m: map[domain.WorkflowID]domain.WorkflowTrading{
			"wf": {WorkflowID: "wf", Mode: domain.ModeLive, WalletID: "w-1"},
		}},
	}

	deps := trading.Deps{
		Ledger: l.ledger, Rules: &rules{}, Markets: markets{}, Workflows: workflows{}, Logger: observability.NoopUtility().Logger,
		Books:        books{},
		PaperBalance: func() decimal.Decimal { return d("100") },
		Now:          func() time.Time { return *l.now },
		Trading:      l.repo, Exchange: l.exchange, Keys: keys{}, Wallets: walletDir{}, Notifier: l.notifier,
		LiveOn:      func() bool { return *l.on },
		BuilderCode: func() string { return builder },
		Patience:    3 * time.Millisecond, Poll: time.Millisecond,
	}

	for _, o := range opts {
		o(&deps)
	}

	l.svc = trading.New(deps)

	return l
}

func liveBuy(shares string) domain.OrderRequest {
	req := order(domain.SideBuy, alice, shares)
	req.Price = d("0.50")

	return req
}

// matched is a trade of 10 shares at 0.5, on its way on chain.
func matched(id, shares string) ports.LiveTrade {
	return ports.LiveTrade{ID: id, Status: domain.FillMatched, Fills: []domain.OrderFill{
		{Price: d("0.5"), Shares: d(shares), Dollars: d(shares).Mul(d("0.5")), Fee: decimal.Zero, TradeID: id},
	}}
}

var liveKey = domain.HoldingKey{WorkflowID: "wf", Mode: domain.ModeLive, Trader: alice, TokenID: "t-yes"}

func TestLiveOrders(t *testing.T) {
	ctx := context.Background()

	t.Run("matched: its trades are its fills, at their fees, counted at once (D193)", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveMatched, TradeIDs: []string{"trade-1"}}
		l.exchange.setTrade(matched("trade-1", "10"))

		o, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)

		assert.Equal(t, domain.ModeLive, o.Mode)
		assert.Equal(t, domain.OrderFilled, o.Status)
		assert.Equal(t, "0xabc", o.ClobID)
		assert.Equal(t, domain.WalletID("w-1"), o.WalletID)
		require.Len(t, o.Fills, 1)
		assert.Equal(t, "0.1", o.Fills[0].Fee.String(), "10 × 0.04 × 0.5 × 0.5, the market's taker fee")
		assert.Equal(t, domain.FillMatched, o.Fills[0].Status)
		assert.Equal(t, "trade-1", o.Fills[0].TradeID)

		h := l.ledger.holdings[liveKey]
		assert.Equal(t, "10", h.Shares.String())
		assert.Equal(t, "5.1", h.Cost.String(), "fees included")
		assert.Empty(t, l.ledger.accounts, "no paper account: the wallet's pUSD is the cash (D198)")

		require.Len(t, l.exchange.sent, 1)
		sent := l.exchange.sent[0]
		assert.Equal(t, domain.TokenID("t-yes"), sent.TokenID)
		assert.Equal(t, domain.SideBuy, sent.Side)
		assert.Equal(t, domain.OrderFAK, sent.Type)
		assert.True(t, d("0.5").Equal(sent.Price))
		assert.True(t, d("10").Equal(sent.Shares))
		assert.True(t, sent.Dollars.IsZero())
		assert.True(t, d("0.01").Equal(sent.TickSize), "the market's")
		assert.False(t, sent.NegRisk)
		assert.Equal(t, builder, sent.Builder)
	})

	t.Run("part of it", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveMatched, TradeIDs: []string{"trade-1"}}
		l.exchange.setTrade(matched("trade-1", "12"))

		o, err := l.svc.Place(ctx, liveBuy("20"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderPartial, o.Status)
	})

	t.Run("the book had nothing to match: unfilled", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveDone, Reason: "the book had nothing to match within its price"}

		o, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderUnfilled, o.Status)
		assert.Equal(t, "the book had nothing to match within its price", o.Reason)
		assert.Len(t, l.ledger.orders, 1)
	})

	t.Run("refused by Polymarket: kept, with why", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveRefused, Reason: "not enough pUSD"}

		o, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status)
		assert.Equal(t, "not enough pUSD", o.Reason)
		assert.Equal(t, "0xabc", l.ledger.orders[0].ClobID)
	})

	t.Run("delayed: waited for, then what it came to (D204)", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveDelayed}
		l.exchange.states = []ports.LiveOrderState{
			{State: ports.LiveDelayed}, {State: ports.LiveMatched, Matched: d("10"), TradeIDs: []string{"trade-1"}},
		}
		l.exchange.setTrade(matched("trade-1", "10"))

		o, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderFilled, o.Status)
		assert.Equal(t, 2, l.exchange.lookups)
	})

	t.Run("still undecided: pending, and followed until it is", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveUnknown}

		o, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderPending, o.Status)
		assert.Contains(t, o.Reason, "followed")
		assert.Empty(t, l.ledger.holdings, "nothing counted yet")

		l.exchange.states = []ports.LiveOrderState{{State: ports.LiveMatched, TradeIDs: []string{"trade-1"}}}
		l.exchange.lookups = 0
		l.exchange.setTrade(matched("trade-1", "10"))

		require.NoError(t, l.svc.Follow(ctx))
		assert.Equal(t, domain.OrderFilled, l.ledger.orders[0].Status)
		assert.Equal(t, "10", l.ledger.holdings[liveKey].Shares.String())
	})

	t.Run("never found: after a while, it never reached the book", func(t *testing.T) {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveUnknown}

		_, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)

		require.NoError(t, l.svc.Follow(ctx))
		assert.Equal(t, domain.OrderPending, l.ledger.orders[0].Status, "not yet")

		*l.now = at.Add(11 * time.Minute)
		require.NoError(t, l.svc.Follow(ctx))
		assert.Equal(t, domain.OrderRefused, l.ledger.orders[0].Status)
		assert.Equal(t, "it never reached Polymarket's book", l.ledger.orders[0].Reason)
	})

	t.Run("refused before it is sent", func(t *testing.T) {
		for name, tc := range map[string]struct {
			setup func(l live)
			opts  []func(*trading.Deps)
			req   domain.OrderRequest
			want  string
		}{
			"Live trading off": {
				setup: func(l live) { *l.on = false }, req: liveBuy("10"), want: "live trading is off on this server",
			},
			"no wallet": {
				setup: func(l live) { l.repo.m["wf"] = domain.WorkflowTrading{WorkflowID: "wf", Mode: domain.ModeLive} },
				req:   liveBuy("10"), want: "the workflow has no wallet to trade from",
			},
			"a wallet that cannot trade": {
				opts: []func(*trading.Deps){func(d *trading.Deps) { d.Keys = keys{err: domain.ErrWalletCannotTrade} }},
				req:  liveBuy("10"), want: "the wallet cannot trade",
			},
			"selling what it does not hold live": {
				setup: func(l live) {
					paper := liveKey
					paper.Mode = domain.ModePaper
					l.ledger.holdings[paper] = domain.Holding{WorkflowID: "wf", Mode: domain.ModePaper, Trader: alice, TokenID: "t-yes", Shares: d("50")}
				},
				req: order(domain.SideSell, alice, "10"), want: "not enough shares: 10 to sell, 0 held",
			},
		} {
			t.Run(name, func(t *testing.T) {
				l := liveService(t, tc.opts...)
				if tc.setup != nil {
					tc.setup(l)
				}

				o, err := l.svc.Place(ctx, tc.req)
				require.NoError(t, err)
				assert.Equal(t, domain.OrderRefused, o.Status)
				assert.Contains(t, o.Reason, tc.want)
				assert.Empty(t, l.exchange.sent, "nothing sent")
			})
		}
	})

	t.Run("not sent at all: no order, the item fails", func(t *testing.T) {
		l := liveService(t)
		l.exchange.err = domain.ErrUnavailable

		_, err := l.svc.Place(ctx, liveBuy("10"))
		require.ErrorIs(t, err, domain.ErrUnavailable)
		assert.Empty(t, l.ledger.orders)
	})
}

func TestFollowingLiveTrades(t *testing.T) {
	ctx := context.Background()

	placed := func(t *testing.T) live {
		l := liveService(t)
		l.exchange.placed = ports.LivePlacement{ID: "0xabc", State: ports.LiveMatched, TradeIDs: []string{"trade-1"}}
		l.exchange.setTrade(matched("trade-1", "10"))

		_, err := l.svc.Place(ctx, liveBuy("10"))
		require.NoError(t, err)

		return l
	}

	t.Run("confirmed: final, no longer followed", func(t *testing.T) {
		l := placed(t)

		confirmed := matched("trade-1", "10")
		confirmed.Status = domain.FillConfirmed
		l.exchange.setTrade(confirmed)

		require.NoError(t, l.svc.Follow(ctx))
		assert.Equal(t, domain.FillConfirmed, l.ledger.orders[0].Fills[0].Status)
		assert.Equal(t, domain.OrderFilled, l.ledger.orders[0].Status)
		assert.Empty(t, l.notifier.told)

		following, err := l.ledger.Following(ctx, at.Add(-time.Hour))
		require.NoError(t, err)
		assert.Empty(t, following)
	})

	t.Run("failed: undone in the books, and the owner told once", func(t *testing.T) {
		l := placed(t)

		failed := matched("trade-1", "10")
		failed.Status = domain.FillFailed
		l.exchange.setTrade(failed)

		require.NoError(t, l.svc.Follow(ctx))

		o := l.ledger.orders[0]
		assert.Equal(t, domain.OrderFailed, o.Status)
		assert.Contains(t, o.Reason, "failed on chain")
		assert.True(t, l.ledger.holdings[liveKey].Shares.IsZero(), "the shares never changed hands")
		assert.True(t, l.ledger.holdings[liveKey].Cost.IsZero())
		require.Len(t, l.notifier.told, 1)
		assert.Equal(t, o.ID, l.notifier.told[0].ID)

		require.NoError(t, l.svc.Follow(ctx))
		assert.Len(t, l.notifier.told, 1, "final: not told again")
	})

	t.Run("a failed buy taken out exactly, whatever came after", func(t *testing.T) {
		l := placed(t)

		l.exchange.placed = ports.LivePlacement{ID: "0xdef", State: ports.LiveMatched, TradeIDs: []string{"trade-2"}}
		second := matched("trade-2", "6")
		second.Status = domain.FillConfirmed
		l.exchange.setTrade(second)

		_, err := l.svc.Place(ctx, liveBuy("6"))
		require.NoError(t, err)
		assert.Equal(t, "16", l.ledger.holdings[liveKey].Shares.String())

		failed := matched("trade-1", "10")
		failed.Status = domain.FillFailed
		l.exchange.setTrade(failed)

		require.NoError(t, l.svc.Follow(ctx))

		h := l.ledger.holdings[liveKey]
		assert.Equal(t, "6", h.Shares.String(), "replayed: the second buy alone is left")
		assert.Equal(t, "3.06", h.Cost.String(), "6 at 0.5, and its fee")
	})
}

func TestLiveSetup(t *testing.T) {
	ctx := context.Background()
	owner := auth.Principal{User: domain.User{ID: "alice"}}

	paperFirst := func(t *testing.T) live {
		l := liveService(t)
		l.repo.m = map[domain.WorkflowID]domain.WorkflowTrading{}

		return l
	}

	t.Run("on paper until it says otherwise (D22)", func(t *testing.T) {
		l := paperFirst(t)

		s, err := l.svc.Setup(ctx, owner, "wf")
		require.NoError(t, err)
		assert.Equal(t, domain.ModePaper, s.Mode)
		assert.Nil(t, s.Wallet)
		assert.True(t, s.LiveAvailable)
	})

	t.Run("going live needs a wallet that can trade, and the owner's yes (D201)", func(t *testing.T) {
		l := paperFirst(t)

		_, err := l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive})
		require.ErrorIs(t, err, domain.ErrInvalidInput, "no wallet")

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-3", Confirmed: true})
		require.ErrorIs(t, err, domain.ErrWalletCannotTrade)

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-9", Confirmed: true})
		require.ErrorIs(t, err, domain.ErrNotFound, "not hers")

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1"})
		require.ErrorIs(t, err, domain.ErrNotConfirmed)

		s, err := l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true})
		require.NoError(t, err)
		assert.Equal(t, domain.ModeLive, s.Mode)
		assert.Equal(t, "Main", s.Wallet.Label)
		assert.Equal(t, domain.WorkflowTrading{WorkflowID: "wf", Mode: domain.ModeLive, WalletID: "w-1", UpdatedAt: at}, l.repo.m["wf"])

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1"})
		require.NoError(t, err, "no change: nothing to confirm")

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-2"})
		require.ErrorIs(t, err, domain.ErrNotConfirmed, "another wallet's money")
	})

	t.Run("not while Live trading is off (D192)", func(t *testing.T) {
		l := paperFirst(t)
		*l.on = false

		_, err := l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true})
		require.ErrorIs(t, err, domain.ErrLiveTradingOff)

		s, err := l.svc.Setup(ctx, owner, "wf")
		require.NoError(t, err)
		assert.False(t, s.LiveAvailable)
		assert.Contains(t, s.Why, "Live trading")
	})

	t.Run("holding live shares: back to paper any time, the wallet stays (D202)", func(t *testing.T) {
		l := liveService(t)
		l.ledger.holdings[liveKey] = domain.Holding{WorkflowID: "wf", Mode: domain.ModeLive, Trader: alice, TokenID: "t-yes", Shares: d("10")}

		s, err := l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-1"})
		require.NoError(t, err)
		assert.True(t, s.HoldsLive)

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModePaper, WalletID: "w-2"})
		require.ErrorIs(t, err, domain.ErrHoldsLive)

		_, err = l.svc.SetSetup(ctx, owner, "wf", trading.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true})
		require.NoError(t, err, "live again finds them")
	})

	t.Run("someone else's workflow", func(t *testing.T) {
		l := liveService(t)

		_, err := l.svc.Setup(ctx, auth.Principal{User: domain.User{ID: "bob"}}, "wf")
		require.ErrorIs(t, err, domain.ErrNotFound)

		_, err = l.svc.SetSetup(ctx, auth.Principal{User: domain.User{ID: "bob"}}, "wf", trading.SetupChange{Mode: domain.ModePaper})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("a wallet's cash", func(t *testing.T) {
		l := liveService(t)

		cash, err := l.svc.WalletCash(ctx, owner, "w-1")
		require.NoError(t, err)
		assert.Equal(t, "250", cash.String())
	})
}

func TestLiveBooks(t *testing.T) {
	ctx := context.Background()
	owner := auth.Principal{User: domain.User{ID: "alice"}}

	withHolding := func(t *testing.T) live {
		l := liveService(t)
		l.ledger.holdings[liveKey] = domain.Holding{
			WorkflowID: "wf", OwnerID: "alice", Mode: domain.ModeLive, Trader: alice, TraderLabel: "label", TokenID: "t-yes",
			MarketID: "0xm", Shares: d("10"), Cost: d("4"), Realized: d("1.5"),
		}

		return l
	}

	t.Run("worth for sizing: the wallet's pUSD and what the live holdings cost (D198)", func(t *testing.T) {
		l := withHolding(t)

		worth, err := l.svc.Worth(ctx, "alice", "wf")
		require.NoError(t, err)
		assert.Equal(t, "254", worth.String())

		mode, err := l.svc.Mode(ctx, "wf")
		require.NoError(t, err)
		assert.Equal(t, domain.ModeLive, mode)
	})

	t.Run("the live summary: the wallet, its cash, what the holdings made", func(t *testing.T) {
		l := withHolding(t)

		s, err := l.svc.Summary(ctx, owner, "wf", "")
		require.NoError(t, err)

		a := s.Account
		assert.Equal(t, domain.ModeLive, a.Mode)
		assert.True(t, a.CashKnown)
		assert.Equal(t, "250", a.Cash.String())
		assert.Equal(t, "Main", a.Wallet.Label)
		require.Len(t, s.Holdings, 1)
		assert.Equal(t, "254.9", a.Worth.String(), "250 and 10 shares at the midpoint, 0.49")
		assert.Equal(t, "2.4", a.Profit.String(), "1.5 realised, 0.9 open")

		paper, err := l.svc.Summary(ctx, owner, "wf", domain.ModePaper)
		require.NoError(t, err)
		assert.Equal(t, domain.ModePaper, paper.Account.Mode)
		assert.Empty(t, paper.Holdings, "the paper books are their own")
	})

	t.Run("the CLOB down: the rest still shows", func(t *testing.T) {
		l := withHolding(t)
		l.exchange.cash = ""

		s, err := l.svc.Summary(ctx, owner, "wf", "")
		require.NoError(t, err)
		assert.False(t, s.Account.CashKnown)
		assert.Len(t, s.Holdings, 1)
	})

	t.Run("resolved, a live holding settles in the books, no cash (D200)", func(t *testing.T) {
		l := withHolding(t)
		l.svc = trading.New(trading.Deps{
			Ledger: l.ledger, Rules: &rules{}, Markets: markets{resolved: true}, Workflows: workflows{},
			Logger: observability.NoopUtility().Logger, Books: books{}, PaperBalance: func() decimal.Decimal { return d("100") },
			Now: func() time.Time { return at },
		})

		require.NoError(t, l.svc.Settle(ctx))

		h := l.ledger.holdings[liveKey]
		assert.True(t, h.Shares.IsZero())
		assert.Equal(t, "7.5", h.Realized.String(), "1.5, and 10 at $1 that cost 4")
		assert.Empty(t, l.ledger.accounts, "no paper cash moved")

		settled := l.ledger.orders[len(l.ledger.orders)-1]
		assert.Equal(t, domain.OrderSettle, settled.Type)
		assert.Equal(t, domain.ModeLive, settled.Mode)
		assert.Equal(t, domain.UserID("alice"), settled.OwnerID)
		assert.Contains(t, settled.Reason, "redeemed on Polymarket")
	})
}
