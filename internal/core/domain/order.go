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

// Scope is what a token or an agent needs to trade in the mode (D399).
func (m TradingMode) Scope() Scope {
	if m == ModeLive {
		return ScopeLive
	}

	return ScopePaper
}

// OrderType is how long an order may wait for the book.
type OrderType string

const (
	OrderFOK OrderType = "FOK" // fill wholly at once, or not at all
	OrderFAK OrderType = "FAK" // fill what the book has at once, cancel the rest
	OrderGTC OrderType = "GTC" // rest on the book until filled or cancelled
	OrderGTD OrderType = "GTD" // rest until filled, cancelled or its expiry

	// OrderSettle is no order placed: a holding closed at what its resolved market pays
	// a share, recorded as one (D106).
	OrderSettle OrderType = "SETTLE"
)

// OrderStatus is how an order ended, or stands.
type OrderStatus string

const (
	OrderFilled   OrderStatus = "filled"   // wholly
	OrderPartial  OrderStatus = "partial"  // in part, the rest cancelled (FAK)
	OrderUnfilled OrderStatus = "unfilled" // the book could not fill it, or not wholly (FOK)
	OrderRefused  OrderStatus = "refused"  // never placed: Reason says why
	// OrderPending is a live order sent whose outcome is not known yet: held in its
	// market's delay, or sent without an answer. It is followed (D204).
	OrderPending OrderStatus = "pending"
	// OrderFailed is a live order that matched, but whose trades failed on chain: undone
	// in the books (D193).
	OrderFailed OrderStatus = "failed"

	// OrderOpen rests on the book, a GTC or GTD, filled in part or not yet (3p3); then
	// filled, cancelled or expired, keeping what it filled.
	OrderOpen     OrderStatus = "open"
	OrderCanceled OrderStatus = "canceled"
	OrderExpired  OrderStatus = "expired"
)

// Rests is whether an order of type t may rest on the book.
func (t OrderType) Rests() bool {
	return t == OrderGTC || t == OrderGTD
}

// FillStatus is how far a live fill's trade has come on chain (D193). Paper fills have
// none: they are final at once.
type FillStatus string

const (
	FillMatched   FillStatus = "matched"  // matched by the CLOB, on its way on chain
	FillMined     FillStatus = "mined"    // in a block, not final yet
	FillRetrying  FillStatus = "retrying" // failed for now; it may yet be mined
	FillConfirmed FillStatus = "confirmed"
	FillFailed    FillStatus = "failed" // undone in the books
)

// Final is whether the fill will not change again.
func (s FillStatus) Final() bool {
	return s == "" || s == FillConfirmed || s == FillFailed
}

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
	// By is the agent or token behind its run when it was placed (D408): zero for the user.
	By Actor

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

	Expires time.Time // GTD only: when what is left of it is cancelled
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

	// A live order's wallet, and its ID on the CLOB: its hash, known before it is sent.
	WalletID WalletID
	ClobID   string
}

// OrderFill is part of an order matched against one price level of the book.
type OrderFill struct {
	Price   decimal.Decimal
	Shares  decimal.Decimal
	Dollars decimal.Decimal // shares times price
	Fee     decimal.Decimal // in dollars, on top of a buy, out of a sell (D90)

	// A live fill's trade on the CLOB, and how far it has come.
	TradeID string
	Status  FillStatus

	// At is when it filled: a live trade's match time, or when a paper order filled.
	// Zero for fills recorded before 3r: their order's time stands for it.
	At time.Time
}

// Totals sums an order's fills, but those whose trades failed.
func (o PlacedOrder) Totals() (shares, dollars, fees decimal.Decimal) {
	for _, f := range o.Fills {
		if f.Status == FillFailed {
			continue
		}

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
	StartedAt   time.Time // when it opened, or was last reset
	ResetAt     time.Time // zero: never reset (D107)
}

// TraderTotal is what a workflow's copies of one trader came to: the orders that filled,
// and the dollars they traded. An empty Trader is the orders copied from no one.
type TraderTotal struct {
	Trader WalletAddress
	Orders int
	Volume decimal.Decimal

	// WorkflowID and Mode say whose they are, where totals of many workflows come
	// together (D429).
	WorkflowID WorkflowID
	Mode       TradingMode
}

// Holding is a workflow's position in one outcome, copied from one trader or from no one
// (D92), in one mode.
type Holding struct {
	WorkflowID  WorkflowID
	OwnerID     UserID
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
	NegRisk      bool // its orders are signed for the neg risk exchange
}

// FeeSchedule is a market's taker fee: shares × rate × (p × (1 − p))^exponent, in
// dollars, rounded to 5 decimals. Makers pay none.
type FeeSchedule struct {
	Rate     decimal.Decimal // zero: no fees
	Exponent decimal.Decimal
}

// WorkflowTrading is how a workflow trades (D191): on paper, or live from one of its
// owner's wallets. A workflow that never said trades on paper (D22).
type WorkflowTrading struct {
	WorkflowID WorkflowID
	Mode       TradingMode
	WalletID   WalletID // the wallet live orders spend; it may stay set on paper
	UpdatedAt  time.Time
}

var (
	// ErrLiveTradingOff means the instance takes no live orders: an admin turns Live
	// trading on (D192).
	ErrLiveTradingOff = errors.New("live trading is off on this server")
	// ErrNotConfirmed means switching to live was not confirmed (D201).
	ErrNotConfirmed = errors.New("switching to live trading must be confirmed")
	// ErrWalletCannotTrade means a wallet has no key to sign orders with: an imported
	// private key or a session key (D153).
	ErrWalletCannotTrade = errors.New("the wallet cannot trade: import its private key or authorise a session key")
	// ErrHoldsLive means a workflow's wallet cannot change while it holds live shares
	// (D202).
	ErrHoldsLive = errors.New("the workflow holds live shares: its wallet changes once they are sold or settled")
	// ErrOrdersLive means a workflow's wallet cannot change while its live orders rest on
	// the book or are pending from it (R7).
	ErrOrdersLive = errors.New("the workflow has live orders resting or pending from its wallet: its wallet changes once they are done or cancelled")
	// ErrNotOpen means an order is not resting on the book: there is nothing to cancel.
	ErrNotOpen = errors.New("the order is not resting on the book")
	// ErrWalletTradesLive means a wallet cannot be unlinked while a workflow trades live
	// from it.
	ErrWalletTradesLive = errors.New("workflows trade live from this wallet")
)

// OrderEventKind is what happened to an order after the run that placed it (D215), or
// as it was placed, for notifications (D229).
type OrderEventKind string

const (
	OrderEventFilled   OrderEventKind = "filled"   // it filled, in part or wholly
	OrderEventCanceled OrderEventKind = "canceled" // what was left of it was cancelled
	OrderEventExpired  OrderEventKind = "expired"  // a GTD's time came
	OrderEventFailed   OrderEventKind = "failed"   // a live trade of it failed on chain, undone (D193)

	// As it was placed, or a pending one once known; Order activity does not see these.
	OrderEventRefused  OrderEventKind = "refused"  // refused, before it was sent or by Polymarket
	OrderEventUnfilled OrderEventKind = "unfilled" // a FAK or FOK the book could not fill
	OrderEventRested   OrderEventKind = "rested"   // it rests on the book
)

// OrderEvent is something that happened to one of a workflow's orders (D211): the order
// as it is now, and for fills and failures, the fills it was about.
type OrderEvent struct {
	Kind  OrderEventKind
	Order PlacedOrder
	Fills []OrderFill
	At    time.Time
}
