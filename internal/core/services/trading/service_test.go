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

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ledger keeps the books in memory, as the database would.
type ledger struct {
	mu       sync.Mutex
	accounts map[domain.WorkflowID]domain.PaperAccount
	holdings map[domain.HoldingKey]domain.Holding
	orders   []domain.PlacedOrder
}

func newLedger() *ledger {
	return &ledger{accounts: map[domain.WorkflowID]domain.PaperAccount{}, holdings: map[domain.HoldingKey]domain.Holding{}}
}

func (l *ledger) PaperAccount(_ context.Context, w domain.WorkflowID) (domain.PaperAccount, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.accounts[w]
	if !ok {
		return a, domain.ErrNotFound
	}

	return a, nil
}

func (l *ledger) OpenPaperAccount(_ context.Context, a domain.PaperAccount) (domain.PaperAccount, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if have, ok := l.accounts[a.WorkflowID]; ok {
		return have, nil
	}

	l.accounts[a.WorkflowID] = a

	return a, nil
}

func (l *ledger) Holding(_ context.Context, k domain.HoldingKey) (domain.Holding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	h, ok := l.holdings[k]
	if !ok {
		return h, domain.ErrNotFound
	}

	return h, nil
}

func (l *ledger) Holdings(context.Context, domain.WorkflowID) ([]domain.Holding, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []domain.Holding
	for _, h := range l.holdings {
		out = append(out, h)
	}

	return out, nil
}

func (l *ledger) Spent(context.Context, domain.WorkflowID, domain.TradingMode, time.Time) (decimal.Decimal, error) {
	return decimal.Zero, nil
}

func (l *ledger) Record(_ context.Context, o domain.PlacedOrder, c *ports.LedgerChange) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if c != nil {
		a := l.accounts[o.WorkflowID]
		if a.Cash.Add(c.Cash).IsNegative() {
			return domain.ErrNotEnoughCash
		}

		a.Cash = a.Cash.Add(c.Cash)
		l.accounts[o.WorkflowID] = a
		l.holdings[c.Holding.Key()] = c.Holding
	}

	l.orders = append(l.orders, o)

	return nil
}

func (l *ledger) Orders(context.Context, domain.WorkflowID, ports.PageFilter) (ports.Page[domain.PlacedOrder], error) {
	return ports.Page[domain.PlacedOrder]{Items: l.orders}, nil
}

type books struct{ book domain.OrderBook }

func (b books) OrderBooks(context.Context, ...domain.TokenID) ([]domain.OrderBook, error) {
	return []domain.OrderBook{b.book}, nil
}

type rules struct{ asked []domain.MarketID }

func (r *rules) TradingRules(_ context.Context, m domain.MarketID) (domain.TradingRules, error) {
	r.asked = append(r.asked, m)

	return domain.TradingRules{
		MarketID: m, TickSize: d("0.01"), MinOrderSize: d("5"),
		Fees: domain.FeeSchedule{Rate: d("0.04"), Exponent: d("1")},
	}, nil
}

type markets struct{}

func (markets) ListMarkets(_ context.Context, f ports.MarketFilter) (ports.Page[domain.Market], error) {
	if len(f.TokenIDs) == 1 && f.TokenIDs[0] == "t-yes" {
		return ports.Page[domain.Market]{Items: []domain.Market{{ID: "0xm"}}}, nil
	}

	return ports.Page[domain.Market]{}, nil
}

func (markets) GetMarket(context.Context, domain.MarketID) (domain.Market, error) {
	return domain.Market{}, domain.ErrNotFound
}

func (markets) GetMarketBySlug(context.Context, string) (domain.Market, error) {
	return domain.Market{}, domain.ErrNotFound
}

// workflows has one workflow, alice's.
type workflows struct{}

func (workflows) Get(_ context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	if p.User.ID != "alice" || id != "wf" {
		return domain.Workflow{}, domain.ErrNotFound
	}

	return domain.Workflow{ID: id, OwnerID: "alice"}, nil
}

var at = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func newService(l *ledger, r *rules) *trading.Service {
	return trading.New(trading.Deps{
		Ledger: l, Rules: r, Markets: markets{}, Workflows: workflows{}, Logger: observability.NoopUtility().Logger,
		Books: books{book: domain.OrderBook{
			Asks: []domain.Level{{Price: d("0.50"), Size: d("100")}},
			Bids: []domain.Level{{Price: d("0.48"), Size: d("100")}},
		}},
		PaperBalance: func() decimal.Decimal { return d("100") },
		Now:          func() time.Time { return at },
	})
}

const (
	alice = domain.WalletAddress("0x5f3C00000000000000000000000000000000A1b2")
	bob   = domain.WalletAddress("0x91D000000000000000000000000000000000C7e4")
)

func order(side domain.Side, trader domain.WalletAddress, shares string) domain.OrderRequest {
	return domain.OrderRequest{
		WorkflowID: "wf", OwnerID: "alice", NodeID: "copy", Mode: domain.ModePaper, Trader: trader, TraderLabel: "label",
		TokenID: "t-yes", Side: side, Type: domain.OrderFAK, Price: d("0.45"), Shares: d(shares),
	}
}

