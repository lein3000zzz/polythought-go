package polymarket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

const waitFor = 3 * time.Second

func TestToUpdate(t *testing.T) {
	const ts = "1791450379907"

	at := time.UnixMilli(1791450379907).UTC()

	tests := []struct {
		name   string
		event  pm.MarketEvent
		want   domain.MarketUpdate
		wantOK bool
	}{
		{
			name: "book becomes a best-first snapshot",
			event: pm.BookEvent{
				Market: "0xc", AssetID: "t1", Timestamp: ts, TickSize: dec("0.01"),
				Bids: []pm.PriceLevel{{Price: dec("0.1"), Size: dec("1")}, {Price: dec("0.2"), Size: dec("2")}},
				Asks: []pm.PriceLevel{{Price: dec("0.4"), Size: dec("3")}, {Price: dec("0.3"), Size: dec("4")}},
			},
			want: domain.BookSnapshot{OrderBook: domain.OrderBook{
				MarketID: "0xc", TokenID: "t1", TickSize: dec("0.01"), At: at,
				Bids: []domain.Level{{Price: dec("0.2"), Size: dec("2")}, {Price: dec("0.1"), Size: dec("1")}},
				Asks: []domain.Level{{Price: dec("0.3"), Size: dec("4")}, {Price: dec("0.4"), Size: dec("3")}},
			}},
			wantOK: true,
		},
		{
			name: "price change keeps every level in order",
			event: pm.PriceChangeEvent{Market: "0xc", Timestamp: ts, Changes: []pm.PriceChange{
				{AssetID: "t1", Price: dec("0.2"), Size: dec("0"), Side: pm.SideBuy, BestBid: dec("0.19"), BestAsk: dec("0.21")},
				{AssetID: "t2", Price: dec("0.8"), Size: dec("5"), Side: pm.SideSell, BestBid: dec("0.79"), BestAsk: dec("0.81")},
			}},
			want: domain.BookChanges{MarketID: "0xc", At: at, Changes: []domain.LevelChange{
				{TokenID: "t1", Side: domain.SideBuy, Price: dec("0.2"), Size: dec("0"), BestBid: dec("0.19"), BestAsk: dec("0.21")},
				{TokenID: "t2", Side: domain.SideSell, Price: dec("0.8"), Size: dec("5"), BestBid: dec("0.79"), BestAsk: dec("0.81")},
			}},
			wantOK: true,
		},
		{
			name: "last trade price becomes a trade",
			event: pm.LastTradePriceEvent{
				Market: "0xc", AssetID: "t1", Price: dec("0.5"), Size: dec("10"), Side: pm.SideSell,
				Timestamp: ts, TransactionHash: "0xh",
			},
			want: domain.TradeUpdate{Trade: domain.Trade{
				MarketID: "0xc", TokenID: "t1", Side: domain.SideSell, Price: dec("0.5"), Size: dec("10"), At: at, TxHash: "0xh",
			}},
			wantOK: true,
		},
		{
			name:   "tick size change keeps the new size",
			event:  pm.TickSizeChangeEvent{Market: "0xc", AssetID: "t1", OldTickSize: dec("0.01"), NewTickSize: dec("0.001"), Timestamp: ts},
			want:   domain.TickSizeChange{MarketID: "0xc", TokenID: "t1", TickSize: dec("0.001"), At: at},
			wantOK: true,
		},
		{
			name:   "best bid ask becomes a quote",
			event:  pm.BestBidAskEvent{Market: "0xc", AssetID: "t1", BestBid: dec("0.4"), BestAsk: dec("0.6"), Timestamp: ts},
			want:   domain.QuoteUpdate{MarketID: "0xc", TokenID: "t1", BestBid: dec("0.4"), BestAsk: dec("0.6"), At: at},
			wantOK: true,
		},
		{
			name:   "resolution with an iso timestamp",
			event:  pm.MarketResolvedEvent{Market: "0xc", WinningAssetID: "t1", WinningOutcome: "Yes", Timestamp: "2026-10-08T15:00:00Z"},
			want:   domain.MarketResolution{MarketID: "0xc", WinningTokenID: "t1", WinningOutcome: "Yes", At: time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)},
			wantOK: true,
		},
		{
			name:  "unknown events are skipped",
			event: pm.UnknownEvent{Type: "new_market", Raw: json.RawMessage(`{}`)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toUpdate(tt.event)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

// fakeStreamClient wires a mock client to a channel the test feeds. Run blocks until
// ctx is done, then closes the events channel and returns runErr, like the real client.
func fakeStreamClient(t *testing.T, runErr error) (*polymarketmocks.MockMarketStreamClient, chan pm.MarketEvent) {
	t.Helper()

	events := make(chan pm.MarketEvent, 10)
	client := polymarketmocks.NewMockMarketStreamClient(t)

	client.EXPECT().Events().Return(events).Maybe()
	client.EXPECT().Run(mock.Anything).RunAndReturn(func(ctx context.Context) error {
		<-ctx.Done()
		close(events)

		return runErr
	}).Maybe()

	return client, events
}

func nextUpdate(t *testing.T, s *Stream) domain.MarketUpdate {
	t.Helper()

	select {
	case u, ok := <-s.Updates():
		require.True(t, ok, "updates closed early")
		return u
	case <-time.After(waitFor):
		require.FailNow(t, "no update")
		return nil
	}
}

func TestStreamLifecycle(t *testing.T) {
	tests := []struct {
		name   string
		runErr error
		run    func(t *testing.T, s *Stream, events chan<- pm.MarketEvent, cancel context.CancelFunc)
		// wantErr is what the first Run returns once cancel has been called.
		wantErr error
	}{
		{
			name: "forwards translated updates in order, skipping unknown ones",
			run: func(t *testing.T, s *Stream, events chan<- pm.MarketEvent, cancel context.CancelFunc) {
				events <- pm.TickSizeChangeEvent{AssetID: "t1", NewTickSize: dec("0.01")}
				events <- pm.UnknownEvent{Type: "new_market"}
				events <- pm.TickSizeChangeEvent{AssetID: "t2", NewTickSize: dec("0.001")}

				assert.Equal(t, domain.TokenID("t1"), nextUpdate(t, s).(domain.TickSizeChange).TokenID)
				assert.Equal(t, domain.TokenID("t2"), nextUpdate(t, s).(domain.TickSizeChange).TokenID)
				cancel()
			},
		},
		{
			name:    "returns the client's error",
			runErr:  errUpstream,
			wantErr: errUpstream,
			run: func(_ *testing.T, _ *Stream, _ chan<- pm.MarketEvent, cancel context.CancelFunc) {
				cancel()
			},
		},
		{
			name: "stops even when nobody reads updates",
			run: func(_ *testing.T, _ *Stream, events chan<- pm.MarketEvent, cancel context.CancelFunc) {
				for range 5 {
					events <- pm.TickSizeChangeEvent{AssetID: "t1"}
				}

				cancel()
			},
		},
		{
			name: "run is allowed once",
			run: func(t *testing.T, s *Stream, _ chan<- pm.MarketEvent, cancel context.CancelFunc) {
				assert.ErrorIs(t, s.Run(context.Background()), ErrStreamStarted)
				cancel()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, events := fakeStreamClient(t, tt.runErr)
			s := NewStream(client)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- s.Run(ctx) }()

			// Give Run a moment to claim the stream before scenarios race it.
			require.Eventually(t, s.started.Load, waitFor, time.Millisecond)

			tt.run(t, s, events, cancel)

			select {
			case err := <-done:
				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(waitFor):
				require.FailNow(t, "Run did not return")
			}

			for range s.Updates() {
				// Drain anything forwarded before shutdown; the loop ends once Updates is closed.
			}
		})
	}
}

func TestStreamSubscriptions(t *testing.T) {
	tests := []struct {
		name  string
		setup func(c *polymarketmocks.MockMarketStreamClient)
		call  func(ctx context.Context, s *Stream) error
	}{
		{
			name: "subscribe passes token ids through",
			setup: func(c *polymarketmocks.MockMarketStreamClient) {
				c.EXPECT().Subscribe(mock.Anything, []string{"t1", "t2"}).Return(nil).Once()
			},
			call: func(ctx context.Context, s *Stream) error { return s.Subscribe(ctx, "t1", "t2") },
		},
		{
			name: "unsubscribe passes token ids through",
			setup: func(c *polymarketmocks.MockMarketStreamClient) {
				c.EXPECT().Unsubscribe(mock.Anything, []string{"t1"}).Return(nil).Once()
			},
			call: func(ctx context.Context, s *Stream) error { return s.Unsubscribe(ctx, "t1") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := polymarketmocks.NewMockMarketStreamClient(t)
			tt.setup(client)

			require.NoError(t, tt.call(context.Background(), NewStream(client)))
		})
	}
}
