package traders_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/traders"
)

var (
	alice = address("0x5f3c00000000000000000000000000000000a1b2")
	bob   = address("0x91d000000000000000000000000000000000c7e4")
)

// address is a wallet as the domain writes it, checksummed.
func address(lower string) domain.WalletAddress {
	w, err := domain.ParseAddress(lower)
	if err != nil {
		panic(err)
	}

	return w
}

var waitFor = 3 * time.Second

// stream is an activity stream the test writes to; it counts how often it runs.
type stream struct {
	events  chan ports.ActivityEvent
	running atomic.Int32
	runs    atomic.Int32
}

func newStream() *stream { return &stream{events: make(chan ports.ActivityEvent)} }

func (s *stream) Events() <-chan ports.ActivityEvent { return s.events }

func (s *stream) Run(ctx context.Context) error {
	s.runs.Add(1)
	s.running.Add(1)
	defer s.running.Add(-1)

	<-ctx.Done()

	return nil
}

// history answers the Data API's trades of a wallet, and keeps what it was asked.
type history struct {
	mu     sync.Mutex
	trades map[domain.WalletAddress][]domain.Trade
	asked  []ports.TradeFilter
}

func (h *history) Trades(_ context.Context, f ports.TradeFilter) (ports.Page[domain.Trade], error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.asked = append(h.asked, f)

	var out []domain.Trade

	for _, t := range h.trades[domain.WalletAddress(strings.ToLower(string(f.Wallet)))] {
		if !t.At.Before(f.Since) {
			out = append(out, t)
		}
	}

	return ports.Page[domain.Trade]{Items: out}, nil
}

func (h *history) set(w domain.WalletAddress, trades ...domain.Trade) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.trades == nil {
		h.trades = map[domain.WalletAddress][]domain.Trade{}
	}

	h.trades[domain.WalletAddress(strings.ToLower(string(w)))] = trades // the Data API's own case
}

type markets struct{ known []domain.Market }

func (m markets) ListMarkets(_ context.Context, f ports.MarketFilter) (ports.Page[domain.Market], error) {
	var out []domain.Market

	for _, mk := range m.known {
		for _, o := range mk.Outcomes {
			for _, t := range f.TokenIDs {
				if o.TokenID == t {
					out = append(out, mk)
				}
			}
		}
	}

	return ports.Page[domain.Market]{Items: out}, nil
}

func (markets) GetMarket(context.Context, domain.MarketID) (domain.Market, error) {
	return domain.Market{}, domain.ErrNotFound
}

func (markets) GetMarketBySlug(context.Context, string) (domain.Market, error) {
	return domain.Market{}, domain.ErrNotFound
}

// quick is a hub that settles in 40 ms and polls only when told.
var quick = traders.Config{Settle: 40 * time.Millisecond, Poll: time.Hour, PollDown: time.Hour, Quiet: time.Hour, Linger: 50 * time.Millisecond}

func run(t *testing.T, s *stream, h *history, m markets, cfg traders.Config) *traders.Hub {
	t.Helper()

	hub, err := traders.NewHub(observability.NoopUtility(), s, h, m, cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- hub.Run(ctx) }()

	t.Cleanup(func() {
		cancel()
		assert.NoError(t, <-done)
	})

	return hub
}

func fill(w domain.WalletAddress, tx string, size, price string) domain.Fill {
	return domain.Fill{
		Wallet: w, TxHash: tx, TokenID: "t-yes", MarketID: "0xm", Outcome: "Yes", Title: "Will it?",
		Side: domain.SideBuy, Size: decimal.RequireFromString(size), Price: decimal.RequireFromString(price),
		At: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
	}
}

func send(t *testing.T, s *stream, events ...ports.ActivityEvent) {
	t.Helper()

	for _, ev := range events {
		select {
		case s.events <- ev:
		case <-time.After(waitFor):
			require.FailNow(t, "the hub does not read the stream")
		}
	}
}

