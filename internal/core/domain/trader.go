package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// Fill is a wallet's part in one transaction on Polymarket, as the market's activity
// reports it: for the taker, its whole order; for a maker, one of its orders filled.
type Fill struct {
	Wallet    WalletAddress
	TxHash    string
	TokenID   TokenID
	MarketID  MarketID // empty when the source did not say: the token's market
	Outcome   string
	Title     string // the market's question
	Slug      string
	EventSlug string
	Side      Side
	Price     decimal.Decimal
	Size      decimal.Decimal // shares
	At        time.Time
	Name      string // the wallet's name on Polymarket, if it has one
}

// TradeRole is how a trader's order met the book.
type TradeRole string

const (
	RoleTaker   TradeRole = "taker" // it took liquidity: the order was placed against the book
	RoleMaker   TradeRole = "maker" // a resting order of theirs filled
	RoleUnknown TradeRole = ""      // the source did not say
)

// TraderTrade is a followed wallet's trade (D84): its fills in one transaction, outcome
// and side, summed, so an order that filled against many makers is one trade.
type TraderTrade struct {
	Wallet    WalletAddress
	TxHash    string
	TokenID   TokenID
	MarketID  MarketID
	Outcome   string
	Title     string
	Slug      string
	EventSlug string
	Side      Side
	Shares    decimal.Decimal
	Dollars   decimal.Decimal
	Price     decimal.Decimal // the average: dollars over shares
	Fills     int
	At        time.Time // of its first fill
	Name      string
	Role      TradeRole
}
