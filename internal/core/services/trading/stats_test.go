package trading_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// countedBooks counts the times it is asked, and fails while fail is set.
type countedBooks struct {
	books

	asked *atomic.Int32
	fail  bool
}

func (b countedBooks) OrderBooks(ctx context.Context, tokens ...domain.TokenID) ([]domain.OrderBook, error) {
	b.asked.Add(1)

	if b.fail {
		return nil, errors.New("upstream service unavailable")
	}

	return b.books.OrderBooks(ctx, tokens...)
}

// statsService is resultsService with books counted, and a clock that stays put unless moved.
func statsService(l *ledger, b countedBooks, now *time.Time, repo *tradingRepo) *trading.Service {
	deps := trading.Deps{
		Ledger: l, Rules: &rules{}, Markets: markets{}, Workflows: workflows{}, Logger: observability.NoopUtility().Logger,
		Books:        b,
		PaperBalance: func() decimal.Decimal { return d("100") },
		Now:          func() time.Time { return *now },
	}
	if repo != nil {
		deps.Trading = repo
	}

	return trading.New(deps)
}

func TestResults(t *testing.T) {
	ctx := context.Background()

	t.Run("as the trading page has it, without asking for cash", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))

		now := at
		svc := statsService(l, countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}, &now, nil)

		summary, err := svc.Summary(ctx, owner, "wf", "")
		require.NoError(t, err)

		all, err := svc.Results(ctx, "alice")
		require.NoError(t, err)
		require.Len(t, all.Workflows, 1)

		r := all.Workflows["wf"]
		assert.Equal(t, domain.ModePaper, r.Mode)
		assert.Equal(t, summary.Account.Profit.String(), r.Profit.String(), "the same profit as its page")
		assert.Equal(t, "-0.79984", r.Profit.String())
		assert.Equal(t, "-0.39984", r.Realized.String(), "Alice's 10 sold")
		assert.Equal(t, "-0.4", r.Unrealized.String(), "20 held at the 0.49 midpoint, which cost 10.20")
		assert.Equal(t, 2, r.Positions)
		assert.Equal(t, 0, r.Unpriced)
		assert.Equal(t, 3, r.Orders)
		assert.Equal(t, "19.8", r.Volume.String())

		// Each trader as its page's Traders has them, the most profitable first.
		require.Len(t, all.Traders, 2)

		page := map[domain.WalletAddress]trading.TraderSummary{}
		for _, tr := range summary.Traders {
			page[tr.Trader] = tr
		}

		for _, tr := range all.Traders {
			want := page[tr.Trader]
			assert.Equal(t, want.Realized.String(), tr.Realized.String(), tr.Label)
			assert.Equal(t, want.Unrealized.String(), tr.Unrealized.String(), tr.Label)
			assert.Equal(t, want.Copies, tr.Orders, tr.Label)
			assert.Equal(t, want.Volume.String(), tr.Volume.String(), tr.Label)
			assert.Equal(t, 1, tr.Workflows)
			assert.Equal(t, domain.ModePaper, tr.Mode)
		}

		assert.Equal(t, []string{"Bob", "Alice"}, []string{all.Traders[0].Label, all.Traders[1].Label}, "Bob lost less")
		assert.Equal(t, "-0.59984", all.Traders[1].Profit.String())
	})

	t.Run("a trader copied by two workflows, summed across them", func(t *testing.T) {
		l := newLedger()
		svc := resultsService(l, markets{}, twoSided)
		copies(t, svc)

		_, err := svc.Place(ctx, domain.OrderRequest{
			WorkflowID: "wf2", OwnerID: "alice", NodeID: "copy", Mode: domain.ModePaper, Trader: alice, TraderLabel: "Alice",
			TokenID: "t-yes", Type: domain.OrderFAK, Side: domain.SideBuy, Price: d("0.50"), Shares: d("10"),
		})
		require.NoError(t, err)

		now := at
		all, err := statsService(l, countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}, &now, nil).Results(ctx, "alice")
		require.NoError(t, err)
		require.Len(t, all.Workflows, 2)

		var found bool

		for _, tr := range all.Traders {
			if tr.Trader == alice {
				found = true

				assert.Equal(t, 2, tr.Workflows)
				assert.Equal(t, 3, tr.Orders, "two in wf, one in wf2")
				assert.Equal(t, 2, tr.Positions, "one holding in each")
			}
		}

		assert.True(t, found)
	})

	t.Run("counting from the account's start, then from its reset", func(t *testing.T) {
		l := newLedger()
		svc := resultsService(l, markets{}, twoSided)
		copies(t, svc)

		now := at
		stats := statsService(l, countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}, &now, nil)

		before := results(t, stats)["wf"]
		assert.Equal(t, l.accounts["wf"].StartedAt, before.Since)

		a, err := svc.Reset(ctx, owner, "wf")
		require.NoError(t, err)

		after := results(t, stats)["wf"]
		assert.Equal(t, a.ResetAt, after.Since, "a new start, its extremes with it")
		assert.Equal(t, 0, after.Positions)
	})

	t.Run("one that never traded has none, nor anyone else's", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))

		now := at
		svc := statsService(l, countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}, &now, nil)

		none, err := svc.Results(ctx, "bob")
		require.NoError(t, err)
		assert.Empty(t, none.Workflows)
		assert.Empty(t, none.Traders)
	})

	t.Run("settled: closed, and won", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))
		require.NoError(t, resultsService(l, markets{resolved: true}, twoSided).Settle(ctx))

		now := at
		r := results(t, statsService(l, countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}, &now, nil))["wf"]

		assert.Equal(t, 0, r.Positions)
		assert.Equal(t, 2, r.Closed)
		assert.Equal(t, 2, r.Wins, "both paid $1 a share, more than they cost")
		assert.Equal(t, 3, r.Orders, "settlements are no orders")
		assert.Equal(t, "9.40016", r.Profit.String())
	})

	t.Run("prices kept a minute", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))

		now := at
		b := countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}
		svc := statsService(l, b, &now, nil)

		results(t, svc)
		now = now.Add(59 * time.Second)
		results(t, svc)
		assert.Equal(t, int32(1), b.asked.Load(), "the books asked once")

		now = now.Add(time.Second)
		results(t, svc)
		assert.Equal(t, int32(2), b.asked.Load(), "a minute on, again")
	})

	t.Run("no prices: at their cost, and counted so", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))

		now := at
		b := countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}, fail: true}
		r := results(t, statsService(l, b, &now, nil))["wf"]

		assert.Equal(t, 2, r.Positions)
		assert.Equal(t, 2, r.Unpriced)
		assert.Equal(t, "0", r.Unrealized.String())
		assert.Equal(t, "-0.39984", r.Profit.String(), "what was sold, the rest at cost")
	})

	t.Run("live: what its holdings made, in the mode it trades in now", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))

		l.holdings[domain.HoldingKey{WorkflowID: "wf", Mode: domain.ModeLive, Trader: alice, TokenID: "t-yes"}] = domain.Holding{
			WorkflowID: "wf", OwnerID: "alice", Mode: domain.ModeLive, Trader: alice, TokenID: "t-yes", MarketID: "0xm",
			Shares: d("10"), Cost: d("4"), Realized: d("1.5"),
		}

		repo := &tradingRepo{m: map[domain.WorkflowID]domain.WorkflowTrading{"wf": {WorkflowID: "wf", Mode: domain.ModeLive}}}
		now := at
		r := results(t, statsService(l, countedBooks{books: books{book: twoSided}, asked: &atomic.Int32{}}, &now, repo))["wf"]

		assert.Equal(t, domain.ModeLive, r.Mode, "its paper history aside")
		assert.Equal(t, 1, r.Positions)
		assert.Equal(t, "1.5", r.Realized.String())
		assert.Equal(t, "0.9", r.Unrealized.String(), "10 at 0.49, which cost 4")
		assert.Equal(t, "2.4", r.Profit.String())
		assert.Equal(t, 0, r.Orders, "no live orders in the ledger")
	})
}

func results(t *testing.T, svc *trading.Service) map[domain.WorkflowID]trading.Result {
	t.Helper()

	all, err := svc.Results(context.Background(), "alice")
	require.NoError(t, err)

	return all.Workflows
}