// collect reads trades until it has n of them.
func collect(t *testing.T, sub ports.TraderSubscription, n int) []domain.TraderTrade {
	t.Helper()

	var got []domain.TraderTrade

	for len(got) < n {
		select {
		case batch := <-sub.Trades():
			got = append(got, batch...)
		case <-time.After(waitFor):
			require.FailNow(t, "too few trades", "got %d of %d", len(got), n)
		}
	}

	return got
}

func nothingMore(t *testing.T, sub ports.TraderSubscription) {
	t.Helper()

	select {
	case batch := <-sub.Trades():
		assert.Empty(t, batch, "nothing more is delivered")
	case <-time.After(150 * time.Millisecond):
	}
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestHubGroupsFillsIntoTrades(t *testing.T) {
	s := newStream()
	hub := run(t, s, &history{}, markets{}, quick)

	sub, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Time{})
	require.NoError(t, err)
	defer sub.Close()

	require.Eventually(t, func() bool { return s.running.Load() == 1 }, waitFor, 5*time.Millisecond, "opened for a follower")

	taking := fill(alice, "0xtx1", "5", "0.56")
	send(t, s,
		ports.ActivityEvent{Fill: taking, Taker: true},              // the transaction's taking order: alice's
		ports.ActivityEvent{Fill: taking},                           // and her fill
		ports.ActivityEvent{Fill: fill(bob, "0xtx1", "5", "0.56")},  // someone else's
		ports.ActivityEvent{Fill: fill(alice, "0xTX2", "1", "0.4")}, // two resting orders of hers, filled
		ports.ActivityEvent{Fill: fill(alice, "0xtx2", "2", "0.4")},
	)

	got := collect(t, sub, 2)
	byTx := map[string]domain.TraderTrade{}
	for _, tr := range got {
		byTx[tr.TxHash] = tr
	}

	took := byTx["0xtx1"]
	assert.Equal(t, domain.RoleTaker, took.Role)
	assert.True(t, dec("5").Equal(took.Shares))
	assert.True(t, dec("2.8").Equal(took.Dollars))
	assert.True(t, dec("0.56").Equal(took.Price))

	made := byTx["0xTX2"]
	assert.Equal(t, domain.RoleMaker, made.Role, "her resting orders count too (D88)")
	assert.Equal(t, 2, made.Fills, "the same transaction, whatever the case of its hash")
	assert.True(t, dec("3").Equal(made.Shares))
	assert.True(t, dec("1.2").Equal(made.Dollars))
	assert.Equal(t, "Will it?", made.Title)

	nothingMore(t, sub)
}

func TestHubDeliversToWhoFollows(t *testing.T) {
	s := newStream()
	hub := run(t, s, &history{}, markets{}, quick)

	ofAlice, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Time{})
	require.NoError(t, err)

	ofBoth, err := hub.Follow(context.Background(), []domain.WalletAddress{"0x5f3c00000000000000000000000000000000a1b2", bob}, time.Time{})
	require.NoError(t, err)

	send(t, s, ports.ActivityEvent{Fill: fill(bob, "0xb", "1", "0.5")}, ports.ActivityEvent{Fill: fill(alice, "0xa", "1", "0.5")})

	assert.Equal(t, "0xa", collect(t, ofAlice, 1)[0].TxHash)
	assert.Len(t, collect(t, ofBoth, 2), 2, "an address in any case is the same wallet")
	nothingMore(t, ofAlice)

	ofAlice.Close()
	ofBoth.Close()

	_, open := <-ofAlice.Trades()
	assert.False(t, open, "closed with the subscription")
}

