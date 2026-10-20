package trading_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// canceller cancels what it is asked, but o-matched, which matched already.
type canceller struct{ asked []ports.CancelRequest }

func (c *canceller) CancelOrders(_ context.Context, r ports.CancelRequest) ([]ports.CancelResult, error) {
	c.asked = append(c.asked, r)

	switch {
	case r.OrderID == "o-matched":
		return []ports.CancelResult{{Order: domain.PlacedOrder{ID: r.OrderID}, Reason: "Order already matched"}}, nil
	case r.OrderID != "":
		return []ports.CancelResult{{Order: domain.PlacedOrder{
			ID: r.OrderID, Status: domain.OrderCanceled, OrderRequest: domain.OrderRequest{Mode: domain.ModeLive},
			Fills: []domain.OrderFill{{Price: d("0.4"), Shares: d("4"), Dollars: d("1.6"), Fee: d("0")}},
		}}}, nil
	default:
		return nil, nil
	}
}

func cancelled(t *testing.T, c *canceller, params map[string]any, items ...engine.Item) engine.Ports {
	t.Helper()

	n := trading.NewCancelOrders(c)

	out, err := n.Run(context.Background(), engine.RunContext{
		WorkflowID: "wf", OwnerID: "u", NodeID: "cancel", Params: engine.LiteralParams(n.Type(), params),
	}, engine.Ports{engine.DefaultPort: items})
	require.NoError(t, err)
	require.NoError(t, shape.Check(n.Type(), out), "the fields it adds, as declared")

	return out
}

func withOrder(id string) engine.Item {
	return engine.Item{JSON: map[string]any{"order": map[string]any{"id": id, "status": "open"}}}
}

func TestCancelOrdersNode(t *testing.T) {
	t.Run("the item's order, the workflow's own (D207)", func(t *testing.T) {
		c := &canceller{}
		out := cancelled(t, c, nil, withOrder("o-1"))

		require.Len(t, c.asked, 1)
		assert.Equal(t, ports.CancelRequest{WorkflowID: "wf", OwnerID: "u", OrderID: "o-1"}, c.asked[0])

		require.Len(t, out[engine.DefaultPort], 1)
		got := out[engine.DefaultPort][0].JSON["cancel"].(map[string]any)
		require.Len(t, got["cancelled"], 1)
		assert.Equal(t, "4", got["cancelled"].([]any)[0].(map[string]any)["shares"].(interface{ String() string }).String())
	})

	t.Run("not cancelled: out of missed, saying why", func(t *testing.T) {
		out := cancelled(t, &canceller{}, nil, withOrder("o-matched"))

		require.Len(t, out[trading.PortMissed], 1)
		got := out[trading.PortMissed][0].JSON["cancel"].(map[string]any)
		assert.Equal(t, "Order already matched", got["notCancelled"].([]any)[0].(map[string]any)["reason"])
	})

	t.Run("nothing resting there: missed", func(t *testing.T) {
		c := &canceller{}
		out := cancelled(t, c, map[string]any{"which": "market", "marketId": "0xm"}, engine.Item{JSON: map[string]any{}})

		assert.Equal(t, ports.CancelRequest{WorkflowID: "wf", OwnerID: "u", MarketID: "0xm"}, c.asked[0])
		require.Len(t, out[trading.PortMissed], 1)
	})

	t.Run("all, or an outcome's", func(t *testing.T) {
		c := &canceller{}
		cancelled(t, c, map[string]any{"which": "all"}, engine.Item{JSON: map[string]any{}})
		cancelled(t, c, map[string]any{"which": "outcome", "tokenId": "t-yes"}, engine.Item{JSON: map[string]any{}})

		assert.True(t, c.asked[0].All)
		assert.Equal(t, domain.TokenID("t-yes"), c.asked[1].TokenID)
	})

	t.Run("no order on the item fails it", func(t *testing.T) {
		out := cancelled(t, &canceller{}, nil, engine.Item{JSON: map[string]any{"question": "Will it?"}})
		require.Len(t, out[engine.ErrorPort], 1)
	})
}
