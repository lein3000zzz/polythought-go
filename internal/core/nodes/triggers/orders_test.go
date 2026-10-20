package triggers_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
)

// events is a workflow's order events, the test's to send.
type events struct{ ch chan domain.OrderEvent }

func (e events) OrderEvents(ctx context.Context, w domain.WorkflowID) <-chan domain.OrderEvent {
	if w != "wf" {
		return nil
	}

	return e.ch
}

func TestOrderActivity(t *testing.T) {
	d := decimal.RequireFromString
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	src := events{ch: make(chan domain.OrderEvent, 4)}
	n := triggers.NewOrderActivity(src)

	order := domain.PlacedOrder{
		OrderRequest: domain.OrderRequest{
			WorkflowID: "wf", Mode: domain.ModePaper, TokenID: "t-yes", MarketID: "0xm", Side: domain.SideBuy,
			Type: domain.OrderGTC, Price: d("0.4"), Shares: d("10"),
		},
		ID: "o-1", Status: domain.OrderOpen, Reason: "rests on the book at 0.4, 4 of 10 shares filled",
		Fills: []domain.OrderFill{{Price: d("0.4"), Shares: d("4"), Dollars: d("1.6"), Fee: d("0")}},
	}

	src.ch <- domain.OrderEvent{Kind: domain.OrderEventFilled, Order: order, Fills: order.Fills, At: at}
	src.ch <- domain.OrderEvent{Kind: domain.OrderEventExpired, Order: order, At: at}
	src.ch <- domain.OrderEvent{Kind: domain.OrderEventCanceled, Order: order, At: at}
	close(src.ch)

	var got []engine.Ports

	err := n.Start(context.Background(), engine.TriggerContext{
		WorkflowID: "wf", NodeID: "orders", Params: engine.Params{"events": []any{"filled", "canceled"}},
	}, func(_ context.Context, out engine.Ports) error {
		got = append(got, out)
		return nil
	})
	require.NoError(t, err)

	require.Len(t, got, 2, "expired is not asked for")
	for _, out := range got {
		require.NoError(t, shape.Check(n.Type(), out))
	}

	first := got[0][engine.DefaultPort][0].JSON
	assert.Equal(t, "filled", first["event"])
	assert.Equal(t, "paper", first["mode"])
	o := first["order"].(map[string]any)
	assert.Equal(t, "o-1", o["id"])
	assert.Equal(t, "4", o["filled"].(decimal.Decimal).String())
	assert.Len(t, first["fills"], 1)

	assert.Equal(t, "canceled", got[1][engine.DefaultPort][0].JSON["event"])
}