func TestHubCatchesUp(t *testing.T) {
	at := time.Now().Add(-30 * time.Second)
	missed := domain.Trade{
		Wallet: "0x5f3c00000000000000000000000000000000a1b2", TxHash: "0xold", TokenID: "t-yes", MarketID: "0xm",
		Side: domain.SideSell, Size: dec("4"), Price: dec("0.25"), At: at,
	}

	h := &history{}
	h.set(alice, missed)

	s := newStream()
	hub := run(t, s, h, markets{}, quick)

	t.Run("a new follower looks back as asked", func(t *testing.T) {
		sub, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Now().Add(-2*time.Minute))
		require.NoError(t, err)
		defer sub.Close()

		got := collect(t, sub, 1)[0]
		assert.Equal(t, "0xold", got.TxHash)
		assert.Equal(t, domain.RoleUnknown, got.Role, "the Data API does not say who took")
		assert.Equal(t, alice, got.Wallet, "checksummed")

		h.mu.Lock()
		assert.True(t, h.asked[0].Makers, "resting orders included")
		h.mu.Unlock()

		// The stream bringing it late delivers it no more.
		send(t, s, ports.ActivityEvent{Fill: domain.Fill{
			Wallet: alice, TxHash: "0xold", TokenID: "t-yes", Side: domain.SideSell, Size: dec("4"), Price: dec("0.25"), At: at,
		}})
		nothingMore(t, sub)
	})

	t.Run("after the stream comes back, what it missed is looked for", func(t *testing.T) {
		sub, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Time{})
		require.NoError(t, err)
		defer sub.Close()

		send(t, s, ports.ActivityEvent{Fill: fill(alice, "0xlive", "1", "0.5")})
		collect(t, sub, 1)

		h.set(alice, missed, domain.Trade{
			Wallet: alice, TxHash: "0xgap", TokenID: "t-yes", Side: domain.SideBuy, Size: dec("1"), Price: dec("0.5"), At: time.Now(),
		})
		send(t, s, ports.ActivityEvent{Connected: true})

		got := collect(t, sub, 1)
		assert.Equal(t, "0xgap", got[0].TxHash, "and only what was not delivered")
	})
}

func TestHubPollsWhileTheStreamIsDown(t *testing.T) {
	h := &history{}
	h.set(alice, domain.Trade{
		Wallet: alice, TxHash: "0xpolled", TokenID: "t-yes", Side: domain.SideBuy, Size: dec("1"), Price: dec("0.5"), At: time.Now(),
	})

	cfg := quick
	cfg.PollDown = 50 * time.Millisecond // no row yet: down

	hub := run(t, newStream(), h, markets{}, cfg)

	sub, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Time{})
	require.NoError(t, err)
	defer sub.Close()

	assert.Equal(t, "0xpolled", collect(t, sub, 1)[0].TxHash)
}

func TestHubNamesTheMarketOfABareFill(t *testing.T) {
	s := newStream()
	hub := run(t, s, &history{}, markets{known: []domain.Market{{
		ID: "0xm2", Question: "Rain tomorrow?", Slug: "rain",
		Outcomes: []domain.Outcome{{TokenID: "t-no", Label: "No"}, {TokenID: "t-bare", Label: "Yes"}},
	}}}, quick)

	sub, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Time{})
	require.NoError(t, err)
	defer sub.Close()

	bare := domain.Fill{Wallet: alice, TxHash: "0xbare", TokenID: "t-bare", Side: domain.SideBuy, Size: dec("1"), Price: dec("0.5")}
	send(t, s, ports.ActivityEvent{Fill: bare})

	got := collect(t, sub, 1)[0]
	assert.Equal(t, domain.MarketID("0xm2"), got.MarketID)
	assert.Equal(t, "Rain tomorrow?", got.Title)
	assert.Equal(t, "Yes", got.Outcome)
}

func TestHubOpensTheStreamOnlyWhileFollowed(t *testing.T) {
	s := newStream()
	hub := run(t, s, &history{}, markets{}, quick)

	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, s.runs.Load(), "nobody follows anyone yet")

	sub, err := hub.Follow(context.Background(), []domain.WalletAddress{alice}, time.Time{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return s.running.Load() == 1 }, waitFor, 5*time.Millisecond)

	sub.Close()
	require.Eventually(t, func() bool { return s.running.Load() == 0 }, waitFor, 5*time.Millisecond, "closed a while after the last leaves")

	_, err = hub.Follow(context.Background(), nil, time.Time{})
	require.ErrorIs(t, err, traders.ErrNoWallets)
}
