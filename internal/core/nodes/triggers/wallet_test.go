package triggers_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

type traderSub struct{ ch chan []domain.TraderTrade }

func (s traderSub) Trades() <-chan []domain.TraderTrade { return s.ch }
func (s traderSub) Close()                              {}

type traderFeed struct {
	sub     traderSub
	wallets []domain.WalletAddress
	since   time.Time
	follows int
}

func (f *traderFeed) Follow(_ context.Context, wallets []domain.WalletAddress, since time.Time) (ports.TraderSubscription, error) {
	f.wallets, f.since = wallets, since
	f.follows++

	return f.sub, nil
}

const (
	aliceLower = "0x5f3c00000000000000000000000000000000a1b2"
	bobLower   = "0x91d000000000000000000000000000000000c7e4"
)

var startedAt = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func checksummed(t *testing.T, lower string) domain.WalletAddress {
	t.Helper()

	w, err := domain.ParseAddress(lower)
	require.NoError(t, err)

	return w
}

// follow starts the trigger with params, hands it batch, and returns what it emitted.
func follow(t *testing.T, params map[string]any, resumed bool, batch ...domain.TraderTrade) (*traderFeed, []engine.Ports) {
	t.Helper()

	feed := &traderFeed{sub: traderSub{ch: make(chan []domain.TraderTrade, 1)}}
	w := triggers.NewWalletActivity(feed, observability.NoopUtility().Logger, func() time.Time { return startedAt }, nil)

	tc := triggerContext(t, w.Type(), params)
	tc.Resumed = resumed

	rec := &recorder{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- w.Start(ctx, tc, rec.emit) }()

	if len(batch) > 0 {
		feed.sub.ch <- batch
		require.Eventually(t, func() bool { return rec.count() >= 1 }, waitFor, time.Millisecond)
	}

	time.Sleep(20 * time.Millisecond) // nothing more comes
	cancel()
	require.NoError(t, <-done)

	rec.mu.Lock()
	defer rec.mu.Unlock()

	return feed, rec.emitted
}

func trade(t *testing.T, wallet string, side domain.Side, dollars string, ago time.Duration) domain.TraderTrade {
	t.Helper()

	return domain.TraderTrade{
		Wallet: checksummed(t, wallet), TxHash: "0x" + dollars + string(side), TokenID: "t-yes", MarketID: "0xm",
		Outcome: "Yes", Title: "Will it?", Slug: "will-it", Side: side, Shares: dec(dollars).Mul(dec("2")),
		Dollars: dec(dollars), Price: dec("0.5"), Fills: 1, At: startedAt.Add(-ago), Name: "BOOOST",
		Role: domain.RoleTaker,
	}
}

func traders(rows ...map[string]any) []any {
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r
	}

	return out
}

func TestWalletActivity(t *testing.T) {
	params := map[string]any{
		"traders": traders(
			map[string]any{"label": "Alice", "address": aliceLower, "scale": "0.5", "take": "30"},
			map[string]any{"label": "Bob", "address": bobLower, "on": false},
			map[string]any{"label": "Alice again", "address": string(checksummed(t, aliceLower))},
		),
		"least": "10",
	}

	feed, fires := follow(t, params, false,
		trade(t, aliceLower, domain.SideBuy, "20", time.Second),
		trade(t, aliceLower, domain.SideBuy, "5", time.Second),   // under At least $
		trade(t, aliceLower, domain.SideSell, "50", time.Second), // both sides by default
		trade(t, aliceLower, domain.SideBuy, "30", time.Minute),  // from before it started
		trade(t, bobLower, domain.SideBuy, "40", time.Second),    // off
	)

	assert.Equal(t, []domain.WalletAddress{checksummed(t, aliceLower)}, feed.wallets, "on, once each")
	assert.True(t, feed.since.IsZero(), "a first start does not look back (D86)")

	require.Len(t, fires, 1, "a batch starts one execution")
	require.NoError(t, shape.Check((&triggers.WalletActivity{}).Type(), fires[0]), "items as declared")

	items := fires[0][engine.DefaultPort]
	require.Len(t, items, 2)

	assert.Equal(t, map[string]any{
		"label": "Alice", "address": string(checksummed(t, aliceLower)), "name": "BOOOST",
		"scale": dec("0.5"), "maxDollars": nil, "take": dec("30"),
	}, items[0].JSON["trader"], "the first row of a wallet counts")
	assert.Equal(t, "buy", items[0].JSON["side"])
	assert.Equal(t, "taker", items[0].JSON["role"])
	assert.Equal(t, "Will it?", items[0].JSON["question"])
	assert.True(t, dec("20").Equal(items[0].JSON["dollars"].(decimal.Decimal)))
	assert.Equal(t, "sell", items[1].JSON["side"])
}

