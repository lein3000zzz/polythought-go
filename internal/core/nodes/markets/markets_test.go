package markets

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var errDown = errors.New("api down")

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func runContext(typ engine.NodeType, params map[string]any) engine.RunContext {
	return engine.RunContext{ExecutionID: "ex", WorkflowID: "wf", NodeID: "n", Params: engine.LiteralParams(typ, params)}
}

func inputs(n int) engine.Ports {
	items := make([]engine.Item, n)
	for i := range items {
		items[i] = engine.Item{JSON: map[string]any{}}
	}

	return engine.Ports{engine.DefaultPort: items}
}

func market(id string, tokens ...string) domain.Market {
	m := domain.Market{ID: domain.MarketID(id), Question: "Q " + id, Status: domain.MarketOpen}
	for i, tok := range tokens {
		m.Outcomes = append(m.Outcomes, domain.Outcome{TokenID: domain.TokenID(tok), Label: []string{"Yes", "No"}[i%2], Price: dec("0.5")})
	}

	return m
}

func TestMarketsNode(t *testing.T) {
	closed, open := true, false

	tests := []struct {
		name    string
		params  map[string]any
		in      engine.Ports
		setup   func(r *portsmocks.MockMarketReader)
		wantIDs []any
		wantErr error
		failed  int
	}{
		{
			name: "defaults list open markets",
			in:   inputs(1),
			setup: func(r *portsmocks.MockMarketReader) {
				r.EXPECT().ListMarkets(mock.Anything, ports.MarketFilter{Limit: 20, Closed: &open}).
					Return(ports.Page[domain.Market]{Items: []domain.Market{market("0xa", "t1", "t2"), market("0xb")}}, nil)
			},
			wantIDs: []any{"0xa", "0xb"},
		},
		{
			name:   "configured filter",
			params: map[string]any{"limit": 3, "closed": true, "slugs": []any{"s1"}, "tagIds": []string{"t"}},
			in:     inputs(1),
			setup: func(r *portsmocks.MockMarketReader) {
				r.EXPECT().ListMarkets(mock.Anything, ports.MarketFilter{Limit: 3, Closed: &closed, Slugs: []string{"s1"}, TagIDs: []string{"t"}}).
					Return(ports.Page[domain.Market]{Items: []domain.Market{market("0xc")}}, nil)
			},
			wantIDs: []any{"0xc"},
		},
		{
			name: "runs once per input item",
			in:   inputs(2),
			setup: func(r *portsmocks.MockMarketReader) {
				r.EXPECT().ListMarkets(mock.Anything, mock.Anything).
					Return(ports.Page[domain.Market]{Items: []domain.Market{market("0xa")}}, nil).Twice()
			},
			wantIDs: []any{"0xa", "0xa"},
		},
		{
			name: "lookup failure fails the item",
			in:   inputs(1),
			setup: func(r *portsmocks.MockMarketReader) {
				r.EXPECT().ListMarkets(mock.Anything, mock.Anything).Return(ports.Page[domain.Market]{}, errDown)
			},
			failed: 1,
		},
		{
			name:    "bad param fails the node",
			params:  map[string]any{"limit": "lots"},
			in:      inputs(1),
			setup:   func(*portsmocks.MockMarketReader) {},
			wantErr: engine.ErrInvalidParam,
		},
		{
			name:  "no input runs nothing",
			in:    engine.Ports{},
			setup: func(*portsmocks.MockMarketReader) {},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := portsmocks.NewMockMarketReader(t)
			tt.setup(reader)

			node := NewMarkets(reader)
			out, err := node.Run(context.Background(), runContext(node.Type(), tt.params), tt.in)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			var ids []any
			for _, it := range out[engine.DefaultPort] {
				ids = append(ids, it.JSON["id"])
			}

			assert.Equal(t, tt.wantIDs, ids)
			assert.Len(t, out[engine.ErrorPort], tt.failed)
		})
	}
}

func TestMarketsNodeStopsWhenCancelled(t *testing.T) {
	node := NewMarkets(portsmocks.NewMockMarketReader(t))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := node.Run(ctx, runContext(node.Type(), nil), inputs(1))
	require.ErrorIs(t, err, context.Canceled)
}

func marketInput(markets ...domain.Market) engine.Ports {
	items := make([]engine.Item, len(markets))
	for i, m := range markets {
		items[i] = engine.Item{JSON: MarketItem(m)}
	}

	return engine.Ports{engine.DefaultPort: items}
}

func book(token string, bids, asks [][2]string) domain.OrderBook {
	levels := func(in [][2]string) []domain.Level {
		out := make([]domain.Level, len(in))
		for i, l := range in {
			out[i] = domain.Level{Price: dec(l[0]), Size: dec(l[1])}
		}

		return out
	}

	return domain.OrderBook{TokenID: domain.TokenID(token), Bids: levels(bids), Asks: levels(asks), At: time.Unix(1, 0).UTC()}
}

