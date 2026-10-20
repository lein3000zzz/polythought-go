package polymarket

import (
	"context"

	"github.com/shopspring/decimal"
)

// Page is one page of a cursor-paginated endpoint. NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
}

// GammaClient reads market metadata from gamma-api.polymarket.com.
type GammaClient interface {
	ListMarkets(ctx context.Context, q MarketsQuery) (Page[Market], error)
	ListEvents(ctx context.Context, q EventsQuery) (Page[Event], error)
	GetMarket(ctx context.Context, id string) (Market, error)
	GetMarketBySlug(ctx context.Context, slug string) (Market, error)
	GetEvent(ctx context.Context, id string) (Event, error)
	GetEventBySlug(ctx context.Context, slug string) (Event, error)
	ListTags(ctx context.Context, q TagsQuery) ([]Tag, error)
	Search(ctx context.Context, q SearchQuery) (SearchResult, error)
}

// ClobClient reads public order book data from clob.polymarket.com.
type ClobClient interface {
	GetOrderBooks(ctx context.Context, tokenIDs ...string) ([]OrderBook, error)
	GetPrices(ctx context.Context, reqs ...PriceRequest) (map[string]SidePrices, error)
	GetMidpoints(ctx context.Context, tokenIDs ...string) (map[string]decimal.Decimal, error)
	GetSpreads(ctx context.Context, tokenIDs ...string) (map[string]decimal.Decimal, error)
	GetLastTradePrices(ctx context.Context, tokenIDs ...string) ([]LastTrade, error)
	GetMarketInfo(ctx context.Context, conditionID string) (MarketInfo, error)
	ServerTime(ctx context.Context) (int64, error)
}

// ClobAccountClient reads an account's own CLOB data with L2 credentials. Credentials
// are passed per call, so one client serves many accounts.
type ClobAccountClient interface {
	// CreateOrDeriveAPIKey makes the account of auth's key an API key, or gives its key again.
	CreateOrDeriveAPIKey(ctx context.Context, auth L1Auth) (L2Credentials, error)
	// APIKeys lists the keys of the credentials' account; a 401 is ErrUnauthorized.
	APIKeys(ctx context.Context, creds L2Credentials) ([]string, error)
	OpenOrders(ctx context.Context, creds L2Credentials, q OpenOrdersQuery) (Page[OpenOrder], error)
	BalanceAllowance(ctx context.Context, creds L2Credentials, q BalanceQuery) (BalanceAllowance, error)
}

// ClobTradingClient places and cancels an account's orders and reads them back, with L2
// credentials. An order refused is an *OrderRefusedError; one sent without an answer and
// not found after, an *OrderUncertainError (D194).
type ClobTradingClient interface {
	// PostOrder places an order; sent again it is found, not placed twice.
	PostOrder(ctx context.Context, creds L2Credentials, p OrderPost) (PostedOrder, error)
	// PostOrders places 1 to MaxBatchOrders orders: how each went, in their order.
	PostOrders(ctx context.Context, creds L2Credentials, ps []OrderPost) ([]PostResult, error)
	// Order is one of the account's orders by its ID, whatever its status; ErrNotFound
	// when the CLOB has none.
	Order(ctx context.Context, creds L2Credentials, id string) (OpenOrder, error)
	Trades(ctx context.Context, creds L2Credentials, q ClobTradesQuery) (Page[ClobTrade], error)

	CancelOrder(ctx context.Context, creds L2Credentials, id string) (Cancellation, error)
	// CancelOrders cancels 1 to MaxBatchCancels orders.
	CancelOrders(ctx context.Context, creds L2Credentials, ids []string) (Cancellation, error)
	CancelAll(ctx context.Context, creds L2Credentials) (Cancellation, error)
	CancelMarketOrders(ctx context.Context, creds L2Credentials, q CancelMarketQuery) (Cancellation, error)
}

// DataClient reads positions, trades and analytics from data-api.polymarket.com/v2.
type DataClient interface {
	Positions(ctx context.Context, q PositionsQuery) (Page[Position], error)
	Trades(ctx context.Context, q TradesQuery) (Page[Trade], error)
	Activity(ctx context.Context, q ActivityQuery) (Page[Activity], error)
	Holders(ctx context.Context, q HoldersQuery) ([]TokenHolders, error)
	OpenInterest(ctx context.Context, conditionIDs ...string) ([]OpenInterest, error)
	PriceHistory(ctx context.Context, q PriceHistoryQuery) (Page[PricePoint], error)
	// Value is what a wallet's positions are worth now, its cash aside.
	Value(ctx context.Context, user string) (decimal.Decimal, error)
	// Approvals is what is indexed of a wallet's approvals: all of Polymarket's catalog.
	Approvals(ctx context.Context, user string) (ApprovalsSnapshot, error)
}

// MarketStreamClient streams the public CLOB market channel.
type MarketStreamClient interface {
	Run(ctx context.Context) error
	Subscribe(ctx context.Context, assetIDs ...string) error
	Unsubscribe(ctx context.Context, assetIDs ...string) error
	Events() <-chan MarketEvent
}
