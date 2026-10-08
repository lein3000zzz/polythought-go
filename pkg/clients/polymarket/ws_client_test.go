package polymarket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

const waitFor = 3 * time.Second

// wsServer accepts market channel connections and hands each one to the test.
type wsServer struct {
	url   string
	conns chan *wsConn
}

type wsConn struct {
	conn *websocket.Conn
	msgs chan string
}

func newWSServer(t *testing.T) *wsServer {
	t.Helper()

	s := &wsServer{conns: make(chan *wsConn, 10)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}

		c := &wsConn{conn: conn, msgs: make(chan string, 100)}
		s.conns <- c

		for {
			_, msg, err := conn.Read(context.Background())
			if err != nil {
				return
			}

			c.msgs <- string(msg)
		}
	}))
	t.Cleanup(srv.Close)

	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")

	return s
}

func (s *wsServer) accept(t *testing.T) *wsConn {
	t.Helper()

	select {
	case c := <-s.conns:
		return c
	case <-time.After(waitFor):
		require.FailNow(t, "no connection")
		return nil
	}
}

func (c *wsConn) next(t *testing.T) string {
	t.Helper()

	select {
	case msg := <-c.msgs:
		return msg
	case <-time.After(waitFor):
		require.FailNow(t, "no message from client")
		return ""
	}
}

// nextJSON skips keepalive PINGs and returns the next subscription message, decoded and raw.
func (c *wsConn) nextJSON(t *testing.T) (subscribeMessage, string) {
	t.Helper()

	for {
		raw := c.next(t)
		if raw == "PING" {
			continue
		}

		var msg subscribeMessage
		require.NoError(t, json.Unmarshal([]byte(raw), &msg))

		return msg, raw
	}
}

func (c *wsConn) send(t *testing.T, frames ...string) {
	t.Helper()

	for _, f := range frames {
		require.NoError(t, c.conn.Write(context.Background(), websocket.MessageText, []byte(f)))
	}
}

// startStream runs the client until the test ends, then checks that Run returned cleanly.
// Mocks must be created before calling it so their assertions run after Run has stopped.
func startStream(t *testing.T, util *observability.Utility, cfg StreamConfig, preSubscribe ...string) *DefaultMarketStreamClient {
	t.Helper()

	stream, err := NewMarketStreamClient(util, cfg)
	require.NoError(t, err)
	require.NoError(t, stream.Subscribe(context.Background(), preSubscribe...))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})

	go func() { done <- stream.Run(ctx) }()

	return stream
}

func nextEvent(t *testing.T, stream *DefaultMarketStreamClient) MarketEvent {
	t.Helper()

	select {
	case ev := <-stream.Events():
		return ev
	case <-time.After(waitFor):
		require.FailNow(t, "no event")
		return nil
	}
}

