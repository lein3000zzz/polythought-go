package trading

import (
	"context"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// The order log (D228): every live order of a user's workflows, deleted ones' too
// (D199), as the record of the real money they spent.

// exportMost is how many orders a download holds at most, the newest.
const exportMost = 10_000

// LogEntry is an order in the log, its market and outcome named when known.
type LogEntry struct {
	ports.LoggedOrder

	Question, Outcome string
}

// OrderLog is a page of the user's live orders, the newest first, as f narrows them.
func (s *Service) OrderLog(ctx context.Context, p auth.Principal, f ports.OrderLogFilter) (ports.Page[LogEntry], error) {
	f.Mode = domain.ModeLive

	page, err := s.d.Ledger.OwnerOrders(ctx, p.User.ID, f)
	if err != nil {
		return ports.Page[LogEntry]{}, err
	}

	return ports.Page[LogEntry]{Items: s.named(ctx, page.Items), NextCursor: page.NextCursor}, nil
}

// ExportOrders is every live order of the user f keeps, the newest exportMost, for a
// download.
func (s *Service) ExportOrders(ctx context.Context, p auth.Principal, f ports.OrderLogFilter) ([]LogEntry, error) {
	f.Mode, f.Limit, f.Cursor = domain.ModeLive, 200, ""

	var out []LogEntry

	for len(out) < exportMost {
		page, err := s.d.Ledger.OwnerOrders(ctx, p.User.ID, f)
		if err != nil {
			return nil, err
		}

		out = append(out, s.named(ctx, page.Items)...)

		if f.Cursor = page.NextCursor; f.Cursor == "" {
			break
		}
	}

	return out[:min(len(out), exportMost)], nil
}

// named names each order's market and outcome, looking each market up once.
func (s *Service) named(ctx context.Context, orders []ports.LoggedOrder) []LogEntry {
	markets := map[domain.MarketID]*domain.Market{}
	out := make([]LogEntry, len(orders))

	for i, o := range orders {
		out[i] = LogEntry{LoggedOrder: o}

		if o.MarketID == "" || s.d.Markets == nil {
			continue
		}

		m, seen := markets[o.MarketID]
		if !seen {
			got, err := s.d.Markets.GetMarket(ctx, o.MarketID)
			if err != nil {
				s.d.Logger.Debug(ctx, "Order's market not named", "market_id", o.MarketID, logging.Err(err))
			} else {
				m = &got
			}

			markets[o.MarketID] = m
		}

		if m == nil {
			continue
		}

		out[i].Question = m.Question
		if outcome, ok := m.Outcome(o.TokenID); ok {
			out[i].Outcome = outcome.Label
		}
	}

	return out
}
