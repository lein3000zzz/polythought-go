package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// ActivityStream is every trade on Polymarket as it happens, for picking out the wallets
// that are followed (D87).
type ActivityStream interface {
	// Run streams until ctx is done. It may run again once it has returned, so the
	// stream need only be open while someone follows a trader.
	Run(ctx context.Context) error
	Events() <-chan ActivityEvent
}

// ActivityEvent is a fill, the taking order of a transaction, or the stream connecting.
type ActivityEvent struct {
	Fill domain.Fill

	// Taker marks Fill as the transaction's taking order, sent once more besides its
	// fill: who took, so the other fills of the transaction were makers'.
	Taker bool

	// Connected says the stream has just connected, again or for the first time: what
	// happened before, while it was down, is not on it.
	Connected bool
}

// TraderFeed follows wallets' trades for everyone who asks, over one stream.
type TraderFeed interface {
	// Follow delivers wallets' trades until the subscription is closed or ctx is done.
	// With since set, it first looks back to it for trades it has not delivered yet.
	Follow(ctx context.Context, wallets []domain.WalletAddress, since time.Time) (TraderSubscription, error)
}

type TraderSubscription interface {
	// Trades delivers trades a batch at a time, those settled together. It is closed
	// once the subscription ends.
	Trades() <-chan []domain.TraderTrade
	Close()
}
