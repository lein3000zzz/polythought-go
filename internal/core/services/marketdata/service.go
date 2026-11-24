// Package marketdata is Polymarket's data over REST (D442), as agents have it over MCP:
// markets found and read, order books, price history, a market's trades and an address's
// positions, through the reader's cache and its fair share per user, with bigger pages
// than an agent's. For notebooks and any other program a user writes.
package marketdata

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	// DefaultLimit is how many results a list gives unless asked for another number.
	DefaultLimit = 50
	// MaxLimit is the most results a list gives.
	MaxLimit = 500
	// MaxTokens is how many order books one call reads.
	MaxTokens = 50
)

// Windows are the spans price history covers, by name.
var Windows = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "1d": 24 * time.Hour, "1w": 7 * 24 * time.Hour, "1m": 30 * 24 * time.Hour,
}

type Deps struct {
	Events    ports.EventReader
	Markets   ports.MarketReader
	Books     ports.OrderBookReader
	History   ports.PriceHistoryReader
	Trades    ports.TradeReader
	Positions ports.PositionReader
	Now       func() time.Time
}

type Service struct {
	deps Deps
}

func New(deps Deps) *Service {
	if deps.Now == nil {
		deps.Now = time.Now
	}

	return &Service{deps: deps}
}

// limit is n, or the default for none, within the bounds.
func limit(n int) (int, error) {
	switch {
	case n == 0:
		return DefaultLimit, nil
	case n < 0 || n > MaxLimit:
		return 0, fmt.Errorf("%w: limit: from 1 to %d", domain.ErrInvalidInput, MaxLimit)
	default:
		return n, nil
	}
}

// Search finds markets by words in their event's title, open ones unless closed says.
func (s *Service) Search(ctx context.Context, user domain.UserID, query string, closed bool, n int) ([]markets.Found, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("%w: query: say what to look for", domain.ErrInvalidInput)
	}

	n, err := limit(n)
	if err != nil {
		return nil, err
	}

	return markets.FindMarkets(ports.WithUser(ctx, user), s.deps.Events, query, n, closed)
}

// Market is a market by its condition ID, 0x…, or its slug.
func (s *Service) Market(ctx context.Context, user domain.UserID, idOrSlug string) (domain.Market, error) {
	idOrSlug = strings.TrimSpace(idOrSlug)
	ctx = ports.WithUser(ctx, user)

	switch {
	case idOrSlug == "":
		return domain.Market{}, fmt.Errorf("%w: name the market by its ID or slug", domain.ErrInvalidInput)
	case strings.HasPrefix(idOrSlug, "0x"):
		return s.deps.Markets.GetMarket(ctx, domain.MarketID(idOrSlug))
	default:
		return s.deps.Markets.GetMarketBySlug(ctx, idOrSlug)
	}
}

// Books are the tokens' order books, in the order asked, a zero book's TokenID empty for a
// token without one.
func (s *Service) Books(ctx context.Context, user domain.UserID, tokens []domain.TokenID) ([]domain.OrderBook, error) {
	if len(tokens) == 0 || len(tokens) > MaxTokens {
		return nil, fmt.Errorf("%w: tokens: from 1 to %d, not %d", domain.ErrInvalidInput, MaxTokens, len(tokens))
	}

	got, err := s.deps.Books.OrderBooks(ports.WithUser(ctx, user), tokens...)
	if err != nil {
		return nil, err
	}

	by := make(map[domain.TokenID]domain.OrderBook, len(got))
	for _, b := range got {
		by[b.TokenID] = b
	}

	out := make([]domain.OrderBook, len(tokens))
	for i, t := range tokens {
		out[i] = by[t]
	}

	return out, nil
}

// History is a token's prices over a window, oldest first, at most n of them, evenly
// spaced, the first and the last among them.
func (s *Service) History(ctx context.Context, user domain.UserID, token domain.TokenID, window string, n int) ([]domain.PricePoint, error) {
	span, ok := Windows[window]
	if !ok {
		return nil, fmt.Errorf("%w: window: %q is not 1h, 6h, 1d, 1w or 1m", domain.ErrInvalidInput, window)
	}

	if token == "" {
		return nil, fmt.Errorf("%w: name the outcome's token", domain.ErrInvalidInput)
	}

	n, err := limit(n)
	if err != nil {
		return nil, err
	}

	page, err := s.deps.History.PriceHistory(ports.WithUser(ctx, user), ports.PriceHistoryFilter{
		TokenID: token, Since: s.deps.Now().Add(-span), Bucket: max(span/time.Duration(n), time.Minute),
	})
	if err != nil {
		return nil, err
	}

	points := slices.SortedFunc(slices.Values(page.Items), func(a, b domain.PricePoint) int { return a.At.Compare(b.At) })

	return evenly(points, n), nil
}

// evenly keeps n of the points, evenly, the first and the last among them.
func evenly(points []domain.PricePoint, n int) []domain.PricePoint {
	if len(points) <= n || n < 2 {
		return points[:min(len(points), max(n, 0))]
	}

	out := make([]domain.PricePoint, n)
	for i := range out {
		out[i] = points[i*(len(points)-1)/(n-1)]
	}

	return out
}

// Trades are a market's most recent trades, newest first.
func (s *Service) Trades(ctx context.Context, user domain.UserID, market domain.MarketID, n int) ([]domain.Trade, error) {
	if !strings.HasPrefix(string(market), "0x") {
		return nil, fmt.Errorf("%w: name the market by its condition ID, 0x…", domain.ErrInvalidInput)
	}

	n, err := limit(n)
	if err != nil {
		return nil, err
	}

	page, err := s.deps.Trades.Trades(ports.WithUser(ctx, user), ports.TradeFilter{MarketIDs: []domain.MarketID{market}, Limit: n})
	if err != nil {
		return nil, err
	}

	return page.Items, nil
}

// Positions are an address's open positions, public on the chain.
func (s *Service) Positions(ctx context.Context, user domain.UserID, address string, n int) ([]domain.Position, error) {
	wallet, err := domain.ParseAddress(strings.TrimSpace(address))
	if err != nil {
		return nil, fmt.Errorf("%w: address: %w", domain.ErrInvalidInput, err)
	}

	n, err = limit(n)
	if err != nil {
		return nil, err
	}

	page, err := s.deps.Positions.Positions(ports.WithUser(ctx, user), ports.PositionFilter{Wallet: wallet, Limit: n})
	if err != nil {
		return nil, err
	}

	return page.Items, nil
}
