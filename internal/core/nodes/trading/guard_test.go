package trading_test

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
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// guarded runs the Risk guard on items and returns what left by each port.
func guarded(t *testing.T, b books, params map[string]any, items ...engine.Item) engine.Ports {
	t.Helper()

	n := trading.NewRiskGuard(b, b, rules{}, markets{}, func() time.Time { return now })

	out, err := n.Run(context.Background(), engine.RunContext{
		WorkflowID: "wf", OwnerID: "u", NodeID: "guard", Params: engine.LiteralParams(n.Type(), params),
	}, engine.Ports{engine.DefaultPort: items})
	require.NoError(t, err)
	require.NoError(t, shape.Check(n.Type(), out), "the fields it adds, as declared")

	return out
}

func copyOf(dollars string) engine.Item {
	item := withCopy(domain.SideBuy, nil)
	item.JSON["copy"].(map[string]any)["dollars"] = d(dollars)

	return item
}

func dollarsOf(t *testing.T, item engine.Item) decimal.Decimal {
	t.Helper()

	return item.JSON["copy"].(map[string]any)["dollars"].(decimal.Decimal)
}

func reason(item engine.Item, field string) any {
	return item.JSON[field].(map[string]any)["reason"]
}

func TestRiskGuardCaps(t *testing.T) {
	t.Run("a copy over a cap is shrunk to fit, saying so (D100)", func(t *testing.T) {
		out := guarded(t, books{}, map[string]any{"maxPerCopy": "20"}, copyOf("50"))

		require.Len(t, out[engine.DefaultPort], 1)
		item := out[engine.DefaultPort][0]
		assert.Equal(t, "20", dollarsOf(t, item).String())
		assert.Equal(t, "50", item.JSON["guard"].(map[string]any)["from"].(decimal.Decimal).String())
		assert.Equal(t, "a copy's cap of $20.00 leaves $20.00", reason(item, "guard"))
	})

	t.Run("the day's cap counts the last 24 hours (D102)", func(t *testing.T) {
		var since time.Time

		out := guarded(t, books{spent: "480", since: &since}, map[string]any{"maxPerDay": "500"}, copyOf("50"))
		assert.Equal(t, "20", dollarsOf(t, out[engine.DefaultPort][0]).String())
		assert.Equal(t, now.Add(-24*time.Hour), since)

		out = guarded(t, books{spent: "500"}, map[string]any{"maxPerDay": "500"}, copyOf("50"))
		require.Len(t, out[trading.PortBlocked], 1)
		assert.Equal(t, "the day's cap of $500.00 is reached: $500.00 already", reason(out[trading.PortBlocked][0], "blocked"))
	})

	t.Run("the market's cap counts what is held there, all traders' copies (D103)", func(t *testing.T) {
		b := books{holdings: []domain.Holding{
			{Mode: domain.ModePaper, Trader: alice, MarketID: "0xm", TokenID: "t-yes", Cost: d("60")},
			{Mode: domain.ModePaper, Trader: "0xBob", MarketID: "0xm", TokenID: "t-no", Cost: d("30")},
			{Mode: domain.ModePaper, Trader: alice, MarketID: "0xother", TokenID: "t-x", Cost: d("500")},
		}}

		out := guarded(t, b, map[string]any{"maxPerMarket": "100"}, copyOf("50"))
		assert.Equal(t, "10", dollarsOf(t, out[engine.DefaultPort][0]).String())
	})

	t.Run("what resting buys hold counts in the day's and the market's caps (D219)", func(t *testing.T) {
		resting := map[domain.MarketID]decimal.Decimal{"0xm": d("60"), "0xother": d("20")}

		out := guarded(t, books{spent: "400", resting: resting}, map[string]any{"maxPerDay": "500"}, copyOf("50"))
		assert.Equal(t, "20", dollarsOf(t, out[engine.DefaultPort][0]).String(), "$400 spent and $80 held")

		b := books{resting: resting, holdings: []domain.Holding{{Mode: domain.ModePaper, MarketID: "0xm", TokenID: "t-yes", Cost: d("30")}}}
		out = guarded(t, b, map[string]any{"maxPerMarket": "100"}, copyOf("50"))
		assert.Equal(t, "10", dollarsOf(t, out[engine.DefaultPort][0]).String(), "$30 held and $60 resting in the market")
	})

	t.Run("live, its live books: what it holds and spent live", func(t *testing.T) {
		var modes []domain.TradingMode
		b := books{live: true, spent: "0", modes: &modes, holdings: []domain.Holding{
			{Mode: domain.ModePaper, MarketID: "0xm", TokenID: "t-yes", Cost: d("90")},
			{Mode: domain.ModeLive, MarketID: "0xm", TokenID: "t-yes", Cost: d("70")},
		}}

		out := guarded(t, b, map[string]any{"maxPerMarket": "100", "maxPerDay": "500"}, copyOf("50"))
		assert.Equal(t, "30", dollarsOf(t, out[engine.DefaultPort][0]).String(), "the paper holding plays no part")
		assert.Equal(t, []domain.TradingMode{domain.ModeLive}, modes, "the day's spending, live")
	})

	t.Run("shrunk under the market's least order, it is blocked", func(t *testing.T) {
		out := guarded(t, books{spent: "498"}, map[string]any{"maxPerDay": "500"}, copyOf("50"))

		require.Len(t, out[trading.PortBlocked], 1)
		assert.Equal(t, "the day's cap of $500.00 leaves $2.00, and $2.00 is under the market's least order of 5 shares",
			reason(out[trading.PortBlocked][0], "blocked"))
	})

	t.Run("copies of one run count against the ones after them", func(t *testing.T) {
		out := guarded(t, books{}, map[string]any{"maxPerDay": "50"}, copyOf("30"), copyOf("30"))

		require.Len(t, out[engine.DefaultPort], 2)
		assert.Equal(t, "30", dollarsOf(t, out[engine.DefaultPort][0]).String())
		assert.Equal(t, "20", dollarsOf(t, out[engine.DefaultPort][1]).String())
	})

	t.Run("no limits, no change", func(t *testing.T) {
		out := guarded(t, books{}, nil, copyOf("50"))
		assert.Nil(t, out[engine.DefaultPort][0].JSON["guard"])
	})
}

