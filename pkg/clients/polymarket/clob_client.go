package polymarket

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ ClobClient = (*DefaultClobClient)(nil)

const (
	opGetOrderBooks      = "get_order_books"
	opGetPrices          = "get_prices"
	opGetMidpoints       = "get_midpoints"
	opGetSpreads         = "get_spreads"
	opGetLastTradePrices = "get_last_trade_prices"
	opGetMarketInfo      = "get_market_info"
	opServerTime         = "server_time"
)

// DefaultClobClient uses the POST batch endpoints; the documented GET variants return 400.
// Unknown token IDs are dropped from batch responses, so results can be shorter than the input.
type DefaultClobClient struct {
	core  *httpCore
	kit   *redkit.Kit
	scope tracing.Scope
	log   logging.Logger

	lookupWait time.Duration // before an order sent without an answer is looked up (D194)
}

type tokenRequest struct {
	TokenID string `json:"token_id"`
}

func NewClobClient(util *observability.Utility, cfg HTTPConfig) (*DefaultClobClient, error) {
	log := util.Logger.With("component", "polymarket.clob")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "polymarket_clob",
		Buckets:   redkit.MediumLongBuckets,
		Component: "polymarket clob",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register clob metrics: %w", err)
	}

	return &DefaultClobClient{
		core:  newHTTPCore(log, cfg.withDefaults(DefaultClobURL, defaultClobRPS)),
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,

		lookupWait: lookupWait,
	}, nil
}

func (c *DefaultClobClient) GetOrderBooks(ctx context.Context, tokenIDs ...string) (_ []OrderBook, err error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}

	done := c.kit.Start(ctx, opGetOrderBooks)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetOrderBooks, func(ctx context.Context, span trace.Span) ([]OrderBook, error) {
		span.SetAttributes(tokenAttrs(tokenIDs)...)

		var books []OrderBook
		if err := c.core.post(ctx, "/books", toTokenRequests(tokenIDs), &books); err != nil {
			return nil, failed(ctx, c.log, opGetOrderBooks, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(books)))
		c.log.Debug(ctx, "Fetched order books", "requested", len(tokenIDs), "returned", len(books))

		return books, nil
	})
}

// GetPrices quotes per side: BUY returns the best bid, SELL the best ask.
func (c *DefaultClobClient) GetPrices(ctx context.Context, reqs ...PriceRequest) (_ map[string]SidePrices, err error) {
	if len(reqs) == 0 {
		return map[string]SidePrices{}, nil
	}

	done := c.kit.Start(ctx, opGetPrices)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetPrices, func(ctx context.Context, span trace.Span) (map[string]SidePrices, error) {
		tokenIDs := make([]string, len(reqs))
		for i, r := range reqs {
			tokenIDs[i] = r.TokenID
		}

		span.SetAttributes(tokenAttrs(tokenIDs)...)

		var prices map[string]SidePrices
		if err := c.core.post(ctx, "/prices", reqs, &prices); err != nil {
			return nil, failed(ctx, c.log, opGetPrices, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(prices)))
		c.log.Debug(ctx, "Fetched prices", "requested", len(reqs), "returned", len(prices))

		return prices, nil
	})
}

func (c *DefaultClobClient) GetMidpoints(ctx context.Context, tokenIDs ...string) (_ map[string]decimal.Decimal, err error) {
	if len(tokenIDs) == 0 {
		return map[string]decimal.Decimal{}, nil
	}

	done := c.kit.Start(ctx, opGetMidpoints)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetMidpoints, func(ctx context.Context, span trace.Span) (map[string]decimal.Decimal, error) {
		span.SetAttributes(tokenAttrs(tokenIDs)...)

		var mids map[string]decimal.Decimal
		if err := c.core.post(ctx, "/midpoints", toTokenRequests(tokenIDs), &mids); err != nil {
			return nil, failed(ctx, c.log, opGetMidpoints, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(mids)))
		c.log.Debug(ctx, "Fetched midpoints", "requested", len(tokenIDs), "returned", len(mids))

		return mids, nil
	})
}

func (c *DefaultClobClient) GetSpreads(ctx context.Context, tokenIDs ...string) (_ map[string]decimal.Decimal, err error) {
	if len(tokenIDs) == 0 {
		return map[string]decimal.Decimal{}, nil
	}

	done := c.kit.Start(ctx, opGetSpreads)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetSpreads, func(ctx context.Context, span trace.Span) (map[string]decimal.Decimal, error) {
		span.SetAttributes(tokenAttrs(tokenIDs)...)

		var spreads map[string]decimal.Decimal
		if err := c.core.post(ctx, "/spreads", toTokenRequests(tokenIDs), &spreads); err != nil {
			return nil, failed(ctx, c.log, opGetSpreads, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(spreads)))
		c.log.Debug(ctx, "Fetched spreads", "requested", len(tokenIDs), "returned", len(spreads))

		return spreads, nil
	})
}

func (c *DefaultClobClient) GetLastTradePrices(ctx context.Context, tokenIDs ...string) (_ []LastTrade, err error) {
	if len(tokenIDs) == 0 {
		return nil, nil
	}

	done := c.kit.Start(ctx, opGetLastTradePrices)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetLastTradePrices, func(ctx context.Context, span trace.Span) ([]LastTrade, error) {
		span.SetAttributes(tokenAttrs(tokenIDs)...)

		var trades []LastTrade
		if err := c.core.post(ctx, "/last-trades-prices", toTokenRequests(tokenIDs), &trades); err != nil {
			return nil, failed(ctx, c.log, opGetLastTradePrices, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(trades)))
		c.log.Debug(ctx, "Fetched last trade prices", "requested", len(tokenIDs), "returned", len(trades))

		return trades, nil
	})
}

func (c *DefaultClobClient) GetMarketInfo(ctx context.Context, conditionID string) (_ MarketInfo, err error) {
	done := c.kit.Start(ctx, opGetMarketInfo)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opGetMarketInfo, func(ctx context.Context, span trace.Span) (MarketInfo, error) {
		span.SetAttributes(attribute.String(attrConditionID, conditionID))

		var info MarketInfo
		if err := c.core.get(ctx, "/clob-markets/"+url.PathEscape(conditionID), nil, &info); err != nil {
			return MarketInfo{}, failed(ctx, c.log, opGetMarketInfo, err)
		}

		c.log.Debug(ctx, "Fetched market info", "condition_id", conditionID, "version", info.Version)

		return info, nil
	})
}

// ServerTime returns the CLOB clock in unix seconds.
func (c *DefaultClobClient) ServerTime(ctx context.Context) (_ int64, err error) {
	done := c.kit.Start(ctx, opServerTime)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opServerTime, func(ctx context.Context, _ trace.Span) (int64, error) {
		var ts int64
		if err := c.core.get(ctx, "/time", nil, &ts); err != nil {
			return 0, failed(ctx, c.log, opServerTime, err)
		}

		return ts, nil
	})
}

func toTokenRequests(tokenIDs []string) []tokenRequest {
	reqs := make([]tokenRequest, len(tokenIDs))
	for i, id := range tokenIDs {
		reqs[i] = tokenRequest{TokenID: id}
	}

	return reqs
}