func TestDecodeMarketEvent(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		check   func(t *testing.T, ev MarketEvent)
	}{
		{
			name: "book without trades",
			raw:  `{"event_type":"book","asset_id":"a","bids":[{"price":"0.1","size":"5"}],"asks":[],"tick_size":"0.01","last_trade_price":""}`,
			check: func(t *testing.T, ev MarketEvent) {
				book := ev.(BookEvent)
				assert.Equal(t, "a", book.AssetID)
				assert.Len(t, book.Bids, 1)
				assert.True(t, book.TickSize.Equal(dec("0.01")))
				assert.False(t, book.LastTradePrice.Valid)
			},
		},
		{
			name: "price change removing a level",
			raw:  `{"event_type":"price_change","market":"m","price_changes":[{"asset_id":"a","price":"0.2","size":"0","side":"BUY","best_bid":"0.19","best_ask":"0.21"}]}`,
			check: func(t *testing.T, ev MarketEvent) {
				change := ev.(PriceChangeEvent)
				require.Len(t, change.Changes, 1)
				assert.True(t, change.Changes[0].Size.IsZero())
				assert.Equal(t, SideBuy, change.Changes[0].Side)
				assert.True(t, change.Changes[0].BestAsk.Equal(dec("0.21")))
			},
		},
		{
			name: "last trade price",
			raw:  `{"event_type":"last_trade_price","asset_id":"a","price":"0.5","size":"10","side":"SELL","fee_rate_bps":"0"}`,
			check: func(t *testing.T, ev MarketEvent) {
				trade := ev.(LastTradePriceEvent)
				assert.True(t, trade.Price.Equal(dec("0.5")))
				assert.Equal(t, SideSell, trade.Side)
			},
		},
		{
			name: "tick size change",
			raw:  `{"event_type":"tick_size_change","asset_id":"a","old_tick_size":"0.01","new_tick_size":"0.001"}`,
			check: func(t *testing.T, ev MarketEvent) {
				assert.True(t, ev.(TickSizeChangeEvent).NewTickSize.Equal(dec("0.001")))
			},
		},
		{
			name: "best bid ask",
			raw:  `{"event_type":"best_bid_ask","asset_id":"a","best_bid":"0.4","best_ask":"0.6","spread":"0.2"}`,
			check: func(t *testing.T, ev MarketEvent) {
				assert.True(t, ev.(BestBidAskEvent).Spread.Equal(dec("0.2")))
			},
		},
		{
			name: "market resolved",
			raw:  `{"event_type":"market_resolved","market":"m","winning_asset_id":"a","winning_outcome":"Yes"}`,
			check: func(t *testing.T, ev MarketEvent) {
				assert.Equal(t, "Yes", ev.(MarketResolvedEvent).WinningOutcome)
			},
		},
		{
			name: "unknown type keeps the raw payload",
			raw:  `{"event_type":"new_market","id":"x"}`,
			check: func(t *testing.T, ev MarketEvent) {
				unknown := ev.(UnknownEvent)
				assert.Equal(t, MarketEventType("new_market"), unknown.EventType())
				assert.JSONEq(t, `{"event_type":"new_market","id":"x"}`, string(unknown.Raw))
			},
		},
		{name: "invalid json", raw: `{"event_type":`, wantErr: true},
		{name: "bad decimal", raw: `{"event_type":"tick_size_change","new_tick_size":"abc"}`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := decodeMarketEvent([]byte(tt.raw))

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			tt.check(t, ev)
		})
	}
}

func TestStreamSubscriptionMessages(t *testing.T) {
	tests := []struct {
		name         string
		cfg          StreamConfig
		preSubscribe []string
		// act runs after the initial subscribe was received; nil checks only that message.
		act   func(t *testing.T, stream *DefaultMarketStreamClient)
		check func(t *testing.T, msg subscribeMessage, raw string)
	}{
		{
			name:         "initial subscribe carries known assets",
			cfg:          StreamConfig{CustomFeatures: true},
			preSubscribe: []string{"b", "a"},
			check: func(t *testing.T, msg subscribeMessage, _ string) {
				assert.Equal(t, "market", msg.Type)
				assert.True(t, msg.InitialDump)
				assert.True(t, msg.CustomFeatures)
				assert.ElementsMatch(t, []string{"a", "b"}, msg.AssetIDs)
			},
		},
		{
			name: "initial subscribe without assets sends an empty array",
			check: func(t *testing.T, msg subscribeMessage, raw string) {
				assert.Contains(t, raw, `"assets_ids":[]`)
				assert.False(t, msg.CustomFeatures)
			},
		},
		{
			name: "subscribe while connected",
			act: func(t *testing.T, stream *DefaultMarketStreamClient) {
				require.NoError(t, stream.Subscribe(context.Background(), "c"))
			},
			check: func(t *testing.T, msg subscribeMessage, _ string) {
				assert.Equal(t, "subscribe", msg.Operation)
				assert.True(t, msg.InitialDump)
				assert.Equal(t, []string{"c"}, msg.AssetIDs)
			},
		},
		{
			name:         "unsubscribe while connected",
			preSubscribe: []string{"a"},
			act: func(t *testing.T, stream *DefaultMarketStreamClient) {
				require.NoError(t, stream.Unsubscribe(context.Background(), "a"))
			},
			check: func(t *testing.T, msg subscribeMessage, _ string) {
				assert.Equal(t, "unsubscribe", msg.Operation)
				assert.False(t, msg.InitialDump)
				assert.Equal(t, []string{"a"}, msg.AssetIDs)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newWSServer(t)

			cfg := tt.cfg
			cfg.URL = srv.url
			stream := startStream(t, observability.NoopUtility(), cfg, tt.preSubscribe...)

			conn := srv.accept(t)
			msg, raw := conn.nextJSON(t)

			if tt.act != nil {
				tt.act(t, stream)
				msg, raw = conn.nextJSON(t)
			}

			tt.check(t, msg, raw)
		})
	}
}

