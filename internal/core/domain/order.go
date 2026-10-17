package domain

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

type OrderID string

// TradingMode says whether orders spend real money.
type TradingMode string

const (
	ModePaper TradingMode = "paper" // simulated against the live book, with a paper account (D89)
	ModeLive  TradingMode = "live"
)

// OrderType is how long an order may wait for the book.
type OrderType string

const (
	OrderFOK OrderType = "FOK" // fill wholly at once, or not at all
	OrderFAK OrderType = "FAK" // fill what the book has at once, cancel the rest
	OrderGTC OrderType = "GTC" // rest on the book until filled or cancelled
	OrderGTD OrderType = "GTD" // rest until filled, cancelled or its expiry
)

// OrderStatus is how an order ended, or stands.
type OrderStatus string

const (
	OrderFilled   OrderStatus = "filled"   // wholly
	OrderPartial  OrderStatus = "partial"  // in part, the rest cancelled (FAK)
	OrderUnfilled OrderStatus = "unfilled" // the book could not fill it, or not wholly (FOK)
	OrderRefused  OrderStatus = "refused"  // never placed: Reason says why
)

var (
	ErrNotEnoughCash   = errors.New("not enough cash for the order")
	ErrNotEnoughShares = errors.New("not enough shares for the order")
)

// OrderRequest is an order a workflow places.
type OrderRequest struct {
	WorkflowID  WorkflowID
	OwnerID     UserID
	ExecutionID ExecutionID // the run that placed it; empty when none
	NodeID      NodeID
	Mode        TradingMode

	// Trader is the wallet whose trade it copies, with their label then; empty for an
	// order copied from no one. Positions are kept per trader (D92).
	Trader      WalletAddress
	TraderLabel string

	TokenID  TokenID
	MarketID MarketID // empty: the token's market
	Side     Side
	Type     OrderType
	PostOnly bool

	// Price is the limit: the most a buy pays per share, the least a sell takes.
	Price decimal.Decimal

	// Shares to buy or sell; or, for a buy, zero with Dollars to spend instead.
	Shares  decimal.Decimal
	Dollars decimal.Decimal

	Expires time.Time // GTD only
}

// PlacedOrder is an order a workflow placed, as the ledger keeps it: what was asked, and
// how it went. (An account's orders on the book, as Polymarket lists them, are Order.)
type PlacedOrder struct {
	OrderRequest

	ID        OrderID
	Status    OrderStatus
	Reason    string // why it was refused, or did not fill
	Fills     []OrderFill
	CreatedAt time.Time
}

// OrderFill is part of an order matched against one price level of the book.
type OrderFill struct {
	Price   decimal.Decimal
	Shares  decimal.Decimal
	Dollars decimal.Decimal // shares times price
	Fee     decimal.Decimal // in dollars, on top of a buy, out of a sell (D90)
}

// Totals sums an order's fills.
func (o PlacedOrder) Totals() (shares, dollars, fees decimal.Decimal) {
	for _, f := range o.Fills {
		shares = shares.Add(f.Shares)
		dollars = dollars.Add(f.Dollars)
		fees = fees.Add(f.Fee)
	}

	return shares, dollars, fees
}

// PaperAccount is a workflow's paper money (D89).
type PaperAccount struct {
	WorkflowID  WorkflowID
	OwnerID     UserID
	Cash        decimal.Decimal
	StartedWith decimal.Decimal
	StartedAt   time.Time
}

// Holding is a workflow's position in one outcome, copied from one trader or from no one
// (D92), in one mode.
type Holding struct {
	WorkflowID  WorkflowID
	Mode        TradingMode
	Trader      WalletAddress // empty: copied from no one
	TraderLabel string        // as of the last order
	TokenID     TokenID
	MarketID    MarketID
	Shares      decimal.Decimal
	Cost        decimal.Decimal // what the shares held cost, fees included
	Realized    decimal.Decimal // profit and loss of what was sold
	UpdatedAt   time.Time
}

// HoldingKey names a holding.
type HoldingKey struct {
	WorkflowID WorkflowID
	Mode       TradingMode
	Trader     WalletAddress
	TokenID    TokenID
}

func (h Holding) Key() HoldingKey {
	return HoldingKey{WorkflowID: h.WorkflowID, Mode: h.Mode, Trader: h.Trader, TokenID: h.TokenID}
}

// TradingRules are what a market's orders must keep to, and what its fills cost.
type TradingRules struct {
	MarketID     MarketID
	TickSize     decimal.Decimal // prices are multiples of it
	MinOrderSize decimal.Decimal // shares
	Fees         FeeSchedule
}

// FeeSchedule is a market's taker fee: shares × rate × (p × (1 − p))^exponent, in
// dollars, rounded to 5 decimals. Makers pay none.
type FeeSchedule struct {
	Rate     decimal.Decimal // zero: no fees
	Exponent decimal.Decimal
}
