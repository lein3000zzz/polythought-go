package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// OrderBookReader fetches current books. Unknown tokens are left out of the result,
// so it can be shorter than the input; match books by TokenID.
type OrderBookReader interface {
	OrderBooks(ctx context.Context, tokenIDs ...domain.TokenID) ([]domain.OrderBook, error)
}

// MarketStream pushes live updates for subscribed tokens. Run blocks until ctx is done
// and closes Updates when it returns. Subscriptions survive reconnects.
type MarketStream interface {
	Run(ctx context.Context) error
	Subscribe(ctx context.Context, tokenIDs ...domain.TokenID) error
	Unsubscribe(ctx context.Context, tokenIDs ...domain.TokenID) error
	Updates() <-chan domain.MarketUpdate
}
