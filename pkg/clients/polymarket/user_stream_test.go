package polymarket

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The user channel's events as the docs and @polymarket/client's schemas show them.
const (
	orderPlaced = `{"event_type":"order","type":"PLACEMENT","id":"0xo1","owner":"key-1","market":"0xm","asset_id":"1",
		"side":"BUY","original_size":"10","size_matched":"0","price":"0.5","associate_trades":[],"status":"LIVE",
		"order_type":"GTC","timestamp":"1760875200000"}`
	tradeConfirmed = `{"event_type":"trade","type":"TRADE","id":"trade-1","taker_order_id":"0xt","market":"0xm","asset_id":"1",
		"side":"SELL","size":"4","price":"0.5","fee_rate_bps":"","status":"CONFIRMED","owner":"key-2","trader_side":"MAKER",
		"maker_orders":[{"order_id":"0xo1","owner":"key-1","maker_address":"0xw","matched_amount":"4","price":"0.5",
		"fee_rate_bps":"","asset_id":"1","outcome":"Yes","side":"BUY"}],"timestamp":"1760875201000"}`
)

func nextUserEvent(t *testing.T, events <-chan UserEvent) UserEvent {
	t.Helper()

	select {
	case e, ok := <-events:
		require.True(t, ok, "the stream ended")
		return e
	case <-time.After(waitFor):
		require.FailNow(t, "no event")
		return UserEvent{}
	}
}

func watch(t *testing.T, url string) (<-chan UserEvent, context.CancelFunc) {
	t.Helper()

	c := NewUserStreamClient(observability.NoopUtility(), UserStreamConfig{
		URL: url, PingInterval: 50 * time.Millisecond, ReconnectMin: 10 * time.Millisecond, ReconnectMax: 20 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	return c.Watch(ctx, L2Credentials{Address: "0xSigner", APIKey: "key-1", Secret: vectorSecret, Passphrase: "pass"}), cancel
}

func TestUserStream(t *testing.T) {
	srv := newWSServer(t)
	events, cancel := watch(t, srv.url)

	conn := srv.accept(t)

	// It subscribes as the official client does: the API key's three parts, every market.
	var sub map[string]any
	require.NoError(t, json.Unmarshal([]byte(conn.next(t)), &sub))
	assert.Equal(t, map[string]any{
		"type": "user", "auth": map[string]any{"apiKey": "key-1", "passphrase": "pass", "secret": vectorSecret},
	}, sub)

	assert.True(t, nextUserEvent(t, events).Gap, "a connection starts with a gap: read what was missed")

	conn.send(t, "PONG", orderPlaced, `[`+tradeConfirmed+`,{"event_type":"something_new"}]`, `not json`)

	placed := nextUserEvent(t, events)
	require.NotNil(t, placed.Order)
	assert.Equal(t, "0xo1", placed.Order.ID)
	assert.Equal(t, "PLACEMENT", placed.Order.Type)
	assert.Equal(t, "LIVE", placed.Order.Status)

	traded := nextUserEvent(t, events)
	require.NotNil(t, traded.Trade)
	assert.Equal(t, TradeConfirmed, traded.Trade.Status)
	assert.Equal(t, []string{"0xt", "0xo1"}, traded.Trade.Orders())

	t.Run("pings to keep the connection", func(t *testing.T) {
		assert.Equal(t, "PING", conn.next(t))
	})

	t.Run("a dropped connection comes back, with a gap", func(t *testing.T) {
		require.NoError(t, conn.conn.Close(websocket.StatusGoingAway, "bye"))

		again := srv.accept(t)
		assert.Contains(t, again.next(t), `"type":"user"`)
		assert.True(t, nextUserEvent(t, events).Gap)
	})

	t.Run("ends with its context", func(t *testing.T) {
		cancel()

		select {
		case _, ok := <-events:
			for ok {
				_, ok = <-events
			}
		case <-time.After(waitFor):
			require.FailNow(t, "the stream did not end")
		}
	})
}
