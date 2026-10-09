package feed

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

const waitFor = 3 * time.Second

// fakeStream records subscriptions and lets the test push updates.
type fakeStream struct {
	updates chan domain.MarketUpdate

	mu           sync.Mutex
	subscribed   []domain.TokenID
	unsubscribed []domain.TokenID
}

func newFakeStream() *fakeStream {
	return &fakeStream{updates: make(chan domain.MarketUpdate, 16)}
}

func (f *fakeStream) Run(ctx context.Context) error {
	<-ctx.Done()
	close(f.updates)

	return nil
}

func (f *fakeStream) Subscribe(_ context.Context, ids ...domain.TokenID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.subscribed = append(f.subscribed, ids...)

	return nil
}

func (f *fakeStream) Unsubscribe(_ context.Context, ids ...domain.TokenID) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.unsubscribed = append(f.unsubscribed, ids...)

	return nil
}

func (f *fakeStream) Updates() <-chan domain.MarketUpdate { return f.updates }

func (f *fakeStream) calls() ([]domain.TokenID, []domain.TokenID) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.subscribed), slices.Clone(f.unsubscribed)
}

func startHub(t *testing.T, util *observability.Utility) (*Hub, *fakeStream, context.CancelFunc) {
	t.Helper()

	stream := newFakeStream()

	hub, err := NewHub(util, stream)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		_ = hub.Run(ctx)
		close(done)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return hub, stream, cancel
}

func next(t *testing.T, ch <-chan domain.MarketUpdate) domain.MarketUpdate {
	t.Helper()

	select {
	case u, ok := <-ch:
		require.True(t, ok, "subscription ended")
		return u
	case <-time.After(waitFor):
		require.FailNow(t, "no update")
		return nil
	}
}

func nothing(t *testing.T, ch <-chan domain.MarketUpdate) {
	t.Helper()

	select {
	case u := <-ch:
		assert.Failf(t, "unexpected update", "%#v", u)
	case <-time.After(30 * time.Millisecond):
	}
}

func trade(token domain.TokenID) domain.TradeUpdate {
	return domain.TradeUpdate{Trade: domain.Trade{TokenID: token}}
}

func TestHubSharesStreamSubscriptions(t *testing.T) {
	hub, stream, _ := startHub(t, observability.NoopUtility())
	ctx := context.Background()

	a, err := hub.Subscribe(ctx, []domain.TokenID{"t1", "t2", "t1"})
	require.NoError(t, err)

	b, err := hub.Subscribe(ctx, []domain.TokenID{"t2", "t3"})
	require.NoError(t, err)

	subscribed, _ := stream.calls()
	assert.Equal(t, []domain.TokenID{"t1", "t2", "t3"}, subscribed, "each token is subscribed once")

	a.Close()
	_, unsubscribed := stream.calls()
	assert.Equal(t, []domain.TokenID{"t1"}, unsubscribed, "t2 still has a subscriber")

	b.Close()
	b.Close() // closing twice is harmless

	_, unsubscribed = stream.calls()
	assert.ElementsMatch(t, []domain.TokenID{"t1", "t2", "t3"}, unsubscribed)
}

func TestHubRouting(t *testing.T) {
	tests := []struct {
		name    string
		tokens  []domain.TokenID
		updates []domain.MarketUpdate
		want    int // updates the subscriber receives
	}{
		{
			name:    "only subscribed tokens",
			tokens:  []domain.TokenID{"t1"},
			updates: []domain.MarketUpdate{trade("t1"), trade("t2"), trade("t1")},
			want:    2,
		},
		{
			name:   "a change touching two subscribed tokens arrives once",
			tokens: []domain.TokenID{"t1", "t2"},
			updates: []domain.MarketUpdate{domain.BookChanges{MarketID: "m", Changes: []domain.LevelChange{
				{TokenID: "t1"}, {TokenID: "t2"},
			}}},
			want: 1,
		},
		{
			name:   "resolution reaches every token of its market",
			tokens: []domain.TokenID{"no"},
			updates: []domain.MarketUpdate{
				domain.BookSnapshot{OrderBook: domain.OrderBook{MarketID: "m", TokenID: "no"}},
				domain.MarketResolution{MarketID: "m", WinningTokenID: "yes"},
			},
			want: 2,
		},
		{
			name:    "unknown update types are ignored",
			tokens:  []domain.TokenID{"t1"},
			updates: []domain.MarketUpdate{nil},
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub, stream, _ := startHub(t, observability.NoopUtility())

			sub, err := hub.Subscribe(context.Background(), tt.tokens)
			require.NoError(t, err)

			for _, u := range tt.updates {
				stream.updates <- u
			}

			for range tt.want {
				next(t, sub.Updates())
			}

			nothing(t, sub.Updates())
		})
	}
}

func TestHubDropsForSlowSubscribers(t *testing.T) {
	dropped := metricsmocks.NewMockCounter(t)
	dropped.EXPECT().Inc(metrics.Labels(nil)).Return(nil).Times(2)

	manager := metricsmocks.NewMockManager(t)
	manager.EXPECT().RegisterCounter(mock.Anything).Return(dropped, nil)

	util := observability.NewUtility(logging.Noop(), manager, observability.NoopUtility().TracerManager)
	hub, stream, _ := startHub(t, util)
	hub.buffer = 1

	slow, err := hub.Subscribe(context.Background(), []domain.TokenID{"t1"})
	require.NoError(t, err)

	fast, err := hub.Subscribe(context.Background(), []domain.TokenID{"t1"})
	require.NoError(t, err)

	stream.updates <- trade("t1")
	next(t, fast.Updates())

	stream.updates <- trade("t1")
	next(t, fast.Updates())

	stream.updates <- trade("t1")
	next(t, fast.Updates())

	assert.Len(t, slow.Updates(), 1, "the slow subscriber kept the first update and missed the rest")
}

func TestHubEndsSubscriptions(t *testing.T) {
	tests := []struct {
		name string
		end  func(cancelSub, stopHub context.CancelFunc)
	}{
		{name: "when the subscriber's context ends", end: func(cancelSub, _ context.CancelFunc) { cancelSub() }},
		{name: "when the hub stops", end: func(_, stopHub context.CancelFunc) { stopHub() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub, _, stopHub := startHub(t, observability.NoopUtility())

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			sub, err := hub.Subscribe(ctx, []domain.TokenID{"t1"})
			require.NoError(t, err)

			tt.end(cancel, stopHub)

			select {
			case _, ok := <-sub.Updates():
				assert.False(t, ok)
			case <-time.After(waitFor):
				require.FailNow(t, "subscription did not end")
			}
		})
	}
}

func TestHubSubscribeValidation(t *testing.T) {
	hub, _, _ := startHub(t, observability.NoopUtility())

	tooMany := make([]domain.TokenID, maxTokens+1)
	for i := range tooMany {
		tooMany[i] = domain.TokenID(string(rune('a' + i%26)))
	}

	tests := []struct {
		name    string
		tokens  []domain.TokenID
		wantErr error
	}{
		{name: "no tokens", wantErr: ErrNoTokens},
		{name: "too many tokens", tokens: tooMany, wantErr: domain.ErrInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hub.Subscribe(context.Background(), tt.tokens)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
