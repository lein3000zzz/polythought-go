package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// MarketUpdate is one change pushed by a live market feed. Switch on the concrete type.
type MarketUpdate interface {
	isMarketUpdate()
}

// BookSnapshot replaces any book state held for its token. Feeds send one on
// subscribe and after every reconnect.
type BookSnapshot struct {
	OrderBook
}

// LevelChange sets one price level. A zero Size removes the level.
type LevelChange struct {
	TokenID TokenID
	Side    Side
	Price   decimal.Decimal
	Size    decimal.Decimal
	BestBid decimal.Decimal
	BestAsk decimal.Decimal
}

// BookChanges are level updates for one market, applied in order on top of the last snapshot.
type BookChanges struct {
	MarketID MarketID
	Changes  []LevelChange
	At       time.Time
}

// TradeUpdate is a fill on the book. Feeds do not identify the wallet.
type TradeUpdate struct {
	Trade
}

type TickSizeChange struct {
	MarketID MarketID
	TokenID  TokenID
	TickSize decimal.Decimal
	At       time.Time
}

type QuoteUpdate struct {
	MarketID MarketID
	TokenID  TokenID
	BestBid  decimal.Decimal
	BestAsk  decimal.Decimal
	At       time.Time
}

type MarketResolution struct {
	MarketID       MarketID
	WinningTokenID TokenID
	WinningOutcome string
	At             time.Time
}

func (BookSnapshot) isMarketUpdate()     {}
func (BookChanges) isMarketUpdate()      {}
func (TradeUpdate) isMarketUpdate()      {}
func (TickSizeChange) isMarketUpdate()   {}
func (QuoteUpdate) isMarketUpdate()      {}
func (MarketResolution) isMarketUpdate() {}