func TestRiskGuardBlocks(t *testing.T) {
	for name, tc := range map[string]struct {
		params map[string]any
		want   string // the reason; "" lets it through
	}{
		"over the most price":             {map[string]any{"maxPrice": "0.5"}, "a price of 0.52 is over the most, 0.5"},
		"under the least price":           {map[string]any{"minPrice": "0.6"}, "a price of 0.52 is under the least, 0.6"},
		"within the band":                 {map[string]any{"minPrice": "0.1", "maxPrice": "0.9"}, ""},
		"in a category wanted, any case":  {map[string]any{"categories": []any{"politics"}}, ""},
		"in none of the categories":       {map[string]any{"categories": []any{"Sports", "Crypto"}}, "its market is in none of Sports, Crypto"},
		"in a category left out":          {map[string]any{"notCategories": []any{"world"}}, "its market is in world, which Not categories leaves out"},
		"left out wins over only allowed": {map[string]any{"categories": []any{"Politics"}, "notCategories": []any{"World"}}, "which Not categories leaves out"},
	} {
		t.Run(name, func(t *testing.T) {
			out := guarded(t, books{}, tc.params, copyOf("10"))

			if tc.want == "" {
				require.Len(t, out[engine.DefaultPort], 1)
				return
			}

			require.Len(t, out[trading.PortBlocked], 1)
			assert.Contains(t, reason(out[trading.PortBlocked][0], "blocked"), tc.want)
		})
	}
}

func TestRiskGuardLetsSellsGo(t *testing.T) {
	sell := withCopy(domain.SideSell, map[string]any{"dollars": nil, "shares": d("40")})

	out := guarded(t, books{spent: "1000"}, map[string]any{"maxPerDay": "10", "maxPrice": "0.1", "categories": []any{"Sports"}}, sell)
	require.Len(t, out[engine.DefaultPort], 1, "a sell lowers what is at risk (D101)")

	out = guarded(t, books{}, nil, engine.Item{JSON: map[string]any{"question": "Will it?"}})
	require.Len(t, out[engine.ErrorPort], 1, "no copy to guard")
}
