// Package feed shares one live market stream among every workflow that listens to it.
package feed

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.MarketFeed = (*Hub)(nil)

var ErrNoTokens = errors.New("subscribe to at least one token")

const (
	defaultBuffer = 256
	maxTokens     = 100 // per subscription
)

// Hub owns the process's single market stream. It subscribes a token on the stream when
// the first consumer asks for it and unsubscribes when the last one leaves. Updates are
// delivered without blocking: a consumer whose buffer is full misses the update, which
// is counted, so one slow workflow cannot stall the others.
type Hub struct {
	stream  ports.MarketStream
	log     logging.Logger
	dropped metrics.Counter
	buffer  int

	mu       sync.Mutex
	subs     map[domain.TokenID]map[*subscription]struct{}
	markets  map[domain.TokenID]domain.MarketID // learned from book snapshots
	stopping bool
}

func NewHub(util *observability.Utility, stream ports.MarketStream) (*Hub, error) {
	dropped, err := util.MetricsManager.RegisterCounter(metrics.CounterDefinition{
		Definition: metrics.Definition{
			Namespace: metrics.DefaultNamespace,
			Subsystem: "feed",
			Name:      "updates_dropped_total",
			Help:      "Market updates a slow subscriber missed.",
		},
	})
	if err != nil {
		return nil, fmt.Errorf("feed: register metrics: %w", err)
	}

	return &Hub{
		stream:  stream,
		log:     util.Logger.With("component", "feed"),
		dropped: dropped,
		buffer:  defaultBuffer,
		subs:    make(map[domain.TokenID]map[*subscription]struct{}),
		markets: make(map[domain.TokenID]domain.MarketID),
	}, nil
}

// Run drives the stream and dispatches its updates until ctx is done, then ends every
// subscription.
func (h *Hub) Run(ctx context.Context) error {
	errc := make(chan error, 1)
	go func() { errc <- h.stream.Run(ctx) }()

	for update := range h.stream.Updates() {
		h.dispatch(update)
	}

	h.mu.Lock()
	h.stopping = true
	all := make(map[*subscription]struct{})

	for _, set := range h.subs {
		for s := range set {
			all[s] = struct{}{}
		}
	}
	h.mu.Unlock()

	for s := range all {
		s.end()
	}

	return <-errc
}

func (h *Hub) Subscribe(ctx context.Context, tokens []domain.TokenID) (ports.FeedSubscription, error) {
	if len(tokens) == 0 {
		return nil, ErrNoTokens
	}

	if len(tokens) > maxTokens {
		return nil, fmt.Errorf("%w: at most %d tokens per subscription", domain.ErrInvalidInput, maxTokens)
	}

	s := &subscription{
		hub:      h,
		tokens:   unique(tokens),
		updates:  make(chan domain.MarketUpdate, h.buffer),
		finished: make(chan struct{}),
	}

	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return nil, errors.New("feed: hub is stopping")
	}

	var fresh []domain.TokenID

	for _, t := range s.tokens {
		if h.subs[t] == nil {
			h.subs[t] = make(map[*subscription]struct{})
			fresh = append(fresh, t)
		}

		h.subs[t][s] = struct{}{}
	}
	h.mu.Unlock()

	if len(fresh) > 0 {
		if err := h.stream.Subscribe(ctx, fresh...); err != nil {
			s.Close()
			return nil, err
		}
	}

	// End the subscription with its owner, so a cancelled trigger never leaks one.
	go func() {
		select {
		case <-ctx.Done():
			s.Close()
		case <-s.finished:
		}
	}()

	return s, nil
}

func (h *Hub) remove(s *subscription) {
	h.mu.Lock()

	var idle []domain.TokenID

	for _, t := range s.tokens {
		delete(h.subs[t], s)

		if len(h.subs[t]) == 0 {
			delete(h.subs, t)
			idle = append(idle, t)
		}
	}

	stopping := h.stopping
	h.mu.Unlock()

	if len(idle) > 0 && !stopping {
		if err := h.stream.Unsubscribe(context.Background(), idle...); err != nil {
			h.log.Warn(context.Background(), "Unsubscribe failed", "tokens", len(idle), logging.Err(err))
		}
	}
}

// dispatch hands an update to every subscriber of the tokens it concerns, each once.
func (h *Hub) dispatch(u domain.MarketUpdate) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if snap, ok := u.(domain.BookSnapshot); ok && snap.MarketID != "" {
		h.markets[snap.TokenID] = snap.MarketID
	}

	targets := make(map[*subscription]struct{})

	for _, t := range h.tokensOf(u) {
		for s := range h.subs[t] {
			targets[s] = struct{}{}
		}
	}

	for s := range targets {
		select {
		case s.updates <- u:
		default:
			if err := h.dropped.Inc(nil); err != nil {
				h.log.Warn(context.Background(), "Failed to increment metric", logging.Err(err))
			}
		}
	}
}

// tokensOf names the tokens an update is about. A resolution names only its market, so
// it reaches subscribers of every token known to belong to that market.
func (h *Hub) tokensOf(u domain.MarketUpdate) []domain.TokenID {
	switch e := u.(type) {
	case domain.BookSnapshot:
		return []domain.TokenID{e.TokenID}
	case domain.BookChanges:
		out := make([]domain.TokenID, len(e.Changes))
		for i, c := range e.Changes {
			out[i] = c.TokenID
		}

		return out
	case domain.TradeUpdate:
		return []domain.TokenID{e.TokenID}
	case domain.TickSizeChange:
		return []domain.TokenID{e.TokenID}
	case domain.QuoteUpdate:
		return []domain.TokenID{e.TokenID}
	case domain.MarketResolution:
		out := []domain.TokenID{e.WinningTokenID}
		for t, m := range h.markets {
			if m == e.MarketID && t != e.WinningTokenID {
				out = append(out, t)
			}
		}

		return out
	default:
		return nil
	}
}

type subscription struct {
	hub     *Hub
	tokens  []domain.TokenID
	updates chan domain.MarketUpdate

	once     sync.Once
	finished chan struct{}
}

func (s *subscription) Updates() <-chan domain.MarketUpdate { return s.updates }

// Close leaves the hub; it is safe to call more than once.
func (s *subscription) Close() {
	s.once.Do(func() {
		s.hub.remove(s)
		s.closeChannels()
	})
}

// end is Close for a hub shutting down: the stream is going away, nothing to unsubscribe.
func (s *subscription) end() {
	s.once.Do(s.closeChannels)
}

func (s *subscription) closeChannels() {
	// The hub holds its lock while sending, so taking it here makes the close safe.
	s.hub.mu.Lock()
	close(s.updates)
	s.hub.mu.Unlock()

	close(s.finished)
}

func unique(tokens []domain.TokenID) []domain.TokenID {
	seen := make(map[domain.TokenID]bool, len(tokens))
	out := make([]domain.TokenID, 0, len(tokens))

	for _, t := range tokens {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}

	return out
}
