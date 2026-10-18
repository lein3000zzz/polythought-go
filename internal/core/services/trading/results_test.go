package trading_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

var owner = auth.Principal{User: domain.User{ID: "alice"}}

// clock moves a minute each time it is read.
type clock struct{ t time.Time }

func (c *clock) now() time.Time {
	c.t = c.t.Add(time.Minute)
	return c.t
}

func resultsService(l *ledger, m markets, book domain.OrderBook) *trading.Service {
	c := &clock{t: at}

	return trading.New(trading.Deps{
		Ledger: l, Rules: &rules{}, Markets: m, Workflows: workflows{}, Logger: observability.NoopUtility().Logger,
		Books:        books{book: book},
		PaperBalance: func() decimal.Decimal { return d("100") },
		Now:          c.now,
	})
}

var twoSided = domain.OrderBook{
	Asks: []domain.Level{{Price: d("0.50"), Size: d("100")}},
	Bids: []domain.Level{{Price: d("0.48"), Size: d("100")}},
}

// copies buys 20 shares copying Alice and 10 copying Bob, at 0.50, then sells 10 of
// Alice's at 0.48.
func copies(t *testing.T, svc *trading.Service) {
	t.Helper()

	for _, req := range []domain.OrderRequest{
		{Trader: alice, TraderLabel: "Alice", Side: domain.SideBuy, Price: d("0.50"), Shares: d("20")},
		{Trader: bob, TraderLabel: "Bob", Side: domain.SideBuy, Price: d("0.50"), Shares: d("10")},
		{Trader: alice, TraderLabel: "Alice", Side: domain.SideSell, Price: d("0.45"), Shares: d("10")},
	} {
		req.WorkflowID, req.OwnerID, req.NodeID, req.Mode = "wf", "alice", "copy", domain.ModePaper
		req.TokenID, req.Type = "t-yes", domain.OrderFAK

		o, err := svc.Place(context.Background(), req)
		require.NoError(t, err)
		require.Equal(t, domain.OrderFilled, o.Status, o.Reason)
	}
}

func TestSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("before any order, what the account would start with", func(t *testing.T) {
		s, err := resultsService(newLedger(), markets{}, twoSided).Summary(ctx, owner, "wf")
		require.NoError(t, err)

		assert.False(t, s.Account.Open)
		assert.Equal(t, "100", s.Account.Worth.String())
		assert.Empty(t, s.Traders)
	})

	t.Run("worth, profit and each trader's copies, marked at the midpoint (D104, D105)", func(t *testing.T) {
		l := newLedger()
		svc := resultsService(l, markets{}, twoSided)
		copies(t, svc)

		s, err := svc.Summary(ctx, owner, "wf")
		require.NoError(t, err)

		assert.Equal(t, "89.40016", s.Account.Cash.String(), "100 less 10.20 and 5.10 bought, and 4.70016 back")
		assert.Equal(t, "99.20016", s.Account.Worth.String(), "and 20 shares at the 0.49 midpoint")
		assert.Equal(t, "-0.79984", s.Account.Profit.String())

		require.Len(t, s.Holdings, 2)
		a := s.Holdings[0]
		assert.Equal(t, "Alice", a.TraderLabel)
		assert.Equal(t, "Will it?", a.Question)
		assert.Equal(t, "Yes", a.Outcome)
		assert.Equal(t, "0.49", a.Mark.String())
		assert.Equal(t, "4.9", a.Value.String())
		assert.Equal(t, "-0.2", a.Unrealized().String(), "10 shares that cost 5.10")

		require.Len(t, s.Traders, 2)
		alice, bob := s.Traders[0], s.Traders[1]
		assert.Equal(t, "Alice", alice.Label)
		assert.Equal(t, 2, alice.Copies)
		assert.Equal(t, "14.8", alice.Volume.String())
		assert.Equal(t, "-0.39984", alice.Realized.String(), "4.70016 back for shares that cost 5.10")
		assert.Equal(t, "-0.2", alice.Unrealized.String())
		assert.Equal(t, 0, alice.Closed)
		assert.Equal(t, 1, bob.Copies)
		assert.Equal(t, "5", bob.Volume.String())
	})

	t.Run("a one-sided book: the market's own price for the outcome", func(t *testing.T) {
		l := newLedger()
		copies(t, resultsService(l, markets{}, twoSided))

		s, err := resultsService(l, markets{}, domain.OrderBook{Bids: twoSided.Bids}).Summary(ctx, owner, "wf")
		require.NoError(t, err)
		assert.Equal(t, "0.49", s.Holdings[0].Mark.String(), "Gamma's price for Yes")
	})

	t.Run("someone else's workflow is not there", func(t *testing.T) {
		_, err := resultsService(newLedger(), markets{}, twoSided).Summary(ctx, auth.Principal{User: domain.User{ID: "bob"}}, "wf")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestReset(t *testing.T) {
	ctx := context.Background()

	l := newLedger()
	svc := resultsService(l, markets{}, twoSided)
	copies(t, svc)

	a, err := svc.Reset(ctx, owner, "wf")
	require.NoError(t, err)
	assert.Equal(t, "100", a.Cash.String())
	assert.False(t, a.ResetAt.IsZero())

	s, err := svc.Summary(ctx, owner, "wf")
	require.NoError(t, err)
	assert.Equal(t, "100", s.Account.Worth.String())
	assert.Empty(t, s.Holdings, "emptied")
	assert.Empty(t, s.Traders, "counted from the reset (D107)")
	assert.Len(t, l.orders, 3, "the orders before stay")

	_, err = resultsService(newLedger(), markets{}, twoSided).Reset(ctx, owner, "wf")
	require.NoError(t, err, "an account not open yet just opens")
}

func TestSettle(t *testing.T) {
	ctx := context.Background()

	l := newLedger()
	copies(t, resultsService(l, markets{}, twoSided))

	require.NoError(t, resultsService(l, markets{}, twoSided).Settle(ctx))
	assert.Len(t, l.orders, 3, "open markets: nothing to settle")

	svc := resultsService(l, markets{resolved: true}, twoSided)
	require.NoError(t, svc.Settle(ctx))

	require.Len(t, l.orders, 5, "a settlement each for Alice's and Bob's holdings")
	settled := l.orders[3]
	assert.Equal(t, domain.OrderSettle, settled.Type)
	assert.Equal(t, domain.SideSell, settled.Side)
	assert.Equal(t, "Will it? resolved: Yes pays $1.00 a share", settled.Reason)

	s, err := svc.Summary(ctx, owner, "wf")
	require.NoError(t, err)
	assert.Empty(t, s.Holdings, "nothing left open")
	assert.Equal(t, "109.40016", s.Account.Cash.String(), "20 shares paying $1 each")

	alice := s.Traders[0]
	assert.Equal(t, 1, alice.Closed)
	assert.Equal(t, 1, alice.Wins, "4.90 more than her copy's shares cost, all told")
	assert.Equal(t, 2, alice.Copies, "settlements are no copies")

	require.NoError(t, svc.Settle(ctx))
	assert.Len(t, l.orders, 5, "settled once")
}
