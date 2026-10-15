package markets_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var clock = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func market(id, question string) domain.Market {
	return domain.Market{
		ID: domain.MarketID(id), Question: question, Status: domain.MarketOpen,
		Outcomes: []domain.Outcome{{TokenID: domain.TokenID(id + "-yes"), Label: "Yes"}, {TokenID: domain.TokenID(id + "-no"), Label: "No"}},
	}
}

func runNode(t *testing.T, node engine.Node, params map[string]any, in engine.Ports) engine.Ports {
	t.Helper()

	out, err := node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}, in)
	require.NoError(t, err)
	require.NoError(t, shape.Check(node.Type(), out), "items as declared")

	return out
}

func one(j map[string]any) engine.Ports {
	return engine.Ports{engine.DefaultPort: {{JSON: j}}}
}

func TestSearch(t *testing.T) {
	settled := market("m0", "Above 90k by yesterday?")
	settled.Status = domain.MarketClosed

	events := portsmocks.NewMockEventReader(t)
	closed := false
	events.EXPECT().ListEvents(mock.Anything, ports.EventFilter{Title: "bitcoin", Limit: 3, Closed: &closed}).
		Return(ports.Page[domain.Event]{Items: []domain.Event{
			{Title: "Bitcoin in October", Slug: "btc-oct", Markets: []domain.Market{settled, market("m1", "Above 100k?"), market("m2", "Above 120k?")}},
			{Title: "Bitcoin in 2027", Markets: []domain.Market{market("m3", "Above 200k?"), market("m4", "Above 300k?")}},
		}}, nil)

	out := runNode(t, markets.NewSearch(events), map[string]any{"query": "bitcoin", "limit": 3}, one(map[string]any{}))

	require.Len(t, out[engine.DefaultPort], 3, "skips closed markets, stops at the limit, across events")
	first := out[engine.DefaultPort][0].JSON
	assert.Equal(t, "Above 100k?", first["question"])
	assert.Equal(t, "Bitcoin in October", first["event"])
	assert.Len(t, first["outcomes"], 2)
}

func TestSearchFailsTheItemWhenTheLookupFails(t *testing.T) {
	events := portsmocks.NewMockEventReader(t)
	events.EXPECT().ListEvents(mock.Anything, mock.Anything).Return(ports.Page[domain.Event]{}, domain.ErrUnavailable)

	out := runNode(t, markets.NewSearch(events), map[string]any{"query": "x"}, one(map[string]any{}))
	require.Len(t, out[engine.ErrorPort], 1)
	assert.ErrorIs(t, out[engine.ErrorPort][0].Err, domain.ErrUnavailable)
}

