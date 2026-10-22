package pluginhost

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Data is what plugins may read: Polymarket's public data, any plugin (D290, D292), and
// the owner's wallets, with the wallets permission (D301). One left nil makes its calls
// fail.
type Data struct {
	Events    ports.EventReader
	Books     ports.OrderBookReader
	History   ports.PriceHistoryReader
	Trades    ports.TradeReader
	Positions ports.PositionReader
	Wallets   WalletLister
}

// WalletLister lists a user's wallets.
type WalletLister interface {
	ListForUser(ctx context.Context, user domain.UserID) ([]domain.Wallet, error)
}

const (
	maxResults = 100  // markets, trades or positions a call gives at most; 20 when 0
	maxPoints  = 1000 // price points a call gives at most, and when 0
)

var errNoSource = status.Error(codes.Unavailable, "this is not available here")

func (a *api) SearchMarkets(ctx context.Context, req *pluginv1.SearchMarketsRequest) (*pluginv1.SearchMarketsResponse, error) {
	return call(ctx, a, req.GetSession(), opSearchMarkets, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.SearchMarketsResponse, error) {
		query := strings.TrimSpace(req.GetQuery())

		switch {
		case a.deps.Data.Events == nil:
			return nil, errNoSource
		case query == "":
			return nil, status.Error(codes.InvalidArgument, "a query: say what to look for")
		}

		found, err := markets.FindMarkets(ctx, a.deps.Data.Events, query, page(req.GetLimit(), 20, maxResults), req.GetClosed())
		if err != nil {
			return nil, a.sourceError(ctx, s, "search markets", err)
		}

		out := &pluginv1.SearchMarketsResponse{Markets: make([]*pluginv1.Market, len(found))}
		for i, f := range found {
			out.Markets[i] = marketOf(f)
		}

		return out, nil
	})
}

func marketOf(f markets.Found) *pluginv1.Market {
	m := f.Market

	out := &pluginv1.Market{
		Id: string(m.ID), Slug: m.Slug, Question: m.Question, Description: m.Description, Status: string(m.Status),
		AcceptingOrders: m.AcceptingOrders, NegRisk: m.NegRisk, TickSize: text(m.TickSize), MinOrderSize: text(m.MinOrderSize),
		Volume_24H: text(m.Volume24h), Liquidity: text(m.Liquidity), EndsAt: timeText(m.EndsAt), Tags: m.Tags,
		Event: f.Event.Title, EventSlug: f.Event.Slug,
	}

	for _, o := range m.Outcomes {
		out.Outcomes = append(out.Outcomes, &pluginv1.Outcome{TokenId: string(o.TokenID), Label: o.Label, Price: text(o.Price)})
	}

	return out
}

func (a *api) GetOrderBook(ctx context.Context, req *pluginv1.GetOrderBookRequest) (*pluginv1.GetOrderBookResponse, error) {
	return call(ctx, a, req.GetSession(), opOrderBook, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.GetOrderBookResponse, error) {
		token := strings.TrimSpace(req.GetTokenId())

		switch {
		case a.deps.Data.Books == nil:
			return nil, errNoSource
		case token == "":
			return nil, status.Error(codes.InvalidArgument, "a token ID: name the outcome")
		}

		books, err := a.deps.Data.Books.OrderBooks(ctx, domain.TokenID(token))
		if err != nil {
			return nil, a.sourceError(ctx, s, "order book", err)
		}

		if len(books) == 0 {
			return nil, status.Errorf(codes.NotFound, "no order book for token %s", token)
		}

		b := books[0]
		side := func(levels []domain.Level) []*pluginv1.Level {
			out := make([]*pluginv1.Level, len(levels))
			for i, l := range levels {
				out[i] = &pluginv1.Level{Price: text(l.Price), Size: text(l.Size)}
			}

			return out
		}

		return &pluginv1.GetOrderBookResponse{Book: &pluginv1.OrderBook{
			MarketId: string(b.MarketID), TokenId: string(b.TokenID), Bids: side(b.Bids), Asks: side(b.Asks),
			TickSize: text(b.TickSize), At: timeText(b.At),
		}}, nil
	})
}

func (a *api) GetPriceHistory(ctx context.Context, req *pluginv1.GetPriceHistoryRequest) (*pluginv1.GetPriceHistoryResponse, error) {
	return call(ctx, a, req.GetSession(), opPriceHistory, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.GetPriceHistoryResponse, error) {
		if a.deps.Data.History == nil {
			return nil, errNoSource
		}

		f, err := historyFilter(req, time.Now())
		if err != nil {
			return nil, err
		}

		got, err := a.deps.Data.History.PriceHistory(ctx, f)
		if err != nil {
			return nil, a.sourceError(ctx, s, "price history", err)
		}

		points := slices.SortedFunc(slices.Values(got.Items), func(a, b domain.PricePoint) int { return a.At.Compare(b.At) })
		points = points[:min(len(points), f.Limit)]

		out := &pluginv1.GetPriceHistoryResponse{Points: make([]*pluginv1.PricePoint, len(points))}
		for i, p := range points {
			out.Points[i] = &pluginv1.PricePoint{At: timeText(p.At), Price: text(p.Price)}
		}

		return out, nil
	})
}