func TestPaperOrders(t *testing.T) {
	ctx := context.Background()

	t.Run("a buy opens the paper account and the trader's holding", func(t *testing.T) {
		l, r := newLedger(), &rules{}
		svc := newService(l, r)

		req := order(domain.SideBuy, alice, "20")
		req.Price = d("0.50")

		o, err := svc.Place(ctx, req)
		require.NoError(t, err)

		assert.Equal(t, domain.OrderFilled, o.Status)
		assert.Equal(t, domain.MarketID("0xm"), o.MarketID, "found from the token")
		assert.Equal(t, []domain.MarketID{"0xm"}, r.asked)

		a := l.accounts["wf"]
		assert.Equal(t, "100", a.StartedWith.String())
		assert.Equal(t, "89.8", a.Cash.String(), "$10 and a $0.20 fee")

		h := l.holdings[domain.HoldingKey{WorkflowID: "wf", Mode: domain.ModePaper, Trader: alice, TokenID: "t-yes"}]
		assert.Equal(t, "20", h.Shares.String())
		assert.Equal(t, "10.2", h.Cost.String())
		require.Len(t, l.orders, 1)
	})

	t.Run("copies of different traders are held apart (D92)", func(t *testing.T) {
		l := newLedger()
		svc := newService(l, &rules{})

		buy := order(domain.SideBuy, alice, "20")
		buy.Price = d("0.50")
		_, err := svc.Place(ctx, buy)
		require.NoError(t, err)

		o, err := svc.Place(ctx, order(domain.SideSell, bob, "10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status, "Bob's copy holds nothing")
		assert.Contains(t, o.Reason, "not enough shares: 10 to sell, 0 held copying label")

		o, err = svc.Place(ctx, order(domain.SideSell, alice, "10"))
		require.NoError(t, err)
		assert.Equal(t, domain.OrderFilled, o.Status)
		assert.Equal(t, "94.50016", l.accounts["wf"].Cash.String(), "$4.80 back, less a fee of 10 × 0.04 × 0.48 × 0.52")
	})

	t.Run("not enough paper cash refuses the order, and says so", func(t *testing.T) {
		l := newLedger()
		svc := newService(l, &rules{})

		req := order(domain.SideBuy, "", "0")
		req.Price, req.Shares, req.Dollars = d("0.50"), decimal.Zero, d("150")

		o, err := svc.Place(ctx, req)
		require.NoError(t, err)
		assert.Equal(t, domain.OrderRefused, o.Status)
		assert.Equal(t, "not enough paper cash: $150.00 for it, $100.00 left", o.Reason,
			"all it may spend, as the CLOB checks, though the book has only $50 within its price")
		assert.Empty(t, o.Fills)
		assert.Equal(t, "100", l.accounts["wf"].Cash.String())
		assert.Len(t, l.orders, 1, "kept, with why")
	})

	t.Run("refused before the book: rules, types, live", func(t *testing.T) {
		l := newLedger()
		svc := newService(l, &rules{})

		small := order(domain.SideBuy, alice, "4")
		live := order(domain.SideBuy, alice, "10")
		live.Mode = domain.ModeLive
		rest := order(domain.SideBuy, alice, "10")
		rest.Type = domain.OrderGTC
		unknown := order(domain.SideBuy, alice, "10")
		unknown.TokenID = "t-nobody"

		for req, want := range map[*domain.OrderRequest]string{
			&small:   "under the market's least order of 5",
			&live:    "live trading is not available yet",
			&rest:    "FOK or FAK",
			&unknown: "no market trades this token",
		} {
			o, err := svc.Place(ctx, *req)
			require.NoError(t, err)
			assert.Equal(t, domain.OrderRefused, o.Status)
			assert.Contains(t, o.Reason, want)
		}

		assert.Empty(t, l.accounts, "refused before the book: no account opened")
	})

	t.Run("an order the book cannot fill is kept, unfilled", func(t *testing.T) {
		l := newLedger()

		o, err := newService(l, &rules{}).Place(ctx, order(domain.SideBuy, alice, "10")) // asks at 0.50, limit 0.45
		require.NoError(t, err)
		assert.Equal(t, domain.OrderUnfilled, o.Status)
		assert.Len(t, l.orders, 1)
	})
}

func TestBooksAreTheOwners(t *testing.T) {
	svc := newService(newLedger(), &rules{})
	ctx := context.Background()

	_, open, start, err := svc.PaperAccount(ctx, auth.Principal{User: domain.User{ID: "alice"}}, "wf")
	require.NoError(t, err)
	assert.False(t, open, "no paper order yet")
	assert.Equal(t, "100", start.String())

	_, err = svc.Orders(ctx, auth.Principal{User: domain.User{ID: "bob"}}, "wf", ports.PageFilter{})
	require.ErrorIs(t, err, domain.ErrNotFound)

	_, err = svc.Holdings(ctx, auth.Principal{User: domain.User{ID: "bob"}}, "wf")
	require.ErrorIs(t, err, domain.ErrNotFound)
}
