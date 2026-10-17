package polymarket

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

// A trade row as the stream sends it (2026-10-17), shortened.
const activityRow = `{"connection_id":"gd0rak658WeIKEiyDA==","payload":{"asset":"6105196613","bio":"",` +
	`"conditionId":"0xcf9e","eventSlug":"btc-updown-5m-1792242900","fee":0.08064,"icon":"https://x/BTC.png",` +
	`"name":"BOOOST","outcome":"Up","outcomeIndex":0,"price":0.350000260000936,"profileImage":"",` +
	`"proxyWallet":"0x17aa1297346d985AdfF54621a48D4a2a4cE2ba4f","pseudonym":"Simple-Director","side":"BUY",` +
	`"size":5,"slug":"btc-updown-5m-1792242900","timestamp":1792243030,` +
	`"title":"Bitcoin Up or Down - October 17, 9:15AM-9:20AM ET","transactionHash":"0x48e4"},` +
	`"timestamp":1792243030294,"topic":"activity","type":"trades"}`

func startActivity(t *testing.T, util *observability.Utility, cfg ActivityStreamConfig) *DefaultActivityStreamClient {
	t.Helper()

	stream, err := NewActivityStreamClient(util, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})

	go func() { done <- stream.Run(ctx) }()

	return stream
}

func nextActivity(t *testing.T, stream *DefaultActivityStreamClient) ActivityEvent {
	t.Helper()

	select {
	case ev := <-stream.Events():
		return ev
	case <-time.After(waitFor):
		require.FailNow(t, "no event")
		return nil
	}
}

func TestActivityStream(t *testing.T) {
	srv := newWSServer(t)
	stream := startActivity(t, observability.NoopUtility(), ActivityStreamConfig{URL: srv.url, PingInterval: 20 * time.Millisecond})

	conn := srv.accept(t)
	assert.JSONEq(t, `{"action":"subscribe","subscriptions":[{"topic":"activity","type":"*"}]}`, conn.next(t),
		"every trade: there is no filter by wallet")
	assert.IsType(t, ActivityConnected{}, nextActivity(t, stream), "said before any row")

	conn.send(t,
		"", // the empty frame it sends after a subscription
		`{"body":{"message":"leger GetTopics error: not found"}}`,
		`{"payload":{},"topic":"comments","type":"comment_created"}`,
		activityRow,
		`{"payload":{"proxyWallet":"0xabc","transactionHash":"0x48e4","side":"SELL","size":5,"price":0.35},"topic":"activity","type":"orders_matched"}`,
	)

	trade, ok := nextActivity(t, stream).(ActivityTrade)
	require.True(t, ok)
	assert.Equal(t, ActivityTrade{
		Type: ActivityTrades, ProxyWallet: "0x17aa1297346d985AdfF54621a48D4a2a4cE2ba4f", Side: SideBuy,
		Asset: "6105196613", ConditionID: "0xcf9e", Outcome: "Up",
		Title: "Bitcoin Up or Down - October 17, 9:15AM-9:20AM ET", Slug: "btc-updown-5m-1792242900",
		EventSlug: "btc-updown-5m-1792242900", Price: decimal.RequireFromString("0.350000260000936"),
		Size: decimal.NewFromInt(5), Timestamp: 1792243030, TransactionHash: "0x48e4",
		Name: "BOOOST", Pseudonym: "Simple-Director",
	}, trade, "the price as written, not as a float")

	matched, ok := nextActivity(t, stream).(ActivityTrade)
	require.True(t, ok)
	assert.Equal(t, ActivityOrdersMatched, matched.Type)

	assert.Equal(t, "ping", conn.next(t))
}

func TestActivityStreamReconnects(t *testing.T) {
	t.Run("when it goes quiet", func(t *testing.T) {
		reconnects := metricsmocks.NewMockCounter(t)
		reconnects.EXPECT().Inc(metrics.Labels(nil)).Return(nil)

		srv := newWSServer(t)
		stream := startActivity(t, newMockedUtil(t, map[string]*metricsmocks.MockCounter{metricReconnects: reconnects}),
			ActivityStreamConfig{URL: srv.url, Silence: 50 * time.Millisecond, ReconnectMin: 10 * time.Millisecond})

		srv.accept(t)
		assert.IsType(t, ActivityConnected{}, nextActivity(t, stream))

		// Pings go out, but no rows come: a frozen stream is replaced.
		srv.accept(t)
		assert.IsType(t, ActivityConnected{}, nextActivity(t, stream), "and says so, for what it missed")
	})

	t.Run("drops the newest row when the buffer is full", func(t *testing.T) {
		dropped := make(chan struct{}, 1)
		counter := metricsmocks.NewMockCounter(t)
		counter.EXPECT().Inc(metrics.Labels(nil)).Run(func(metrics.Labels) { dropped <- struct{}{} }).Return(nil).Once()

		srv := newWSServer(t)
		stream := startActivity(t, newMockedUtil(t, map[string]*metricsmocks.MockCounter{metricEventsDropped: counter}),
			ActivityStreamConfig{URL: srv.url, BufferSize: 2})

		conn := srv.accept(t)
		conn.send(t, activityRow, activityRow) // with the connected notice, one too many

		select {
		case <-dropped:
		case <-time.After(waitFor):
			require.FailNow(t, "no row was dropped")
		}

		assert.IsType(t, ActivityConnected{}, nextActivity(t, stream))
		assert.IsType(t, ActivityTrade{}, nextActivity(t, stream))
	})
}

func TestActivityStreamRunsAgain(t *testing.T) {
	srv := newWSServer(t)

	stream, err := NewActivityStreamClient(observability.NoopUtility(), ActivityStreamConfig{URL: srv.url})
	require.NoError(t, err)

	for range 2 {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)

		go func() { done <- stream.Run(ctx) }()

		srv.accept(t)
		assert.IsType(t, ActivityConnected{}, nextActivity(t, stream))

		cancel()
		require.NoError(t, <-done, "it stops when nobody needs it, and starts again when someone does")
	}
}
