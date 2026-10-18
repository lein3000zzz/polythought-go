package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

type TradeFilter struct {
	Wallet    domain.WalletAddress
	MarketIDs []domain.MarketID
	Side      domain.Side // empty means both
	Since     time.Time   // zero means unbounded
	Until     time.Time
	Limit     int
	Cursor    string

	// Makers includes the wallet's resting orders that filled, which are left out
	// otherwise: only the orders it placed against the book.
	Makers bool
}

type PositionFilter struct {
	Wallet    domain.WalletAddress
	MarketIDs []domain.MarketID
	Limit     int
	Cursor    string
}

// PriceHistoryFilter selects a time range. Zero Since and Until mean the whole history.
type PriceHistoryFilter struct {
	TokenID domain.TokenID
	Since   time.Time
	Until   time.Time // exclusive
	Bucket  time.Duration
	Limit   int
	Cursor  string
}

type TradeReader interface {
	Trades(ctx context.Context, f TradeFilter) (Page[domain.Trade], error)
}

// PositionReader needs a wallet or market IDs in the filter.
type PositionReader interface {
	Positions(ctx context.Context, f PositionFilter) (Page[domain.Position], error)
}

// PortfolioReader says what a wallet's open positions are worth now, its cash aside.
type PortfolioReader interface {
	PortfolioValue(ctx context.Context, wallet domain.WalletAddress) (decimal.Decimal, error)
}

type PriceHistoryReader interface {
	PriceHistory(ctx context.Context, f PriceHistoryFilter) (Page[domain.PricePoint], error)
}
