package trading_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
)

// placer fills buys at 0.5, refuses sells, and keeps what it was asked.
type placer struct {
	asked []domain.OrderRequest
	err   error
}

func (p *placer) Place(_ context.Context, req domain.OrderRequest) (domain.PlacedOrder, error) {
	p.asked = append(p.asked, req)

	if p.err != nil {
		return domain.PlacedOrder{}, p.err
	}

	o := domain.PlacedOrder{OrderRequest: req, ID: "o-1", Status: domain.OrderRefused, Reason: "not enough shares"}
	if req.Side == domain.SideBuy {
		o.Status, o.Reason = domain.OrderFilled, ""
		o.Fills = []domain.OrderFill{
			{Price: d("0.5"), Shares: d("10"), Dollars: d("5"), Fee: d("0.1")},
			{Price: d("0.51"), Shares: d("9.8"), Dollars: d("4.998"), Fee: d("0.09996")},
		}
	}

	return o, nil
}

func place(t *testing.T, p *placer, params map[string]any, items ...engine.Item) engine.Ports {
	t.Helper()

	n := trading.NewPlaceOrder(p)

	out, err := n.Run(context.Background(), engine.RunContext{
		ExecutionID: "ex-1", WorkflowID: "wf", OwnerID: "u", NodeID: "place", Params: engine.LiteralParams(n.Type(), params),
	}, engine.Ports{engine.DefaultPort: items})
	require.NoError(t, err)
	require.NoError(t, shape.Check(n.Type(), out), "the fields it adds, as declared")

	return out
}

func withCopy(side domain.Side, extra map[string]any) engine.Item {
	item := tradeItem(side, "0.5", "100", "200", nil)
	item.JSON["copy"] = map[string]any{
		"side": string(side), "tokenId": "t-yes", "marketId": "0xm", "price": d("0.52"),
		"dollars": d("10"), "shares": nil, "type": "FAK",
	}

	for k, v := range extra {
		item.JSON["copy"].(map[string]any)[k] = v
	}

	return item
}

func TestPlaceOrder(t *testing.T) {
	t.Run("the copy, as the trader's (D92)", func(t *testing.T) {
		p := &placer{}
		out := place(t, p, nil, withCopy(domain.SideBuy, nil))

		require.Len(t, p.asked, 1)
		assert.Equal(t, domain.OrderRequest{
			WorkflowID: "wf", OwnerID: "u", ExecutionID: "ex-1", NodeID: "place", Mode: domain.ModePaper,
			Trader: alice, TraderLabel: "Alice", TokenID: "t-yes", MarketID: "0xm", Side: domain.SideBuy,
			Type: domain.OrderFAK, Price: d("0.52"), Dollars: d("10"),
		}, p.asked[0])

		require.Len(t, out[engine.DefaultPort], 1)
		order := out[engine.DefaultPort][0].JSON["order"].(map[string]any)
		assert.Equal(t, "filled", order["status"])
		assert.Nil(t, order["reason"])
		assert.Equal(t, "19.8", order["shares"].(decimal.Decimal).String())
		assert.Equal(t, "9.998", order["dollars"].(decimal.Decimal).String())
		assert.Equal(t, "0.19996", order["fees"].(decimal.Decimal).String())
		assert.Equal(t, "0.504949", order["price"].(decimal.Decimal).String(), "the average")
		assert.NotNil(t, out[engine.DefaultPort][0].JSON["copy"], "the item passes on")
	})

	t.Run("what did not fill goes out of missed, saying why (D96)", func(t *testing.T) {
		out := place(t, &placer{}, nil, withCopy(domain.SideSell, map[string]any{"dollars": nil, "shares": d("5")}))

		require.Len(t, out[trading.PortMissed], 1)
		order := out[trading.PortMissed][0].JSON["order"].(map[string]any)
		assert.Equal(t, "refused", order["status"])
		assert.Equal(t, "not enough shares", order["reason"])
		assert.Nil(t, order["price"], "nothing filled")
	})

	t.Run("an order of its own settings (D99)", func(t *testing.T) {
		p := &placer{}
		place(t, p, map[string]any{
			"from": "settings", "tokenId": "t-no", "side": "buy", "price": "0.3", "shares": "20", "type": "FOK",
		}, engine.Item{JSON: map[string]any{"question": "Will it?"}})

		require.Len(t, p.asked, 1)
		got := p.asked[0]
		assert.Equal(t, domain.TokenID("t-no"), got.TokenID)
		assert.Equal(t, domain.OrderFOK, got.Type)
		assert.True(t, d("20").Equal(got.Shares))
		assert.Empty(t, got.Trader, "copied from no one")
	})

	t.Run("no copy to place fails the item", func(t *testing.T) {
		out := place(t, &placer{}, nil, engine.Item{JSON: map[string]any{"question": "Will it?"}})
		require.Len(t, out[engine.ErrorPort], 1)
	})

	t.Run("an order that could not be tried fails the item", func(t *testing.T) {
		out := place(t, &placer{err: errors.New("the book is not there")}, nil, withCopy(domain.SideBuy, nil))
		require.Len(t, out[engine.ErrorPort], 1)
	})
}

func TestCopySizingFeedsPlaceOrder(t *testing.T) {
	size := trading.NewCopySize(trading.Sources{
		Books: books{}, Rules: rules{}, Markets: markets{}, Positions: theirs{}, Portfolios: theirs{},
		PaperBalance: func() decimal.Decimal { return d("1000") },
	})

	rc := engine.RunContext{WorkflowID: "wf", OwnerID: "u", Params: engine.LiteralParams(size.Type(), nil)}

	sized, err := size.Run(context.Background(), rc, engine.Ports{engine.DefaultPort: {tradeItem(domain.SideBuy, "0.5", "100", "200", nil)}})
	require.NoError(t, err)

	p := &placer{}
	place(t, p, nil, sized[engine.DefaultPort]...)

	require.Len(t, p.asked, 1)
	assert.Equal(t, domain.SideBuy, p.asked[0].Side)
	assert.True(t, d("0.52").Equal(p.asked[0].Price))
	assert.True(t, d("10").Equal(p.asked[0].Dollars))
	assert.True(t, p.asked[0].Shares.IsZero())
	assert.Equal(t, alice, p.asked[0].Trader)
}