// historyFilter reads what a plugin asks of price history: a token, since when, until
// when (now when empty), in buckets of a minute at least, as many as the points it may
// have when it says none.
func historyFilter(req *pluginv1.GetPriceHistoryRequest, now time.Time) (ports.PriceHistoryFilter, error) {
	f := ports.PriceHistoryFilter{TokenID: domain.TokenID(strings.TrimSpace(req.GetTokenId())), Until: now, Limit: page(req.GetLimit(), maxPoints, maxPoints)}

	if f.TokenID == "" {
		return f, status.Error(codes.InvalidArgument, "a token ID: name the outcome")
	}

	var err error

	if f.Since, err = time.Parse(time.RFC3339Nano, req.GetSince()); err != nil {
		return f, status.Error(codes.InvalidArgument, "since: a time, RFC 3339")
	}

	if until := req.GetUntil(); until != "" {
		if f.Until, err = time.Parse(time.RFC3339Nano, until); err != nil {
			return f, status.Error(codes.InvalidArgument, "until: a time, RFC 3339")
		}
	}

	if !f.Since.Before(f.Until) {
		return f, status.Error(codes.InvalidArgument, "since comes before until")
	}

	f.Bucket = max(f.Until.Sub(f.Since)/time.Duration(f.Limit), time.Minute)

	if bucket := req.GetBucket(); bucket != "" {
		if f.Bucket, err = time.ParseDuration(bucket); err != nil || f.Bucket < time.Minute {
			return f, status.Error(codes.InvalidArgument, "bucket: a duration of a minute or more, like 1h")
		}
	}

	return f, nil
}

func (a *api) ListTrades(ctx context.Context, req *pluginv1.ListTradesRequest) (*pluginv1.ListTradesResponse, error) {
	return call(ctx, a, req.GetSession(), opTrades, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.ListTradesResponse, error) {
		if a.deps.Data.Trades == nil {
			return nil, errNoSource
		}

		f := ports.TradeFilter{Limit: page(req.GetLimit(), 20, maxResults)}

		if market := strings.TrimSpace(req.GetMarketId()); market != "" {
			f.MarketIDs = []domain.MarketID{domain.MarketID(market)}
		}

		if req.GetAddress() != "" {
			address, err := domain.ParseAddress(req.GetAddress())
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "the address: %v", err)
			}

			f.Wallet = address
		}

		if f.MarketIDs == nil && f.Wallet == "" {
			return nil, status.Error(codes.InvalidArgument, "a market ID, an address, or both")
		}

		got, err := a.deps.Data.Trades.Trades(ctx, f)
		if err != nil {
			return nil, a.sourceError(ctx, s, "trades", err)
		}

		trades := got.Items[:min(len(got.Items), f.Limit)]

		out := &pluginv1.ListTradesResponse{Trades: make([]*pluginv1.Trade, len(trades))}
		for i, t := range trades {
			out.Trades[i] = &pluginv1.Trade{
				MarketId: string(t.MarketID), TokenId: string(t.TokenID), Outcome: t.Outcome, Side: string(t.Side),
				Price: text(t.Price), Size: text(t.Size), Address: string(t.Wallet), Name: t.Name, At: timeText(t.At),
				TxHash: t.TxHash, Title: t.Title, Slug: t.Slug, EventSlug: t.EventSlug,
			}
		}

		return out, nil
	})
}

func (a *api) ListPositions(ctx context.Context, req *pluginv1.ListPositionsRequest) (*pluginv1.ListPositionsResponse, error) {
	return call(ctx, a, req.GetSession(), opPositions, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.ListPositionsResponse, error) {
		if a.deps.Data.Positions == nil {
			return nil, errNoSource
		}

		address, err := domain.ParseAddress(req.GetAddress())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "the address: %v", err)
		}

		f := ports.PositionFilter{Wallet: address, Limit: page(req.GetLimit(), 20, maxResults)}

		got, err := a.deps.Data.Positions.Positions(ctx, f)
		if err != nil {
			return nil, a.sourceError(ctx, s, "positions", err)
		}

		held := got.Items[:min(len(got.Items), f.Limit)]

		out := &pluginv1.ListPositionsResponse{Positions: make([]*pluginv1.Position, len(held))}
		for i, p := range held {
			out.Positions[i] = &pluginv1.Position{
				Address: string(p.Wallet), MarketId: string(p.MarketID), TokenId: string(p.TokenID), Title: p.Title,
				Outcome: p.Outcome, Size: text(p.Size), AvgPrice: text(p.AvgPrice), CurrentPrice: text(p.CurrentPrice),
				Value: text(p.Value), RealizedPnl: text(p.RealizedPnL), UnrealizedPnl: text(p.UnrealizedPnL),
				Redeemable: p.Redeemable,
			}
		}

		return out, nil
	})
}

// ListWallets gives the owner's own wallets, to a plugin with the wallets permission
// (D301): their IDs, as wallet settings name them, names and addresses.
func (a *api) ListWallets(ctx context.Context, req *pluginv1.ListWalletsRequest) (*pluginv1.ListWalletsResponse, error) {
	return call(ctx, a, req.GetSession(), opWallets, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.ListWalletsResponse, error) {
		switch {
		case !a.perms.wallets:
			return nil, status.Errorf(codes.PermissionDenied, "the owner's wallets: its permissions do not name them (%s)", plugin.PermWallets)
		case a.deps.Data.Wallets == nil:
			return nil, errNoSource
		}

		wallets, err := a.deps.Data.Wallets.ListForUser(ctx, s.rc.OwnerID)
		if err != nil {
			return nil, a.sourceError(ctx, s, "wallets", err)
		}

		out := &pluginv1.ListWalletsResponse{Wallets: make([]*pluginv1.Wallet, len(wallets))}
		for i, w := range wallets {
			out.Wallets[i] = &pluginv1.Wallet{Id: string(w.ID), Name: w.Label, Address: string(w.Address)}
		}

		a.log.Debug(ctx, "Plugin read the owner's wallets", s.logArgs()...)

		return out, nil
	})
}

// text is a decimal as exact text.
func text(d decimal.Decimal) string {
	return d.String()
}

// timeText is a time as RFC 3339; empty when unknown.
func timeText(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	return t.UTC().Format(time.RFC3339Nano)
}
