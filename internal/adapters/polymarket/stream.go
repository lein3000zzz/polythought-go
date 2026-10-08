package polymarket

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.MarketStream = (*Stream)(nil)

var ErrStreamStarted = errors.New("polymarket: stream already started")

// Stream translates the market channel into domain updates. It adds no buffer of its
// own: the client buffers and counts drops, so there is one place where events are lost.
type Stream struct {
	client  pm.MarketStreamClient
	updates chan domain.MarketUpdate
	started atomic.Bool
}

func NewStream(client pm.MarketStreamClient) *Stream {
	return &Stream{client: client, updates: make(chan domain.MarketUpdate)}
}

func (s *Stream) Updates() <-chan domain.MarketUpdate {
	return s.updates
}

func (s *Stream) Subscribe(ctx context.Context, tokenIDs ...domain.TokenID) error {
	return s.client.Subscribe(ctx, toStrings(tokenIDs)...)
}

func (s *Stream) Unsubscribe(ctx context.Context, tokenIDs ...domain.TokenID) error {
	return s.client.Unsubscribe(ctx, toStrings(tokenIDs)...)
}

// Run drives the client and forwards translated events until the client stops.
// Event types the domain does not model are skipped.
func (s *Stream) Run(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return ErrStreamStarted
	}
	defer close(s.updates)

	errc := make(chan error, 1)
	go func() { errc <- s.client.Run(ctx) }()

	// Keep draining after cancellation so the client never blocks while shutting down.
	for event := range s.client.Events() {
		update, ok := toUpdate(event)
		if !ok || ctx.Err() != nil {
			continue
		}

		select {
		case s.updates <- update:
		case <-ctx.Done():
		}
	}

	return <-errc
}

func toUpdate(event pm.MarketEvent) (domain.MarketUpdate, bool) {
	switch e := event.(type) {
	case pm.BookEvent:
		return domain.BookSnapshot{OrderBook: domain.OrderBook{
			MarketID: domain.MarketID(e.Market),
			TokenID:  domain.TokenID(e.AssetID),
			Bids:     toLevels(e.Bids, true),
			Asks:     toLevels(e.Asks, false),
			TickSize: e.TickSize,
			At:       parseTime(e.Timestamp),
		}}, true

	case pm.PriceChangeEvent:
		changes := make([]domain.LevelChange, len(e.Changes))
		for i, c := range e.Changes {
			changes[i] = domain.LevelChange{
				TokenID: domain.TokenID(c.AssetID),
				Side:    toSide(c.Side),
				Price:   c.Price,
				Size:    c.Size,
				BestBid: c.BestBid,
				BestAsk: c.BestAsk,
			}
		}

		return domain.BookChanges{MarketID: domain.MarketID(e.Market), Changes: changes, At: parseTime(e.Timestamp)}, true

	case pm.LastTradePriceEvent:
		return domain.TradeUpdate{Trade: domain.Trade{
			MarketID: domain.MarketID(e.Market),
			TokenID:  domain.TokenID(e.AssetID),
			Side:     toSide(e.Side),
			Price:    e.Price,
			Size:     e.Size,
			At:       parseTime(e.Timestamp),
			TxHash:   e.TransactionHash,
		}}, true

	case pm.TickSizeChangeEvent:
		return domain.TickSizeChange{
			MarketID: domain.MarketID(e.Market),
			TokenID:  domain.TokenID(e.AssetID),
			TickSize: e.NewTickSize,
			At:       parseTime(e.Timestamp),
		}, true

	case pm.BestBidAskEvent:
		return domain.QuoteUpdate{
			MarketID: domain.MarketID(e.Market),
			TokenID:  domain.TokenID(e.AssetID),
			BestBid:  e.BestBid,
			BestAsk:  e.BestAsk,
			At:       parseTime(e.Timestamp),
		}, true

	case pm.MarketResolvedEvent:
		return domain.MarketResolution{
			MarketID:       domain.MarketID(e.Market),
			WinningTokenID: domain.TokenID(e.WinningAssetID),
			WinningOutcome: e.WinningOutcome,
			At:             parseTime(e.Timestamp),
		}, true

	default:
		return nil, false
	}
}
