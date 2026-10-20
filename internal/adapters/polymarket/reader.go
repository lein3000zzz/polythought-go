package polymarket

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var (
	_ ports.MarketReader       = (*Reader)(nil)
	_ ports.EventReader        = (*Reader)(nil)
	_ ports.OrderBookReader    = (*Reader)(nil)
	_ ports.TradeReader        = (*Reader)(nil)
	_ ports.PositionReader     = (*Reader)(nil)
	_ ports.PriceHistoryReader = (*Reader)(nil)
)

// historyIntervalAll asks the Data API for the whole price history.
const historyIntervalAll = "max"

// Reader serves the read ports from the Gamma, CLOB and Data clients.
type Reader struct {
	gamma pm.GammaClient
	clob  pm.ClobClient
	data  pm.DataClient
}

func NewReader(gamma pm.GammaClient, clob pm.ClobClient, data pm.DataClient) *Reader {
	return &Reader{gamma: gamma, clob: clob, data: data}
}

func (r *Reader) ListMarkets(ctx context.Context, f ports.MarketFilter) (ports.Page[domain.Market], error) {
	page, err := r.gamma.ListMarkets(ctx, pm.MarketsQuery{
		Limit:        f.Limit,
		AfterCursor:  f.Cursor,
		Closed:       f.Closed,
		TagIDs:       f.TagIDs,
		ConditionIDs: toStrings(f.MarketIDs),
		Slugs:        f.Slugs,
		TokenIDs:     toStrings(f.TokenIDs),
	})
	if err != nil {
		return ports.Page[domain.Market]{}, err
	}

	return mapPage(page, toMarket), nil
}

// GetMarket filters by condition ID: Gamma has no direct lookup for it. Gamma's default
// hides most closed markets even when filtering by ID, so a miss is retried with closed=true.
func (r *Reader) GetMarket(ctx context.Context, id domain.MarketID) (domain.Market, error) {
	closed := true

	for _, filter := range []*bool{nil, &closed} {
		page, err := r.gamma.ListMarkets(ctx, pm.MarketsQuery{ConditionIDs: []string{string(id)}, Closed: filter, Limit: 1, IncludeTags: true})
		if err != nil {
			return domain.Market{}, err
		}

		if len(page.Items) > 0 {
			return toMarket(page.Items[0]), nil
		}
	}

	return domain.Market{}, fmt.Errorf("market %s: %w", id, domain.ErrNotFound)
}

func (r *Reader) GetMarketBySlug(ctx context.Context, slug string) (domain.Market, error) {
	m, err := r.gamma.GetMarketBySlug(ctx, slug)
	if err != nil {
		return domain.Market{}, notFound(err)
	}

	return toMarket(m), nil
}

func (r *Reader) ListEvents(ctx context.Context, f ports.EventFilter) (ports.Page[domain.Event], error) {
	page, err := r.gamma.ListEvents(ctx, pm.EventsQuery{
		Limit:       f.Limit,
		AfterCursor: f.Cursor,
		Closed:      f.Closed,
		Featured:    f.Featured,
		TitleSearch: f.Title,
	})
	if err != nil {
		return ports.Page[domain.Event]{}, err
	}

	return mapPage(page, toEvent), nil
}

func (r *Reader) GetEvent(ctx context.Context, id domain.EventID) (domain.Event, error) {
	e, err := r.gamma.GetEvent(ctx, string(id))
	if err != nil {
		return domain.Event{}, notFound(err)
	}

	return toEvent(e), nil
}

func (r *Reader) GetEventBySlug(ctx context.Context, slug string) (domain.Event, error) {
	e, err := r.gamma.GetEventBySlug(ctx, slug)
	if err != nil {
		return domain.Event{}, notFound(err)
	}

	return toEvent(e), nil
}

func (r *Reader) OrderBooks(ctx context.Context, tokenIDs ...domain.TokenID) ([]domain.OrderBook, error) {
	books, err := r.clob.GetOrderBooks(ctx, toStrings(tokenIDs)...)
	if err != nil {
		return nil, err
	}

	return mapSlice(books, toOrderBook), nil
}

