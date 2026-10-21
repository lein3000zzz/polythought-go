package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Decimal is a decimal as exact text, like 0.42: parse it with a decimal library, not as
// a float, to keep it exact.
type Decimal string

// Market is one question on Polymarket, with its outcomes, and its event.
type Market struct {
	ID              string // the condition ID, 0x…
	Slug            string
	Question        string
	Description     string
	Outcomes        []Outcome
	Status          string // open, closed, resolved or archived
	AcceptingOrders bool
	NegRisk         bool
	TickSize        Decimal
	MinOrderSize    Decimal
	Volume24h       Decimal
	Liquidity       Decimal
	EndsAt          time.Time // zero when unknown
	Tags            []string
	Event           string // its event's title
	EventSlug       string
}

// Outcome is one of a market's answers: its token, and its price.
type Outcome struct {
	TokenID string
	Label   string
	Price   Decimal
}

// OrderBook is an outcome's live book, each side best first.
type OrderBook struct {
	MarketID string
	TokenID  string
	Bids     []Level
	Asks     []Level
	TickSize Decimal
	At       time.Time
}

type Level struct {
	Price Decimal
	Size  Decimal
}

// PriceQuery asks for an outcome's prices from Since until Until (now when zero), one
// every Bucket (a minute at least), up to Limit points (1000 when 0, and at most).
type PriceQuery struct {
	TokenID string
	Since   time.Time
	Until   time.Time
	Bucket  time.Duration
	Limit   int
}

type PricePoint struct {
	At    time.Time
	Price Decimal
}

// TradeQuery asks for recent trades in a market, of an address, or both, up to Limit
// (20 when 0, 100 at most).
type TradeQuery struct {
	MarketID string
	Address  string
	Limit    int
}

type Trade struct {
	MarketID  string
	TokenID   string
	Outcome   string
	Side      string // buy or sell
	Price     Decimal
	Size      Decimal
	Address   string // the trader's
	Name      string // the trader's name on Polymarket, if they have one
	At        time.Time
	TxHash    string
	Title     string // the market's question
	Slug      string
	EventSlug string
}

// Position is an address's open position in an outcome.
type Position struct {
	Address       string
	MarketID      string
	TokenID       string
	Title         string // the market's question
	Outcome       string
	Size          Decimal
	AvgPrice      Decimal
	CurrentPrice  Decimal
	Value         Decimal
	RealizedPnL   Decimal
	UnrealizedPnL Decimal
	Redeemable    bool
}

// Wallet is one of the workflow's owner's: its ID, as a wallet setting names it, its name
// and its address.
type Wallet struct {
	ID      string
	Name    string
	Address string
}

func (h *hostClient) Get(ctx context.Context, key string, v any) (bool, error) {
	if h.rpc == nil {
		return false, errNoHost
	}

	out, err := h.rpc.GetValue(ctx, &pluginv1.GetValueRequest{Session: h.session, Key: key})
	if err != nil {
		return false, fromHost(err)
	}

	if !out.GetFound() {
		return false, nil
	}

	if err := json.Unmarshal(out.GetValue(), v); err != nil {
		return true, fmt.Errorf("the value of %s: %w", key, err)
	}

	return true, nil
}

func (h *hostClient) Set(ctx context.Context, key string, v any) error {
	if h.rpc == nil {
		return errNoHost
	}

	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("the value of %s: %w", key, err)
	}

	if _, err := h.rpc.SetValue(ctx, &pluginv1.SetValueRequest{Session: h.session, Key: key, Value: raw}); err != nil {
		return fromHost(err)
	}

	return nil
}

func (h *hostClient) Delete(ctx context.Context, key string) error {
	if h.rpc == nil {
		return errNoHost
	}

	if _, err := h.rpc.DeleteValue(ctx, &pluginv1.DeleteValueRequest{Session: h.session, Key: key}); err != nil {
		return fromHost(err)
	}

	return nil
}

func (h *hostClient) Keys(ctx context.Context, prefix, after string, limit int) ([]string, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	out, err := h.rpc.ListKeys(ctx, &pluginv1.ListKeysRequest{Session: h.session, Prefix: prefix, After: after, Limit: int32(limit)}) //nolint:gosec // a page
	if err != nil {
		return nil, fromHost(err)
	}

	return out.GetKeys(), nil
}

func (h *hostClient) SearchMarkets(ctx context.Context, query string, closed bool, limit int) ([]Market, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	out, err := h.rpc.SearchMarkets(ctx, &pluginv1.SearchMarketsRequest{
		Session: h.session, Query: query, Closed: closed, Limit: int32(limit), //nolint:gosec // a page
	})
	if err != nil {
		return nil, fromHost(err)
	}

	markets := make([]Market, len(out.GetMarkets()))
	for i, m := range out.GetMarkets() {
		markets[i] = Market{
			ID: m.GetId(), Slug: m.GetSlug(), Question: m.GetQuestion(), Description: m.GetDescription(),
			Status: m.GetStatus(), AcceptingOrders: m.GetAcceptingOrders(), NegRisk: m.GetNegRisk(),
			TickSize: Decimal(m.GetTickSize()), MinOrderSize: Decimal(m.GetMinOrderSize()),
			Volume24h: Decimal(m.GetVolume_24H()), Liquidity: Decimal(m.GetLiquidity()), EndsAt: timeOf(m.GetEndsAt()),
			Tags: m.GetTags(), Event: m.GetEvent(), EventSlug: m.GetEventSlug(),
		}

		for _, o := range m.GetOutcomes() {
			markets[i].Outcomes = append(markets[i].Outcomes, Outcome{TokenID: o.GetTokenId(), Label: o.GetLabel(), Price: Decimal(o.GetPrice())})
		}
	}

	return markets, nil
}