func TestOrderBooksNode(t *testing.T) {
	twoSided := [][2]string{{"0.26", "10"}, {"0.25", "20"}, {"0.24", "30"}}
	asks := [][2]string{{"0.27", "5"}, {"0.28", "6"}}

	tests := []struct {
		name   string
		params map[string]any
		in     engine.Ports
		setup  func(r *portsmocks.MockOrderBookReader)
		check  func(t *testing.T, out engine.Ports)
	}{
		{
			name: "one item per outcome with a summary",
			in:   marketInput(market("0xa", "t1", "t2")),
			setup: func(r *portsmocks.MockOrderBookReader) {
				r.EXPECT().OrderBooks(mock.Anything, []domain.TokenID{"t1", "t2"}).
					Return([]domain.OrderBook{book("t2", twoSided, asks), book("t1", twoSided, asks)}, nil)
			},
			check: func(t *testing.T, out engine.Ports) {
				items := out[engine.DefaultPort]
				require.Len(t, items, 2)

				first := items[0].JSON
				assert.Equal(t, "0xa", first["marketId"])
				assert.Equal(t, "Yes", first["outcome"])
				assert.Equal(t, "t1", first["tokenId"])
				assert.Equal(t, "0.26", first["bestBid"].(decimal.Decimal).String())
				assert.Equal(t, "0.265", first["mid"].(decimal.Decimal).String())
				assert.Equal(t, "0.01", first["spread"].(decimal.Decimal).String())
				assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 0}}, items[0].Source)
			},
		},
		{
			name:   "depth sums the top levels",
			params: map[string]any{"depth": 2},
			in:     marketInput(market("0xa", "t1")),
			setup: func(r *portsmocks.MockOrderBookReader) {
				r.EXPECT().OrderBooks(mock.Anything, mock.Anything).Return([]domain.OrderBook{book("t1", twoSided, asks)}, nil)
			},
			check: func(t *testing.T, out engine.Ports) {
				j := out[engine.DefaultPort][0].JSON
				assert.Equal(t, "30", j["bidDepth"].(decimal.Decimal).String())
				assert.Equal(t, "11", j["askDepth"].(decimal.Decimal).String())
			},
		},
		{
			name: "one-sided book has no mid or spread",
			in:   marketInput(market("0xa", "t1")),
			setup: func(r *portsmocks.MockOrderBookReader) {
				r.EXPECT().OrderBooks(mock.Anything, mock.Anything).Return([]domain.OrderBook{book("t1", twoSided, nil)}, nil)
			},
			check: func(t *testing.T, out engine.Ports) {
				j := out[engine.DefaultPort][0].JSON
				assert.Nil(t, j["bestAsk"])
				assert.Nil(t, j["mid"])
				assert.Nil(t, j["spread"])
			},
		},
		{
			name: "tokens shared between items are fetched once",
			in:   marketInput(market("0xa", "t1", "t2"), market("0xa", "t1", "t2")),
			setup: func(r *portsmocks.MockOrderBookReader) {
				r.EXPECT().OrderBooks(mock.Anything, []domain.TokenID{"t1", "t2"}).
					Return([]domain.OrderBook{book("t1", twoSided, asks), book("t2", twoSided, asks)}, nil).Once()
			},
			check: func(t *testing.T, out engine.Ports) {
				assert.Len(t, out[engine.DefaultPort], 4)
			},
		},
		{
			name: "missing book fails that outcome",
			in:   marketInput(market("0xa", "t1", "t2")),
			setup: func(r *portsmocks.MockOrderBookReader) {
				r.EXPECT().OrderBooks(mock.Anything, mock.Anything).Return([]domain.OrderBook{book("t1", twoSided, asks)}, nil)
			},
			check: func(t *testing.T, out engine.Ports) {
				assert.Len(t, out[engine.DefaultPort], 1)
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "no order book for token t2")
			},
		},
		{
			name:  "item without outcomes fails before any request",
			in:    engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"id": "x"}}}},
			setup: func(*portsmocks.MockOrderBookReader) {},
			check: func(t *testing.T, out engine.Ports) {
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorIs(t, out[engine.ErrorPort][0].Err, ErrNoOutcomes)
			},
		},
		{
			name: "batch failure fails every item",
			in:   marketInput(market("0xa", "t1"), market("0xb", "t2")),
			setup: func(r *portsmocks.MockOrderBookReader) {
				r.EXPECT().OrderBooks(mock.Anything, mock.Anything).Return(nil, errDown)
			},
			check: func(t *testing.T, out engine.Ports) {
				assert.Empty(t, out[engine.DefaultPort])
				assert.Len(t, out[engine.ErrorPort], 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := portsmocks.NewMockOrderBookReader(t)
			tt.setup(reader)

			node := NewOrderBooks(reader)
			out, err := node.Run(context.Background(), runContext(node.Type(), tt.params), tt.in)
			require.NoError(t, err)

			tt.check(t, out)
		})
	}
}

func TestOutcomesOf(t *testing.T) {
	m := market("0xa", "t1", "t2")

	roundTripped := func() engine.Item {
		raw, err := json.Marshal(MarketItem(m))
		require.NoError(t, err)

		var j map[string]any
		require.NoError(t, json.Unmarshal(raw, &j))

		return engine.Item{JSON: j}
	}

	tests := []struct {
		name    string
		item    engine.Item
		want    []outcomeRef
		wantErr bool
	}{
		{name: "market item", item: engine.Item{JSON: MarketItem(m)}, want: []outcomeRef{{"t1", "Yes"}, {"t2", "No"}}},
		{name: "after a json round trip", item: roundTripped(), want: []outcomeRef{{"t1", "Yes"}, {"t2", "No"}}},
		{name: "outcomes without tokens", item: engine.Item{JSON: MarketItem(domain.Market{Outcomes: []domain.Outcome{{Label: "Yes"}}})}, wantErr: true},
		{name: "no outcomes field", item: engine.Item{JSON: map[string]any{}}, wantErr: true},
		{name: "malformed outcome", item: engine.Item{JSON: map[string]any{"outcomes": []any{"nope"}}}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := outcomesOf(tt.item)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