func (r *Reader) Trades(ctx context.Context, f ports.TradeFilter) (ports.Page[domain.Trade], error) {
	var takerOnly *bool
	if f.Makers {
		takerOnly = new(false)
	}

	page, err := r.data.Trades(ctx, pm.TradesQuery{
		TakerOnly:  takerOnly,
		User:       string(f.Wallet),
		Conditions: toStrings(f.MarketIDs),
		Side:       fromSide(f.Side),
		Start:      unixOrZero(f.Since),
		End:        unixOrZero(f.Until),
		Limit:      f.Limit,
		Cursor:     f.Cursor,
	})
	if err != nil {
		return ports.Page[domain.Trade]{}, err
	}

	return mapPage(page, toTrade), nil
}

func (r *Reader) Positions(ctx context.Context, f ports.PositionFilter) (ports.Page[domain.Position], error) {
	page, err := r.data.Positions(ctx, pm.PositionsQuery{
		User:       string(f.Wallet),
		Conditions: toStrings(f.MarketIDs),
		Limit:      f.Limit,
		Cursor:     f.Cursor,
	})
	if err != nil {
		return ports.Page[domain.Position]{}, err
	}

	return mapPage(page, toPosition), nil
}

// PriceHistory maps a time range onto the Data API, which takes either an interval or
// both ends of a range, never one end alone.
func (r *Reader) PriceHistory(ctx context.Context, f ports.PriceHistoryFilter) (ports.Page[domain.PricePoint], error) {
	q := pm.PriceHistoryQuery{
		TokenID:       string(f.TokenID),
		BucketSeconds: int(f.Bucket / time.Second),
		Limit:         f.Limit,
		Cursor:        f.Cursor,
	}

	switch {
	case f.Since.IsZero() && f.Until.IsZero():
		q.Interval = historyIntervalAll
	case f.Until.IsZero():
		q.Start, q.End = f.Since.Unix(), time.Now().Unix()
	case f.Since.IsZero():
		q.Start, q.End = 1, f.Until.Unix() // 0 would be dropped as unset
	default:
		q.Start, q.End = f.Since.Unix(), f.Until.Unix()
	}

	page, err := r.data.PriceHistory(ctx, q)
	if err != nil {
		return ports.Page[domain.PricePoint]{}, err
	}

	return mapPage(page, toPricePoint), nil
}

// notFound makes client not-found errors match domain.ErrNotFound, keeping the original.
func notFound(err error) error {
	if errors.Is(err, pm.ErrNotFound) {
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	}

	return err
}

func mapSlice[S, D any](in []S, f func(S) D) []D {
	out := make([]D, len(in))
	for i, v := range in {
		out[i] = f(v)
	}

	return out
}

func mapPage[S, D any](p pm.Page[S], f func(S) D) ports.Page[D] {
	return ports.Page[D]{Items: mapSlice(p.Items, f), NextCursor: p.NextCursor}
}

var _ ports.TradingRulesReader = (*Reader)(nil)

// TradingRules reads the CLOB's market info: tick size, minimum size and fee schedule.
func (r *Reader) TradingRules(ctx context.Context, market domain.MarketID) (domain.TradingRules, error) {
	info, err := r.clob.GetMarketInfo(ctx, string(market))
	if err != nil {
		return domain.TradingRules{}, err
	}

	return domain.TradingRules{
		MarketID:     market,
		TickSize:     info.MinTickSize,
		MinOrderSize: info.MinOrderSize,
		Fees:         domain.FeeSchedule{Rate: info.Fees.Rate, Exponent: info.Fees.Exponent},
		NegRisk:      info.NegRisk,
	}, nil
}

var _ ports.PortfolioReader = (*Reader)(nil)

func (r *Reader) PortfolioValue(ctx context.Context, wallet domain.WalletAddress) (decimal.Decimal, error) {
	return r.data.Value(ctx, string(wallet))
}