func (h *hostClient) OrderBook(ctx context.Context, tokenID string) (OrderBook, error) {
	if h.rpc == nil {
		return OrderBook{}, errNoHost
	}

	out, err := h.rpc.GetOrderBook(ctx, &pluginv1.GetOrderBookRequest{Session: h.session, TokenId: tokenID})
	if err != nil {
		return OrderBook{}, fromHost(err)
	}

	b := out.GetBook()
	side := func(levels []*pluginv1.Level) []Level {
		out := make([]Level, len(levels))
		for i, l := range levels {
			out[i] = Level{Price: Decimal(l.GetPrice()), Size: Decimal(l.GetSize())}
		}

		return out
	}

	return OrderBook{
		MarketID: b.GetMarketId(), TokenID: b.GetTokenId(), Bids: side(b.GetBids()), Asks: side(b.GetAsks()),
		TickSize: Decimal(b.GetTickSize()), At: timeOf(b.GetAt()),
	}, nil
}

func (h *hostClient) PriceHistory(ctx context.Context, q PriceQuery) ([]PricePoint, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	in := &pluginv1.GetPriceHistoryRequest{
		Session: h.session, TokenId: q.TokenID, Since: textOf(q.Since), Until: textOf(q.Until),
		Limit: int32(q.Limit), //nolint:gosec // a page
	}
	if q.Bucket > 0 {
		in.Bucket = q.Bucket.String()
	}

	out, err := h.rpc.GetPriceHistory(ctx, in)
	if err != nil {
		return nil, fromHost(err)
	}

	points := make([]PricePoint, len(out.GetPoints()))
	for i, p := range out.GetPoints() {
		points[i] = PricePoint{At: timeOf(p.GetAt()), Price: Decimal(p.GetPrice())}
	}

	return points, nil
}

func (h *hostClient) Trades(ctx context.Context, q TradeQuery) ([]Trade, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	out, err := h.rpc.ListTrades(ctx, &pluginv1.ListTradesRequest{
		Session: h.session, MarketId: q.MarketID, Address: q.Address, Limit: int32(q.Limit), //nolint:gosec // a page
	})
	if err != nil {
		return nil, fromHost(err)
	}

	trades := make([]Trade, len(out.GetTrades()))
	for i, t := range out.GetTrades() {
		trades[i] = Trade{
			MarketID: t.GetMarketId(), TokenID: t.GetTokenId(), Outcome: t.GetOutcome(), Side: t.GetSide(),
			Price: Decimal(t.GetPrice()), Size: Decimal(t.GetSize()), Address: t.GetAddress(), Name: t.GetName(),
			At: timeOf(t.GetAt()), TxHash: t.GetTxHash(), Title: t.GetTitle(), Slug: t.GetSlug(), EventSlug: t.GetEventSlug(),
		}
	}

	return trades, nil
}

func (h *hostClient) Positions(ctx context.Context, address string, limit int) ([]Position, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	out, err := h.rpc.ListPositions(ctx, &pluginv1.ListPositionsRequest{
		Session: h.session, Address: address, Limit: int32(limit), //nolint:gosec // a page
	})
	if err != nil {
		return nil, fromHost(err)
	}

	positions := make([]Position, len(out.GetPositions()))
	for i, p := range out.GetPositions() {
		positions[i] = Position{
			Address: p.GetAddress(), MarketID: p.GetMarketId(), TokenID: p.GetTokenId(), Title: p.GetTitle(),
			Outcome: p.GetOutcome(), Size: Decimal(p.GetSize()), AvgPrice: Decimal(p.GetAvgPrice()),
			CurrentPrice: Decimal(p.GetCurrentPrice()), Value: Decimal(p.GetValue()),
			RealizedPnL: Decimal(p.GetRealizedPnl()), UnrealizedPnL: Decimal(p.GetUnrealizedPnl()), Redeemable: p.GetRedeemable(),
		}
	}

	return positions, nil
}

func (h *hostClient) Wallets(ctx context.Context) ([]Wallet, error) {
	if h.rpc == nil {
		return nil, errNoHost
	}

	out, err := h.rpc.ListWallets(ctx, &pluginv1.ListWalletsRequest{Session: h.session})
	if err != nil {
		return nil, fromHost(err)
	}

	wallets := make([]Wallet, len(out.GetWallets()))
	for i, w := range out.GetWallets() {
		wallets[i] = Wallet{ID: w.GetId(), Name: w.GetName(), Address: w.GetAddress()}
	}

	return wallets, nil
}

// timeOf reads a time the host sent, RFC 3339; zero when it sent none.
func timeOf(text string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, text)
	return t
}

func textOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
