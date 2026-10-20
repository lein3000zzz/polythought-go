package trading

import (
	"context"
	"slices"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// What happens to a workflow's orders after the runs that placed them (D211, D215), for
// Order activity: in memory, to whoever listens now; nothing is kept for later.

var _ ports.OrderEvents = (*Service)(nil)

// eventBuffer is how many events a listener may be behind; more are dropped, logged.
const eventBuffer = 64

// OrderEvents streams the workflow's order events until ctx is done.
func (s *Service) OrderEvents(ctx context.Context, w domain.WorkflowID) <-chan domain.OrderEvent {
	ch := make(chan domain.OrderEvent, eventBuffer)

	s.mu.Lock()
	if s.listeners == nil {
		s.listeners = map[domain.WorkflowID]map[chan domain.OrderEvent]struct{}{}
	}

	if s.listeners[w] == nil {
		s.listeners[w] = map[chan domain.OrderEvent]struct{}{}
	}

	s.listeners[w][ch] = struct{}{}
	s.mu.Unlock()

	go func() {
		<-ctx.Done()

		s.mu.Lock()
		delete(s.listeners[w], ch)
		close(ch)
		s.mu.Unlock()
	}()

	return ch
}

// publish tells the workflow's listeners of what happened to one of its orders.
func (s *Service) publish(ctx context.Context, events ...domain.OrderEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range events {
		for ch := range s.listeners[e.Order.WorkflowID] {
			select {
			case ch <- e:
			default:
				s.d.Logger.Warn(ctx, "Order event dropped: its listener is behind", "workflow_id", e.Order.WorkflowID,
					"order_id", e.Order.ID, "kind", e.Kind)
			}
		}
	}
}

// changes are what following an order found happened to it: fills that came, fills that
// failed, and its end.
func (s *Service) changes(before, after domain.PlacedOrder) []domain.OrderEvent {
	at := s.d.Now().UTC()

	var (
		out    []domain.OrderEvent
		filled []domain.OrderFill
		failed []domain.OrderFill
	)

	known := map[string]domain.OrderFill{}
	for _, f := range before.Fills {
		known[fillKey(f)] = f
	}

	for _, f := range after.Fills {
		was, ok := known[fillKey(f)]

		switch {
		case !ok && f.Status != domain.FillFailed:
			filled = append(filled, f)
		case f.Status == domain.FillFailed && (!ok || was.Status != domain.FillFailed):
			failed = append(failed, f)
		}
	}

	if len(filled) > 0 {
		out = append(out, domain.OrderEvent{Kind: domain.OrderEventFilled, Order: after, Fills: filled, At: at})
	}

	if len(failed) > 0 {
		out = append(out, domain.OrderEvent{Kind: domain.OrderEventFailed, Order: after, Fills: failed, At: at})
	}

	if before.Status != after.Status && slices.Contains([]domain.OrderStatus{domain.OrderCanceled, domain.OrderExpired}, after.Status) {
		kind := domain.OrderEventCanceled
		if after.Status == domain.OrderExpired {
			kind = domain.OrderEventExpired
		}

		out = append(out, domain.OrderEvent{Kind: kind, Order: after, At: at})
	}

	return out
}

// fillKey tells a live fill apart by its trade and price; a paper fill has neither, so
// paper fills are told by their place.
func fillKey(f domain.OrderFill) string {
	return f.TradeID + "@" + f.Price.String() + "×" + f.Shares.String()
}
