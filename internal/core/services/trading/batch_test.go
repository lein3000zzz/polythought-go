package trading_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func TestPlaceBatch(t *testing.T) {
	ctx := context.Background()

	t.Run("on paper, one after another, each seeing the cash the one before left", func(t *testing.T) {
		now := at
		svc := paperService(newLedger(), &feed{}, &now)

		got, err := svc.PlaceBatch(ctx, []domain.OrderRequest{
			rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"), // $40 of the $100
			rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"), // $40 more
			rest(domain.OrderGTC, domain.SideBuy, "0.40", "100"), // too much now
		})
		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, domain.OrderOpen, got[0].Order.Status)
		assert.Equal(t, domain.OrderOpen, got[1].Order.Status)
		assert.Equal(t, domain.OrderRefused, got[2].Order.Status)
		assert.Contains(t, got[2].Order.Reason, "not enough paper cash")
	})

	t.Run("live, those that pass go together, each recorded before (D244, D245, D233)", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{PerDay: dollars(10)})
		l.exchange.batched = []ports.LivePlacement{
			{State: ports.LiveResting}, // the first rests
			{State: ports.LiveDone},    // the third: a FAK the book could not fill
		}

		fak := liveBuy("10") // $5
		fak.Price = d("0.5")

		got, err := l.svc.PlaceBatch(ctx, []domain.OrderRequest{
			rest(domain.OrderGTC, domain.SideBuy, "0.40", "10"), // $4
			liveBuy("20"), // $10: over the day's cap with the first
			fak,           // $5: within it
		})
		require.NoError(t, err)
		require.Len(t, got, 3)

		assert.Equal(t, domain.OrderOpen, got[0].Order.Status)
		assert.Equal(t, "0xb0", got[0].Order.ClobID)
		assert.Equal(t, domain.OrderRefused, got[1].Order.Status)
		assert.Contains(t, got[1].Order.Reason, "$4.00 spent or held in the last 24 hours, $6.00 left", "the first counts against it")
		assert.Equal(t, domain.OrderUnfilled, got[2].Order.Status)

		require.Len(t, l.exchange.batches, 1, "one request")
		assert.Len(t, l.exchange.batches[0], 2, "the refused one not in it")
		assert.Zero(t, l.exchange.sentCount(), "nothing sent one by one")

		for _, o := range l.ledger.all() {
			assert.NotEqual(t, domain.OrderPending, o.Status, "%s settled", o.ID)
		}
	})

	t.Run("live sells cannot sell together more than is held", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})
		l.exchange.placed = ports.LivePlacement{State: ports.LiveResting}
		l.ledger.holdings[liveKey] = domain.Holding{WorkflowID: "wf", Mode: domain.ModeLive, Trader: alice, TokenID: "t-yes", Shares: d("10")}

		sell := rest(domain.OrderGTC, domain.SideSell, "0.60", "6")

		got, err := l.svc.PlaceBatch(ctx, []domain.OrderRequest{sell, sell})
		require.NoError(t, err)
		assert.Equal(t, domain.OrderOpen, got[0].Order.Status)
		assert.Equal(t, domain.OrderRefused, got[1].Order.Status)
		assert.Contains(t, got[1].Order.Reason, "not enough shares: 6 to sell, 4 held")
	})

	t.Run("not recorded first, none is sent", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})
		l.ledger.failRecord = errors.New("the database is down")

		got, err := l.svc.PlaceBatch(ctx, []domain.OrderRequest{liveBuy("10"), liveBuy("10")})
		require.NoError(t, err)

		for _, p := range got {
			require.ErrorContains(t, p.Err, "not sent, as it could not be recorded first")
		}

		assert.Empty(t, l.exchange.batches)
	})

	t.Run("one workflow's orders only", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})

		other := liveBuy("10")
		other.WorkflowID = "wf-2"

		_, err := l.svc.PlaceBatch(ctx, []domain.OrderRequest{liveBuy("10"), other})
		require.Error(t, err)
	})

	t.Run("matched ones are awaited together and recorded in turn, in one holding", func(t *testing.T) {
		l, _ := safeService(t, domain.LiveCaps{})
		l.exchange.batched = []ports.LivePlacement{
			{State: ports.LiveMatched, TradeIDs: []string{"trade-1"}},
			{State: ports.LiveMatched, TradeIDs: []string{"trade-2"}},
		}
		l.exchange.setTrade(matched("trade-1", "10"))
		l.exchange.setTrade(matched("trade-2", "10"))

		started := time.Now()
		got, err := l.svc.PlaceBatch(ctx, []domain.OrderRequest{liveBuy("10"), liveBuy("10")})
		require.NoError(t, err)
		assert.Less(t, time.Since(started), time.Second)

		for _, p := range got {
			require.NoError(t, p.Err)
			assert.Equal(t, domain.OrderFilled, p.Order.Status)
		}

		held := l.ledger.holdings[liveKey]
		assert.Equal(t, "20", held.Shares.String(), "both fills in the holding, neither lost")
	})
}
