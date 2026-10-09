// Package events tells signed-in users, as it happens, what their workflows do.
package events

import (
	"context"
	"fmt"
	"sync"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.ExecutionNotifier = (*Hub)(nil)

const (
	defaultBuffer = 64

	// MaxStreams bounds the live connections one user holds, a few per browser tab.
	MaxStreams = 8
)

// Event is something that happened in one of a user's workflows: for now, an
// execution that finished.
type Event struct {
	Execution domain.Execution
}

// Hub hands each user the events of their own workflows. Delivery never blocks: a
// subscriber whose buffer is full misses the event, which is counted, so a stalled
// browser cannot hold up executions.
type Hub struct {
	log     logging.Logger
	dropped metrics.Counter
	buffer  int

	mu   sync.Mutex
	subs map[domain.UserID]map[*Subscription]struct{}
}

func NewHub(util *observability.Utility) (*Hub, error) {
	dropped, err := util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
		Definition: metrics.Definition{
			Namespace: metrics.DefaultNamespace,
			Subsystem: "events",
			Name:      "dropped_total",
			Help:      "Events a slow subscriber missed.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("events: register metrics: %w", err)
	}

	return &Hub{
		log:     util.Logger.With("component", "events"),
		dropped: dropped,
		buffer:  defaultBuffer,
		subs:    make(map[domain.UserID]map[*Subscription]struct{}),
	}, nil
}

// Subscription receives one user's events until it is closed.
type Subscription struct {
	hub    *Hub
	user   domain.UserID
	events chan Event
	once   sync.Once
}

func (s *Subscription) Events() <-chan Event {
	return s.events
}

// Close stops delivery and closes Events. It is safe to call more than once.
func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		defer s.hub.mu.Unlock()

		delete(s.hub.subs[s.user], s)
		if len(s.hub.subs[s.user]) == 0 {
			delete(s.hub.subs, s.user)
		}

		close(s.events)
	})
}

// Subscribe starts delivering user's events. A user may hold MaxStreams at once.
func (h *Hub) Subscribe(user domain.UserID) (*Subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.subs[user]) >= MaxStreams {
		return nil, fmt.Errorf("%w: at most %d live connections per user", domain.ErrRateLimited, MaxStreams)
	}

	s := &Subscription{hub: h, user: user, events: make(chan Event, h.buffer)}

	if h.subs[user] == nil {
		h.subs[user] = make(map[*Subscription]struct{})
	}

	h.subs[user][s] = struct{}{}

	return s, nil
}

// ExecutionFinished delivers e to the owner's subscribers.
func (h *Hub) ExecutionFinished(owner domain.UserID, e domain.Execution) {
	h.publish(owner, Event{Execution: e})
}

func (h *Hub) publish(user domain.UserID, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for s := range h.subs[user] {
		select {
		case s.events <- ev:
		default:
			if err := h.dropped.Inc(nil); err != nil {
				h.log.Warn(context.Background(), "Failed to increment metric", logging.Err(err))
			}
		}
	}
}
