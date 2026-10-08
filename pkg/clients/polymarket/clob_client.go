package polymarket

import (
	"context"
	"net/url"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
)

var _ ClobClient = (*DefaultClobClient)(nil)

// DefaultClobClient uses the POST batch endpoints; the documented GET variants return 400.
// Unknown token IDs are dropped from batch responses, so results can be shorter than the input.
type DefaultClobClient struct {
	core *httpCore
}

type tokenRequest struct {
	TokenID string `json:"token_id"`
}

func NewClobClient(util *observability.Utility, cfg HTTPConfig) (*DefaultClobClient, error) {
	core, err := newHTTPCore(util, "clob", cfg.withDefaults(DefaultClobURL, defaultClobRPS))
	if err != nil {
		return nil, err
	}

	return &DefaultClobClient{core: core}, nil
}

func (c *DefaultClobClient) GetOrderBooks(ctx context.Context, tokenIDs ...string) ([]OrderBook, error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}

	var books []OrderBook
	err := c.core.post(ctx, "get_order_books", "/books", toTokenRequests(tokenIDs), &books)

	return books, err
}

// GetPrices quotes per side: BUY returns the best bid, SELL the best ask.
func (c *DefaultClobClient) GetPrices(ctx context.Context, reqs ...PriceRequest) (map[string]SidePrices, error) {
	if len(reqs) == 0 {
		return map[string]SidePrices{}, nil
	}

	var prices map[string]SidePrices
	err := c.core.post(ctx, "get_prices", "/prices", reqs, &prices)

	return prices, err
}

func (c *DefaultClobClient) GetMidpoints(ctx context.Context, tokenIDs ...string) (map[string]decimal.Decimal, error) {
	return c.postTokenMap(ctx, "get_midpoints", "/midpoints", tokenIDs)
}

func (c *DefaultClobClient) GetSpreads(ctx context.Context, tokenIDs ...string) (map[string]decimal.Decimal, error) {
	return c.postTokenMap(ctx, "get_spreads", "/spreads", tokenIDs)
}

func (c *DefaultClobClient) GetLastTradePrices(ctx context.Context, tokenIDs ...string) ([]LastTrade, error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}

	var trades []LastTrade
	err := c.core.post(ctx, "get_last_trade_prices", "/last-trades-prices", toTokenRequests(tokenIDs), &trades)

	return trades, err
}

func (c *DefaultClobClient) GetMarketInfo(ctx context.Context, conditionID string) (MarketInfo, error) {
	var info MarketInfo
	err := c.core.get(ctx, "get_market_info", "/clob-markets/"+url.PathEscape(conditionID), nil, &info)

	return info, err
}

// ServerTime returns the CLOB clock in unix seconds.
func (c *DefaultClobClient) ServerTime(ctx context.Context) (int64, error) {
	var ts int64
	err := c.core.get(ctx, "server_time", "/time", nil, &ts)

	return ts, err
}

func (c *DefaultClobClient) postTokenMap(ctx context.Context, op, path string, tokenIDs []string) (map[string]decimal.Decimal, error) {
	if len(tokenIDs) == 0 {
		return map[string]decimal.Decimal{}, nil
	}

	var out map[string]decimal.Decimal
	err := c.core.post(ctx, op, path, toTokenRequests(tokenIDs), &out)

	return out, err
}

func toTokenRequests(tokenIDs []string) []tokenRequest {
	reqs := make([]tokenRequest, len(tokenIDs))
	for i, id := range tokenIDs {
		reqs[i] = tokenRequest{TokenID: id}
	}

	return reqs
}
