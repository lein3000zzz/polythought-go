package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

var two = decimal.NewFromInt(2)

type Level struct {
	Price decimal.Decimal
	Size  decimal.Decimal
}

// OrderBook levels are sorted best first on both sides.
type OrderBook struct {
	MarketID MarketID
	TokenID  TokenID
	Bids     []Level
	Asks     []Level
	TickSize decimal.Decimal
	At       time.Time
}

func (b OrderBook) BestBid() (Level, bool) {
	if len(b.Bids) == 0 {
		return Level{}, false
	}

	return b.Bids[0], true
}

func (b OrderBook) BestAsk() (Level, bool) {
	if len(b.Asks) == 0 {
		return Level{}, false
	}

	return b.Asks[0], true
}

// Mid is undefined unless both sides have liquidity.
func (b OrderBook) Mid() (decimal.Decimal, bool) {
	bid, okBid := b.BestBid()
	ask, okAsk := b.BestAsk()
	if !okBid || !okAsk {
		return decimal.Zero, false
	}

	return bid.Price.Add(ask.Price).Div(two), true
}

func (b OrderBook) Spread() (decimal.Decimal, bool) {
	bid, okBid := b.BestBid()
	ask, okAsk := b.BestAsk()
	if !okBid || !okAsk {
		return decimal.Zero, false
	}

	return ask.Price.Sub(bid.Price), true
}