func TestPriceChange(t *testing.T) {
	history := func(points ...string) ports.Page[domain.PricePoint] {
		var page ports.Page[domain.PricePoint]
		for i, p := range points {
			page.Items = append(page.Items, domain.PricePoint{At: clock.Add(-time.Hour + time.Duration(i)*time.Minute), Price: d(p)})
		}

		return page
	}

	marketItem := map[string]any{
		"id": "m1", "question": "Rain?",
		"outcomes": []any{
			map[string]any{"tokenId": "t-yes", "label": "Yes"},
			map[string]any{"tokenId": "t-no", "label": "No"},
		},
	}

	tests := []struct {
		name     string
		params   map[string]any
		in       map[string]any
		setup    func(h *portsmocks.MockPriceHistoryReader)
		want     []map[string]string // outcome -> fields as strings
		wantFail int
	}{
		{
			name: "the first outcome of a market",
			in:   marketItem,
			setup: func(h *portsmocks.MockPriceHistoryReader) {
				// Out of order on purpose: the node sorts by time.
				page := history("0.40", "0.45", "0.50")
				page.Items[0], page.Items[2] = page.Items[2], page.Items[0]
				h.EXPECT().PriceHistory(mock.Anything, ports.PriceHistoryFilter{TokenID: "t-yes", Since: clock.Add(-time.Hour), Bucket: 2 * time.Minute}).Return(page, nil)
			},
			want: []map[string]string{{"outcome": "Yes", "priceThen": "0.4", "priceNow": "0.5", "change": "0.1", "changePct": "25", "absChangePct": "25"}},
		},
		{
			name:   "every outcome, over a day",
			params: map[string]any{"outcomes": "all", "window": "24h"},
			in:     marketItem,
			setup: func(h *portsmocks.MockPriceHistoryReader) {
				h.EXPECT().PriceHistory(mock.Anything, mock.MatchedBy(func(f ports.PriceHistoryFilter) bool { return f.TokenID == "t-yes" && f.Bucket == 48*time.Minute })).Return(history("0.6", "0.5"), nil)
				h.EXPECT().PriceHistory(mock.Anything, mock.MatchedBy(func(f ports.PriceHistoryFilter) bool { return f.TokenID == "t-no" })).Return(history("0.4", "0.5"), nil)
			},
			want: []map[string]string{
				{"outcome": "Yes", "changePct": "-16.67", "absChangePct": "16.67"},
				{"outcome": "No", "changePct": "25"},
			},
		},
		{
			name: "an order book item names its token",
			in:   map[string]any{"marketId": "m1", "question": "Rain?", "outcome": "No", "tokenId": "t-no"},
			setup: func(h *portsmocks.MockPriceHistoryReader) {
				h.EXPECT().PriceHistory(mock.Anything, mock.Anything).Return(history("0.2", "0.1"), nil)
			},
			want: []map[string]string{{"outcome": "No", "tokenId": "t-no", "change": "-0.1", "marketId": "m1"}},
		},
		{
			name: "no trades in the window",
			in:   marketItem,
			setup: func(h *portsmocks.MockPriceHistoryReader) {
				h.EXPECT().PriceHistory(mock.Anything, mock.Anything).Return(history(), nil)
			},
			wantFail: 1,
		},
		{name: "an item that is not about a market", in: map[string]any{"x": 1}, setup: func(*portsmocks.MockPriceHistoryReader) {}, wantFail: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := portsmocks.NewMockPriceHistoryReader(t)
			tt.setup(h)

			out := runNode(t, markets.NewPriceChange(h).WithClock(func() time.Time { return clock }), tt.params, one(tt.in))

			require.Len(t, out[engine.DefaultPort], len(tt.want))
			assert.Len(t, out[engine.ErrorPort], tt.wantFail)

			for i, want := range tt.want {
				got := out[engine.DefaultPort][i].JSON
				for k, v := range want {
					assert.Equal(t, v, asString(got[k]), "%s of %s", k, want["outcome"])
				}
			}
		})
	}
}

func TestTrades(t *testing.T) {
	trades := portsmocks.NewMockTradeReader(t)
	trades.EXPECT().Trades(mock.Anything, ports.TradeFilter{MarketIDs: []domain.MarketID{"m1"}, Since: clock.Add(-15 * time.Minute), Limit: 50}).
		Return(ports.Page[domain.Trade]{Items: []domain.Trade{
			{MarketID: "m1", TokenID: "t-yes", Outcome: "Yes", Side: domain.SideBuy, Price: d("0.6"), Size: d("2500"), Wallet: "0xabc", TxHash: "0xt1", At: clock},
		}}, nil)
	trades.EXPECT().Trades(mock.Anything, mock.MatchedBy(func(f ports.TradeFilter) bool { return f.MarketIDs[0] == "m2" })).
		Return(ports.Page[domain.Trade]{}, errors.New("data api down"))

	in := engine.Ports{engine.DefaultPort: {
		{JSON: map[string]any{"id": "m1", "question": "Rain?"}},
		{JSON: map[string]any{"marketId": "m2"}},
		{JSON: map[string]any{"nothing": true}},
	}}

	out := runNode(t, markets.NewTrades(trades).WithClock(func() time.Time { return clock }), map[string]any{"since": "15m"}, in)

	require.Len(t, out[engine.DefaultPort], 1)
	trade := out[engine.DefaultPort][0].JSON
	assert.Equal(t, "Rain?", trade["question"])
	assert.Equal(t, "1500", asString(trade["value"]), "price times size, in dollars")
	assert.Equal(t, "buy", trade["side"])
	assert.Len(t, out[engine.ErrorPort], 2, "a failed lookup and an item without a market")
}

func asString(v any) string {
	switch x := v.(type) {
	case decimal.Decimal:
		return x.String()
	case string:
		return x
	default:
		return ""
	}
}