func TestStreamDispatch(t *testing.T) {
	book := func(asset string) string {
		return `{"event_type":"book","asset_id":"` + asset + `","bids":[],"asks":[],"last_trade_price":""}`
	}

	tests := []struct {
		name   string
		frames []string
		want   []string // event types and asset IDs, in order
	}{
		{
			name:   "snapshot array fans out",
			frames: []string{"[" + book("a") + "," + book("b") + "]"},
			want:   []string{"book:a", "book:b"},
		},
		{
			name:   "single objects after the snapshot",
			frames: []string{"[" + book("a") + "]", `{"event_type":"tick_size_change","asset_id":"a"}`},
			want:   []string{"book:a", "tick_size_change:a"},
		},
		{
			name:   "plain-text replies are skipped",
			frames: []string{"PONG", "INVALID OPERATION", book("a")},
			want:   []string{"book:a"},
		},
		{
			name:   "undecodable events are skipped",
			frames: []string{`{"event_type":"tick_size_change","new_tick_size":"abc"}`, `[{"event_type":`, book("a")},
			want:   []string{"book:a"},
		},
	}

	assetOf := func(ev MarketEvent) string {
		switch e := ev.(type) {
		case BookEvent:
			return e.AssetID
		case TickSizeChangeEvent:
			return e.AssetID
		default:
			return ""
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newWSServer(t)
			stream := startStream(t, observability.NoopUtility(), StreamConfig{URL: srv.url})

			conn := srv.accept(t)
			conn.nextJSON(t)
			conn.send(t, tt.frames...)

			got := make([]string, 0, len(tt.want))
			for range tt.want {
				ev := nextEvent(t, stream)
				got = append(got, string(ev.EventType())+":"+assetOf(ev))
			}

			assert.Equal(t, tt.want, got)

			select {
			case ev := <-stream.Events():
				assert.Failf(t, "unexpected extra event", "%#v", ev)
			case <-time.After(50 * time.Millisecond):
			}
		})
	}
}

