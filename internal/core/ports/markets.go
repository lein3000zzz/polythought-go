package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

type MarketFilter struct {
	Limit     int
	Cursor    string
	Closed    *bool // nil means open markets only, even when filtering by ID
	MarketIDs []domain.MarketID
	Slugs     []string
	TagIDs    []string
	TokenIDs  []domain.TokenID // the markets of these outcome tokens
}

type EventFilter struct {
	Limit    int
	Cursor   string
	Closed   *bool
	Featured *bool
	Title    string // substring match
}

// MarketReader looks up markets. Get methods find closed markets too; unknown markets
// return domain.ErrNotFound.
type MarketReader interface {
	ListMarkets(ctx context.Context, f MarketFilter) (Page[domain.Market], error)
	GetMarket(ctx context.Context, id domain.MarketID) (domain.Market, error)
	GetMarketBySlug(ctx context.Context, slug string) (domain.Market, error)
}

// EventReader looks up events. Lookups of unknown events return domain.ErrNotFound.
type EventReader interface {
	ListEvents(ctx context.Context, f EventFilter) (Page[domain.Event], error)
	GetEvent(ctx context.Context, id domain.EventID) (domain.Event, error)
	GetEventBySlug(ctx context.Context, slug string) (domain.Event, error)
}
