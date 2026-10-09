package events_test

import (
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/events"
	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

const (
	alice = domain.UserID("alice")
	bob   = domain.UserID("bob")
)

func newHub(t *testing.T) *events.Hub {
	t.Helper()

	hub, err := events.NewHub(observability.NoopUtility())
	require.NoError(t, err)

	return hub
}

func subscribe(t *testing.T, hub *events.Hub, user domain.UserID) *events.Subscription {
	t.Helper()

	s, err := hub.Subscribe(user)
	require.NoError(t, err)
	t.Cleanup(s.Close)

	return s
}

func execution(id domain.ExecutionID) domain.Execution {
	return domain.Execution{ID: id, WorkflowID: "wf-1", Status: domain.ExecutionSucceeded}
}

// received drains what is waiting on s without blocking.
func received(s *events.Subscription) []domain.ExecutionID {
	var ids []domain.ExecutionID

	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				return ids
			}

			ids = append(ids, ev.Execution.ID)
		default:
			return ids
		}
	}
}

func TestHubDelivery(t *testing.T) {
	hub := newHub(t)
	aliceTab1 := subscribe(t, hub, alice)
	aliceTab2 := subscribe(t, hub, alice)
	bobTab := subscribe(t, hub, bob)

	hub.ExecutionFinished(alice, execution("ex-1"))
	hub.ExecutionFinished(bob, execution("ex-2"))
	hub.ExecutionFinished("nobody-listening", execution("ex-3"))

	tests := []struct {
		name string
		sub  *events.Subscription
		want []domain.ExecutionID
	}{
		{name: "every stream of the owner", sub: aliceTab1, want: []domain.ExecutionID{"ex-1"}},
		{name: "including a second tab", sub: aliceTab2, want: []domain.ExecutionID{"ex-1"}},
		{name: "never another user's", sub: bobTab, want: []domain.ExecutionID{"ex-2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, received(tt.sub))
		})
	}
}

func TestHubCloseStopsDelivery(t *testing.T) {
	hub := newHub(t)
	s := subscribe(t, hub, alice)

	s.Close()
	s.Close() // twice is fine

	hub.ExecutionFinished(alice, execution("ex-1"))

	_, open := <-s.Events()
	assert.False(t, open)
}

func TestHubLimitsStreamsPerUser(t *testing.T) {
	hub := newHub(t)

	subs := make([]*events.Subscription, events.MaxStreams)
	for i := range subs {
		subs[i] = subscribe(t, hub, alice)
	}

	_, err := hub.Subscribe(alice)
	require.ErrorIs(t, err, domain.ErrRateLimited)

	subscribe(t, hub, bob) // the limit is per user

	subs[0].Close()
	subscribe(t, hub, alice) // a closed stream frees its place
}

func TestHubDropsForSlowSubscribers(t *testing.T) {
	dropped := metricsmocks.NewMockCounter(t)
	dropped.EXPECT().Inc(metrics.Labels(nil)).Return(nil).Times(3)

	manager := metricsmocks.NewMockManager(t)
	manager.EXPECT().RegisterCounter(mock.Anything).Return(dropped, nil)

	hub, err := events.NewHub(observability.NewUtility(logging.Noop(), manager, observability.NoopUtility().TracerManager))
	require.NoError(t, err)

	stalled := subscribe(t, hub, alice)

	for i := range 64 + 3 { // the buffer holds 64
		hub.ExecutionFinished(alice, execution(domain.ExecutionID(rune('a'+i%26))))
	}

	assert.Len(t, received(stalled), 64, "the buffered events are still delivered")
}