func TestWalletActivityCatchesUpWhenResumed(t *testing.T) {
	params := map[string]any{
		"traders": traders(map[string]any{"label": "Alice", "address": aliceLower}),
		"catchUp": "1m",
	}

	feed, fires := follow(t, params, true,
		trade(t, aliceLower, domain.SideBuy, "20", 30*time.Second),
		trade(t, aliceLower, domain.SideBuy, "30", 90*time.Second), // too old: never copied late
	)

	assert.Equal(t, startedAt.Add(-time.Minute), feed.since, "it looks back as far as Catch up within")
	require.Len(t, fires, 1)
	require.Len(t, fires[0][engine.DefaultPort], 1)
	assert.Equal(t, "0x20buy", fires[0][engine.DefaultPort][0].JSON["txHash"])
}

func TestWalletActivitySides(t *testing.T) {
	params := map[string]any{
		"traders": traders(map[string]any{"label": "Alice", "address": aliceLower}),
		"sides":   []any{"sell"},
	}

	_, fires := follow(t, params, false,
		trade(t, aliceLower, domain.SideBuy, "20", time.Second),
		trade(t, aliceLower, domain.SideSell, "20", time.Second),
	)

	require.Len(t, fires, 1)
	require.Len(t, fires[0][engine.DefaultPort], 1)
	assert.Equal(t, "sell", fires[0][engine.DefaultPort][0].JSON["side"])
}

func TestWalletActivityWithEveryoneOff(t *testing.T) {
	feed, fires := follow(t, map[string]any{
		"traders": traders(map[string]any{"label": "Alice", "address": aliceLower, "on": false}),
	}, false)

	assert.Zero(t, feed.follows, "nobody to follow: no stream for it")
	assert.Empty(t, fires)
}

func TestWalletActivityConfig(t *testing.T) {
	w := triggers.NewWalletActivity(&traderFeed{}, observability.NoopUtility().Logger, time.Now, nil)

	err := w.Start(context.Background(), triggerContext(t, w.Type(), map[string]any{
		"traders": traders(map[string]any{"label": "Alice", "address": "0x5f3C00000000000000000000000000000000a1b2"}),
	}), nil)
	require.ErrorIs(t, err, engine.ErrInvalidParam)
	assert.ErrorContains(t, err, "row 1")
}

// remembered is node memory for Remember alone: the keys held, without expiry.
type remembered struct {
	ports.NodeMemory

	mu   sync.Mutex
	keys map[string]bool
}

func (m *remembered) Remember(_ context.Context, _ domain.WorkflowID, _ domain.NodeID, keys []string, _, _ time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var fresh []string

	for _, k := range keys {
		if !m.keys[k] {
			m.keys[k] = true
			fresh = append(fresh, k)
		}
	}

	return fresh, nil
}

func TestWalletActivityAfterARestart(t *testing.T) {
	params := map[string]any{"traders": traders(map[string]any{"label": "Alice", "address": aliceLower}), "catchUp": "10m"}
	memory := &remembered{keys: map[string]bool{}}

	start := func(resumed bool, batch ...domain.TraderTrade) []engine.Ports {
		feed := &traderFeed{sub: traderSub{ch: make(chan []domain.TraderTrade, 1)}}
		w := triggers.NewWalletActivity(feed, observability.NoopUtility().Logger, func() time.Time { return startedAt }, memory)

		tc := triggerContext(t, w.Type(), params)
		tc.Resumed = resumed

		rec := &recorder{}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)

		go func() { done <- w.Start(ctx, tc, rec.emit) }()

		feed.sub.ch <- batch
		time.Sleep(30 * time.Millisecond)
		cancel()
		require.NoError(t, <-done)

		rec.mu.Lock()
		defer rec.mu.Unlock()

		return rec.emitted
	}

	copied := trade(t, aliceLower, domain.SideBuy, "20", time.Second)
	first := start(false, copied)
	require.Len(t, first, 1)
	assert.NotContains(t, first[0][engine.DefaultPort][0].JSON, "tradeKey", "its key is its own business")

	// Back up, its catch-up hands the same trade over again, and one it missed.
	missed := trade(t, aliceLower, domain.SideBuy, "40", 2*time.Minute)
	again := start(true, copied, missed)
	require.Len(t, again, 1)
	require.Len(t, again[0][engine.DefaultPort], 1, "the trade copied before the restart is not copied twice (R3)")
	assert.Equal(t, missed.TxHash, again[0][engine.DefaultPort][0].JSON["txHash"])
}