func TestStreamLifecycle(t *testing.T) {
	drops := make(chan struct{}, 2)

	tests := []struct {
		name string
		cfg  StreamConfig
		// counters returns the metric mocks this scenario asserts, keyed by metric name.
		counters func(t *testing.T) map[string]*metricsmocks.MockCounter
		run      func(t *testing.T, srv *wsServer, stream *DefaultMarketStreamClient)
	}{
		{
			name: "drops the newest event when the buffer is full",
			cfg:  StreamConfig{BufferSize: 1},
			counters: func(t *testing.T) map[string]*metricsmocks.MockCounter {
				dropped := metricsmocks.NewMockCounter(t)
				dropped.EXPECT().Inc(metrics.Labels{"event_type": string(EventTickSizeChange)}).
					Run(func(metrics.Labels) { drops <- struct{}{} }).
					Return(nil).Times(2)

				return map[string]*metricsmocks.MockCounter{metricEventsDropped: dropped}
			},
			run: func(t *testing.T, srv *wsServer, stream *DefaultMarketStreamClient) {
				conn := srv.accept(t)
				conn.nextJSON(t)

				for _, id := range []string{"first", "second", "third"} {
					conn.send(t, `{"event_type":"tick_size_change","asset_id":"`+id+`"}`)
				}

				for range 2 {
					select {
					case <-drops:
					case <-time.After(waitFor):
						require.FailNow(t, "events were not dropped")
					}
				}

				assert.Equal(t, "first", nextEvent(t, stream).(TickSizeChangeEvent).AssetID, "the oldest event is kept")
			},
		},
		{
			name: "reconnects and resubscribes the full asset set",
			cfg:  StreamConfig{ReconnectMin: 10 * time.Millisecond},
			counters: func(t *testing.T) map[string]*metricsmocks.MockCounter {
				reconnects := metricsmocks.NewMockCounter(t)
				reconnects.EXPECT().Inc(metrics.Labels(nil)).Return(nil).Once()

				return map[string]*metricsmocks.MockCounter{metricReconnects: reconnects}
			},
			run: func(t *testing.T, srv *wsServer, stream *DefaultMarketStreamClient) {
				first := srv.accept(t)
				first.nextJSON(t)

				require.NoError(t, stream.Subscribe(context.Background(), "a"))
				first.nextJSON(t)
				require.NoError(t, first.conn.CloseNow())

				msg, _ := srv.accept(t).nextJSON(t)
				assert.Equal(t, "market", msg.Type)
				assert.Equal(t, []string{"a"}, msg.AssetIDs)
			},
		},
		{
			name: "sends keepalive pings",
			cfg:  StreamConfig{PingInterval: 20 * time.Millisecond},
			run: func(t *testing.T, srv *wsServer, _ *DefaultMarketStreamClient) {
				conn := srv.accept(t)
				conn.nextJSON(t)

				assert.Equal(t, "PING", conn.next(t))
			},
		},
		{
			name: "reconnects when the server goes silent",
			cfg:  StreamConfig{PingInterval: 20 * time.Millisecond, ReconnectMin: 10 * time.Millisecond},
			run: func(t *testing.T, srv *wsServer, _ *DefaultMarketStreamClient) {
				srv.accept(t).nextJSON(t)

				// No PONGs come back, so the 3-interval read deadline forces a new connection.
				srv.accept(t)
			},
		},
		{
			name: "run is allowed once",
			run: func(t *testing.T, srv *wsServer, stream *DefaultMarketStreamClient) {
				srv.accept(t)

				assert.ErrorIs(t, stream.Run(context.Background()), ErrStreamStarted)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var counters map[string]*metricsmocks.MockCounter
			if tt.counters != nil {
				counters = tt.counters(t)
			}

			srv := newWSServer(t)

			cfg := tt.cfg
			cfg.URL = srv.url
			stream := startStream(t, newMockedUtil(t, counters), cfg)

			tt.run(t, srv, stream)
		})
	}
}

func TestStreamClosesEventsWhenRunReturns(t *testing.T) {
	srv := newWSServer(t)

	stream, err := NewMarketStreamClient(observability.NoopUtility(), StreamConfig{URL: srv.url})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- stream.Run(ctx) }()

	srv.accept(t)
	cancel()

	require.NoError(t, <-done)

	_, open := <-stream.Events()
	assert.False(t, open)
}

func TestStreamConfigDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   StreamConfig
		want StreamConfig
	}{
		{
			name: "zero value",
			want: StreamConfig{
				URL: DefaultMarketWSURL, BufferSize: defaultStreamBuffer, PingInterval: defaultPingInterval,
				ReconnectMin: defaultReconnectMin, ReconnectMax: defaultReconnectMax,
			},
		},
		{
			name: "max below min is raised",
			in:   StreamConfig{ReconnectMin: time.Minute, ReconnectMax: time.Second},
			want: StreamConfig{
				URL: DefaultMarketWSURL, BufferSize: defaultStreamBuffer, PingInterval: defaultPingInterval,
				ReconnectMin: time.Minute, ReconnectMax: time.Minute,
			},
		},
		{
			name: "explicit values win",
			in:   StreamConfig{URL: "ws://x", BufferSize: 5, PingInterval: time.Second, ReconnectMin: time.Second, ReconnectMax: time.Hour, CustomFeatures: true},
			want: StreamConfig{URL: "ws://x", BufferSize: 5, PingInterval: time.Second, ReconnectMin: time.Second, ReconnectMax: time.Hour, CustomFeatures: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.withDefaults())
		})
	}
}
