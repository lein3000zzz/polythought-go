package ports

import (
	"context"
	"time"

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
	// AccessKey is the wallet and its API credentials, without a signing key: what
	// following, cancelling and heartbeats need, which go on once the wallet can no longer
	// sign (its session key expired, say).
	AccessKey(ctx context.Context, owner domain.UserID, wallet domain.WalletID) (OrderKey, error)
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

	// Resting ones (3p3): GTC or GTD, refused rather than matched at once when PostOnly;
	// a GTD's rest is cancelled at Expires.
	PostOnly bool
	Expires  time.Time
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
// to work out (D90): a maker pays none.
type LiveTrade struct {
	ID     string
	Status domain.FillStatus
	Fills  []domain.OrderFill
	Maker  bool // the order rested on the book and was matched
}

// LiveCancel is how cancelling an order went: cancelled, or why not.
type LiveCancel struct {
	Canceled bool
	Reason   string // Polymarket's words, when not
}

// LiveOrderEvent says a wallet's orders changed on Polymarket (3p3, D195): which ones, or
// after a Gap, any of them may have.
type LiveOrderEvent struct {
	Gap    bool
	Orders []string // their IDs on the CLOB
}

// LiveOrderStream follows a wallet's orders as they change.
type LiveOrderStream interface {
	// Watch streams k's wallet's order changes until ctx is done, a Gap first on every
	// connection; the channel is closed once ctx is done.
	Watch(ctx context.Context, k OrderKey) <-chan LiveOrderEvent
}

// LiveExchange places a wallet's orders on Polymarket's CLOB and follows them (3p).
type LiveExchange interface {
	// Place signs o with the key and sends it, settling a send without an answer by
	// looking the order up (D194). Signed, and before it is sent, it calls signed with the
	// order's ID, its hash: an error then stops it going, and is Place's (D233). The
	// error is for an order not sent at all.
	Place(ctx context.Context, k OrderKey, o LiveOrder, signed func(id string) error) (LivePlacement, error)
	// PlaceBatch signs each order and sends them together, as many to a request as
	// Polymarket takes (D244): how each went, in their order. Every order is signed, and
	// signed told its index and ID, before any goes; an error then sends none, and is
	// PlaceBatch's. An order whose request was not sent at all is refused, saying so.
	PlaceBatch(ctx context.Context, k OrderKey, os []LiveOrder, signed func(i int, id string) error) ([]LivePlacement, error)
	// Order is how the order with id stands; LiveNotFound when the CLOB has none.
	Order(ctx context.Context, k OrderKey, id string) (LiveOrderState, error)
	// Trades are the order's part in the trades with ids, of its outcome token; the CLOB
	// may not list a trade at once, so some may be missing.
	Trades(ctx context.Context, k OrderKey, order string, token domain.TokenID, ids []string) ([]LiveTrade, error)
	// Cancel cancels what is left of an order on the book.
	Cancel(ctx context.Context, k OrderKey, id string) (LiveCancel, error)
	// Heartbeat tells Polymarket the server is there, armed by the first (D214): last is
	// the ID the one before gave, empty to start; it gives the next.
	Heartbeat(ctx context.Context, k OrderKey, last string) (string, error)
	// Cash is the wallet's pUSD on the CLOB.
	Cash(ctx context.Context, k OrderKey) (decimal.Decimal, error)
}

// LiveTradeNotifier tells an owner a live order's trade failed on chain, undone in the
// books (D193).
type LiveTradeNotifier interface {
	LiveTradeFailed(ctx context.Context, user domain.UserID, o domain.PlacedOrder) error
}

// OrderEvents says what happens to a workflow's orders after the runs that placed them
// (D211, D215): the trading service's, for Order activity.
type OrderEvents interface {
	// OrderEvents streams the workflow's order events until ctx is done.
	OrderEvents(ctx context.Context, workflow domain.WorkflowID) <-chan domain.OrderEvent
}

// OrderCanceller cancels a workflow's own resting orders (D207), for Cancel orders.
type OrderCanceller interface {
	CancelOrders(ctx context.Context, c CancelRequest) ([]CancelResult, error)
}

// CancelRequest names the orders to cancel, of the workflow's that rest on the book: one
// by its ID, or those of a market or an outcome, or all of them.
type CancelRequest struct {
	WorkflowID domain.WorkflowID
	OwnerID    domain.UserID
	OrderID    domain.OrderID
	MarketID   domain.MarketID
	TokenID    domain.TokenID
	All        bool
}

// CancelResult is how cancelling one order went: the order as it is now, or why it was
// not cancelled.
type CancelResult struct {
	Order  domain.PlacedOrder
	Reason string // empty when cancelled
}

// WalletReleaser frees a wallet the owner unlinks (D218): which workflows trade live from
// it, and, confirmed, their resting orders cancelled and them back on paper.
type WalletReleaser interface {
	TradingLive(ctx context.Context, owner domain.UserID, wallet domain.WalletID) ([]string, error)
	ReleaseWallet(ctx context.Context, owner domain.UserID, wallet domain.WalletID) error
}

// LiveUsers are the accounts whose workflows trade live, for their kill switch and caps
// (D220, D222).
type LiveUsers interface {
	ByID(ctx context.Context, id domain.UserID) (domain.User, error)
	SetLiveStopped(ctx context.Context, id domain.UserID, at time.Time) error
}

// OrderNotifier hears what happens to orders, paper and live, to tell their owners as
// they chose (D229): it must not block.
type OrderNotifier interface {
	OrderHappened(e domain.OrderEvent)
}
