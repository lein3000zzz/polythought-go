package ports

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// WorkflowTradingRepository keeps how workflows trade (D191).
type WorkflowTradingRepository interface {
	// Trading is domain.ErrNotFound for a workflow that never said: it trades on paper.
	Trading(ctx context.Context, workflow domain.WorkflowID) (domain.WorkflowTrading, error)
	SetTrading(ctx context.Context, t domain.WorkflowTrading) error
	// TradingFrom are the workflows whose wallet is this one, whatever their mode.
	TradingFrom(ctx context.Context, wallet domain.WalletID) ([]domain.WorkflowTrading, error)
}

// OrderKey is what signs and sends a wallet's orders: its API credentials, and its
// imported private key or its session key (D153), decrypted in memory only.
type OrderKey struct {
	Wallet      domain.Wallet
	Credentials domain.PolymarketCredentials
	Key         string
}

// OrderKeys gives the trading service a wallet's key; domain.ErrWalletCannotTrade for a
// wallet without one.
type OrderKeys interface {
	OrderKey(ctx context.Context, owner domain.UserID, wallet domain.WalletID) (OrderKey, error)
}

// LiveOrder is an order a wallet places on Polymarket: FOK or FAK in 3p2.
type LiveOrder struct {
	TokenID  domain.TokenID
	Side     domain.Side
	Type     domain.OrderType
	Price    decimal.Decimal // the limit
	Shares   decimal.Decimal // or, for a buy, Dollars
	Dollars  decimal.Decimal
	TickSize decimal.Decimal
	NegRisk  bool
	Builder  string // a builder code, bytes32 hex; empty for none (D203)
}

// LiveState is how far a live order is.
type LiveState string

const (
	LiveMatched  LiveState = "matched"  // matched, wholly or in part; done
	LiveResting  LiveState = "resting"  // on the book
	LiveDelayed  LiveState = "delayed"  // held in its market's delay
	LiveDone     LiveState = "done"     // off the book: cancelled, killed or closed with its market
	LiveRefused  LiveState = "refused"  // never placed
	LiveUnknown  LiveState = "unknown"  // sent without an answer, and not found: it may yet be placed
	LiveNotFound LiveState = "notFound" // looked up, and the CLOB has none
)

// LivePlacement is how sending a live order went.
type LivePlacement struct {
	ID       string // the order's ID on the CLOB, its hash
	State    LiveState
	Reason   string   // why it was refused, in the CLOB's words
	TradeIDs []string // its trades so far
}

// LiveOrderState is how a placed order stands on the CLOB now.
type LiveOrderState struct {
	State    LiveState
	Matched  decimal.Decimal // shares
	TradeIDs []string
}

// LiveTrade is one of an order's trades: its part, as fills at each maker's price in the
// order's own outcome, and how far it has come on chain. Fees are the trading service's
// to work out (D90).
type LiveTrade struct {
	ID     string
	Status domain.FillStatus
	Fills  []domain.OrderFill
}

// LiveExchange places a wallet's orders on Polymarket's CLOB and follows them (3p).
type LiveExchange interface {
	// Place signs o with the key and sends it, settling a send without an answer by
	// looking the order up (D194). The error is for an order not sent at all.
	Place(ctx context.Context, k OrderKey, o LiveOrder) (LivePlacement, error)
	// Order is how the order with id stands; LiveNotFound when the CLOB has none.
	Order(ctx context.Context, k OrderKey, id string) (LiveOrderState, error)
	// Trades are those with ids of a taker order in token; the CLOB may not list a trade
	// at once, so some may be missing.
	Trades(ctx context.Context, k OrderKey, token domain.TokenID, ids []string) ([]LiveTrade, error)
	// Cash is the wallet's pUSD on the CLOB.
	Cash(ctx context.Context, k OrderKey) (decimal.Decimal, error)
}

// LiveTradeNotifier tells an owner a live order's trade failed on chain, undone in the
// books (D193).
type LiveTradeNotifier interface {
	LiveTradeFailed(ctx context.Context, user domain.UserID, o domain.PlacedOrder) error
}
