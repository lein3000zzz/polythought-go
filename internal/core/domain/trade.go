package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type Side string

const (
	SideBuy  Side = "buy"
	SideSell Side = "sell"
)

// Trade sizes are in shares, prices in USD per share.
type Trade struct {
	MarketID MarketID
	TokenID  TokenID
	Outcome  string
	Side     Side
	Price    decimal.Decimal
	Size     decimal.Decimal
	Wallet   WalletAddress
	At       time.Time
	TxHash   string

	Title     string // the market's question
	Slug      string
	EventSlug string
	Name      string // the wallet's name on Polymarket, if it has one
}

type Position struct {
	Wallet        WalletAddress
	MarketID      MarketID
	TokenID       TokenID
	Title         string // the market's question
	Outcome       string
	Size          decimal.Decimal
	AvgPrice      decimal.Decimal
	CurrentPrice  decimal.Decimal
	Value         decimal.Decimal
	RealizedPnL   decimal.Decimal
	UnrealizedPnL decimal.Decimal
	Redeemable    bool
}

type PricePoint struct {
	At    time.Time
	Price decimal.Decimal
}
