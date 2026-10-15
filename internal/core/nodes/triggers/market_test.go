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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

type fakeSub struct{ ch chan domain.MarketUpdate }

func (s fakeSub) Updates() <-chan domain.MarketUpdate { return s.ch }
func (s fakeSub) Close()                              {}

type fakeFeed struct {
	sub    fakeSub
	tokens []domain.TokenID
}

func (f *fakeFeed) Subscribe(_ context.Context, tokens []domain.TokenID) (ports.FeedSubscription, error) {
	f.tokens = tokens
	return f.sub, nil
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestMarketConfig(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr error
	}{
		{name: "tokens are required", params: map[string]any{}, wantErr: engine.ErrMissingParam},
		{name: "empty token list", params: map[string]any{"tokens": []any{}}, wantErr: engine.ErrInvalidParam},
		{name: "unknown kind", params: map[string]any{"tokens": []any{"t1"}, "on": []any{"gossip"}}, wantErr: engine.ErrInvalidParam},
		{name: "batch below the minimum", params: map[string]any{"tokens": []any{"t1"}, "batch": "1ms"}, wantErr: engine.ErrInvalidParam},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := triggers.NewMarket(&fakeFeed{}, 10*time.Millisecond)
			rec := &recorder{}

			err := m.Start(context.Background(), triggerContext(t, m.Type(), tt.params), rec.emit)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestMarketBatches(t *testing.T) {
	trade := func(price string) domain.MarketUpdate {
		return domain.TradeUpdate{Trade: domain.Trade{MarketID: "m", TokenID: "t1", Side: domain.SideBuy, Price: dec(price), Size: dec("1")}}
	}

	tests := []struct {
		name      string
		on        []any
		updates   []domain.MarketUpdate
		wantKinds []string
	}{
		{
			name:      "trades by default",
			updates:   []domain.MarketUpdate{trade("0.5"), domain.QuoteUpdate{TokenID: "t1"}, trade("0.6")},
			wantKinds: []string{"trade", "trade"},
		},
		{
			name: "book changes become one item per level",
			on:   []any{"book"},
			updates: []domain.MarketUpdate{
				domain.BookChanges{MarketID: "m", Changes: []domain.LevelChange{{TokenID: "t1", Side: domain.SideBuy}, {TokenID: "t2", Side: domain.SideSell}}},
				domain.BookSnapshot{OrderBook: domain.OrderBook{TokenID: "t1"}},
			},
			wantKinds: []string{"book", "book", "book"},
		},
		{
			name:      "several kinds",
			on:        []any{"tick", "resolution"},
			updates:   []domain.MarketUpdate{domain.TickSizeChange{TokenID: "t1"}, trade("0.5"), domain.MarketResolution{MarketID: "m"}},
			wantKinds: []string{"tick", "resolution"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			feed := &fakeFeed{sub: fakeSub{ch: make(chan domain.MarketUpdate, 16)}}
			m := triggers.NewMarket(feed, 10*time.Millisecond)

			params := map[string]any{"tokens": []any{"t1", "t2"}, "batch": "50ms"}
			if tt.on != nil {
				params["on"] = tt.on
			}

			for _, u := range tt.updates {
				feed.sub.ch <- u
			}

			rec := &recorder{}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)

			go func() { done <- m.Start(ctx, triggerContext(t, m.Type(), params), rec.emit) }()

			require.Eventually(t, func() bool { return rec.count() >= 1 }, waitFor, time.Millisecond)
			cancel()
			require.NoError(t, <-done)

			assert.Equal(t, []domain.TokenID{"t1", "t2"}, feed.tokens)
			assert.Equal(t, 1, rec.count(), "everything in one window starts one execution")

			require.NoError(t, shape.Check(m.Type(), rec.first()), "items as declared")

			items := rec.first()[engine.DefaultPort]
			got := make([]string, len(items))
			for i, it := range items {
				got[i] = it.JSON["kind"].(string)
			}

			assert.Equal(t, tt.wantKinds, got)
		})
	}
}

func TestMarketStopsWhenTheFeedEnds(t *testing.T) {
	feed := &fakeFeed{sub: fakeSub{ch: make(chan domain.MarketUpdate)}}
	close(feed.sub.ch)

	m := triggers.NewMarket(feed, 10*time.Millisecond)
	err := m.Start(context.Background(), triggerContext(t, m.Type(), map[string]any{"tokens": []any{"t1"}}), (&recorder{}).emit)
	require.NoError(t, err)
}
