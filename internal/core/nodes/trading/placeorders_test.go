package trading_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// batcher is the trading service placing batches: what it was asked, a call each, and
// for each order the status its place gives, or an error.
type batcher struct {
	asked    [][]domain.OrderRequest
	statuses []domain.OrderStatus
	errs     map[int]error
	err      error
}

func (b *batcher) PlaceBatch(_ context.Context, reqs []domain.OrderRequest) ([]ports.PlaceResult, error) {
	b.asked = append(b.asked, reqs)

	if b.err != nil {
		return nil, b.err
	}

	out := make([]ports.PlaceResult, len(reqs))
	for i, req := range reqs {
		if err := b.errs[i]; err != nil {
			out[i].Err = err
			continue
		}

		req.Mode = domain.ModeLive
		o := domain.PlacedOrder{OrderRequest: req, ID: domain.OrderID("o-" + string(rune('a'+i))), Status: b.statuses[i]}

		if o.Status == domain.OrderFilled {
			o.Fills = []domain.OrderFill{{Price: req.Price, Shares: req.Shares, Dollars: req.Shares.Mul(req.Price)}}
		}

		out[i].Order = o
	}

	return out, nil
}

func placeAll(t *testing.T, b *batcher, params map[string]any, items ...engine.Item) engine.Ports {
	t.Helper()

	n := trading.NewPlaceOrders(b)

	out, err := n.Run(context.Background(), engine.RunContext{
		ExecutionID: "ex-1", WorkflowID: "wf", OwnerID: "u", NodeID: "basket", Params: engine.LiteralParams(n.Type(), params),
	}, engine.Ports{engine.DefaultPort: items})
	require.NoError(t, err)
	require.NoError(t, shape.Check(n.Type(), out), "the fields it adds, as declared")

	return out
}

func row(token, side, price, shares, typ string) map[string]any {
	return map[string]any{"tokenId": token, "side": side, "price": d(price), "shares": d(shares), "type": typ}
}

func TestPlaceOrders(t *testing.T) {
	basket := []any{
		row("t-yes", "buy", "0.5", "10", "FAK"),
		row("t-no", "buy", "0.3", "10", "GTD"),
		row("t-maybe", "sell", "0.2", "5", "FAK"),
	}

	t.Run("one batch for the item's orders, an item out per order (D244, D248)", func(t *testing.T) {
		b := &batcher{statuses: []domain.OrderStatus{domain.OrderFilled, domain.OrderOpen, domain.OrderRefused}}
		item := tradeItem(domain.SideBuy, "0.5", "100", "200", nil)

		out := placeAll(t, b, map[string]any{"orders": basket, "expiresAfter": "2h", "postOnly": true}, item)

		require.Len(t, b.asked, 1, "together")
		require.Len(t, b.asked[0], 3)
		assert.Equal(t, domain.OrderRequest{
			WorkflowID: "wf", OwnerID: "u", ExecutionID: "ex-1", NodeID: "basket", Trader: alice, TraderLabel: "Alice",
			TokenID: "t-yes", Side: domain.SideBuy, Type: domain.OrderFAK, Price: d("0.5"), Shares: d("10"),
		}, b.asked[0][0], "as the item's trader's")
		assert.True(t, b.asked[0][1].PostOnly)
		assert.WithinDuration(t, time.Now().Add(2*time.Hour), b.asked[0][1].Expires, time.Minute)
		assert.False(t, b.asked[0][0].PostOnly, "only resting ones are post-only")

		require.Len(t, out[engine.DefaultPort], 1)
		require.Len(t, out[trading.PortResting], 1)
		require.Len(t, out[trading.PortMissed], 1)

		filled := out[engine.DefaultPort][0].JSON["order"].(map[string]any)
		assert.Equal(t, 0, filled["index"])
		assert.Equal(t, "t-yes", filled["tokenId"])
		assert.Equal(t, "filled", filled["status"])
		assert.Equal(t, 1, out[trading.PortResting][0].JSON["order"].(map[string]any)["index"])
		assert.Equal(t, "sell", out[trading.PortMissed][0].JSON["order"].(map[string]any)["side"])
		assert.Equal(t, "taker", out[engine.DefaultPort][0].JSON["role"], "the item's fields pass on")
	})

	t.Run("an order in trouble fails on its own, the rest go (D245)", func(t *testing.T) {
		b := &batcher{statuses: []domain.OrderStatus{domain.OrderFilled, domain.OrderFilled}, errs: map[int]error{1: errors.New("the CLOB is down")}}
		bad := row("t-x", "buy", "0.5", "10", "FAK")
		bad["price"] = "a lot"

		out := placeAll(t, b, map[string]any{"orders": []any{basket[0], bad, basket[2]}}, tradeItem(domain.SideBuy, "0.5", "100", "200", nil))

		require.Len(t, b.asked[0], 2, "the bad row not asked")
		require.Len(t, out[engine.DefaultPort], 1)
		require.Len(t, out[engine.ErrorPort], 2)
		assert.Contains(t, out[engine.ErrorPort][0].Err.Error(), "order 1: its price")
		assert.Contains(t, out[engine.ErrorPort][1].Err.Error(), "order 2: the CLOB is down")
	})

	t.Run("no orders, or no batch at all, fails the item", func(t *testing.T) {
		out := placeAll(t, &batcher{}, map[string]any{"orders": []any{}}, tradeItem(domain.SideBuy, "0.5", "100", "200", nil))
		require.Len(t, out[engine.ErrorPort], 1)

		out = placeAll(t, &batcher{err: errors.New("a batch is one workflow's orders")}, map[string]any{"orders": basket},
			tradeItem(domain.SideBuy, "0.5", "100", "200", nil))
		require.Len(t, out[engine.ErrorPort], 1)
	})
}
