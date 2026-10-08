package polymarket

import (
	"context"
	"fmt"
	"net/url"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ DataClient = (*DefaultDataClient)(nil)

const (
	opPositions    = "positions"
	opTrades       = "trades"
	opActivity     = "activity"
	opHolders      = "holders"
	opOpenInterest = "open_interest"
	opPriceHistory = "price_history"
)

// DefaultDataClient targets Data API v2 only; v1 is retired on 2026-10-24.
// Cursors do not carry filters: page with the same query, changing only Cursor.
// A miss is an empty result, not ErrNotFound.
type DefaultDataClient struct {
	core  *httpCore
	kit   *redkit.Kit
	scope tracing.Scope
	log   logging.Logger
}

type dataEnvelope[T any] struct {
	Data       T `json:"data"`
	Pagination struct {
		NextCursor string `json:"next_cursor"`
	} `json:"pagination"`
}

func NewDataClient(util *observability.Utility, cfg HTTPConfig) (*DefaultDataClient, error) {
	log := util.Logger.With("component", "polymarket.data")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "polymarket_data",
		Buckets:   redkit.MediumLongBuckets,
		Component: "polymarket data",
	})
	if err != nil {
		return nil, fmt.Errorf("polymarket: register data metrics: %w", err)
	}

	return &DefaultDataClient{
		core:  newHTTPCore(log, cfg.withDefaults(DefaultDataURL, defaultDataRPS)),
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
	}, nil
}

func (c *DefaultDataClient) Positions(ctx context.Context, q PositionsQuery) (_ Page[Position], err error) {
	done := c.kit.Start(ctx, opPositions)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opPositions, func(ctx context.Context, span trace.Span) (Page[Position], error) {
		span.SetAttributes(pageAttrs(q.Limit, q.Cursor)...)
		span.SetAttributes(
			attribute.String(attrUser, q.User),
			attribute.StringSlice(attrConditionIDs, q.Conditions),
		)

		params := url.Values{}
		setString(params, "user", q.User)
		setCSV(params, "condition", q.Conditions)
		setString(params, "status", q.Status)
		setString(params, "event_id", q.EventID)
		setString(params, "sort_by", q.SortBy)
		setString(params, "sort_direction", q.SortDirection)
		setInt(params, "limit", q.Limit)
		setString(params, "cursor", q.Cursor)

		var resp dataEnvelope[[]Position]
		if err := c.core.get(ctx, "/v2/positions", params, &resp); err != nil {
			return Page[Position]{}, failed(ctx, c.log, opPositions, err)
		}

		span.SetAttributes(resultAttrs(len(resp.Data), resp.Pagination.NextCursor)...)
		c.log.Debug(ctx, "Fetched positions", "user", q.User, "count", len(resp.Data))

		return Page[Position]{Items: resp.Data, NextCursor: resp.Pagination.NextCursor}, nil
	})
}

func (c *DefaultDataClient) Trades(ctx context.Context, q TradesQuery) (_ Page[Trade], err error) {
	done := c.kit.Start(ctx, opTrades)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opTrades, func(ctx context.Context, span trace.Span) (Page[Trade], error) {
		span.SetAttributes(pageAttrs(q.Limit, q.Cursor)...)
		span.SetAttributes(
			attribute.String(attrUser, q.User),
			attribute.StringSlice(attrConditionIDs, q.Conditions),
		)

		params := url.Values{}
		setString(params, "user", q.User)
		setCSV(params, "condition", q.Conditions)
		setString(params, "event_id", q.EventID)
		setString(params, "side", string(q.Side))
		setBool(params, "taker_only", q.TakerOnly)
		setInt64(params, "start", q.Start)
		setInt64(params, "end", q.End)
		setInt(params, "limit", q.Limit)
		setString(params, "cursor", q.Cursor)

		var resp dataEnvelope[[]Trade]
		if err := c.core.get(ctx, "/v2/trades", params, &resp); err != nil {
			return Page[Trade]{}, failed(ctx, c.log, opTrades, err)
		}

		span.SetAttributes(resultAttrs(len(resp.Data), resp.Pagination.NextCursor)...)
		c.log.Debug(ctx, "Fetched trades", "count", len(resp.Data))

		return Page[Trade]{Items: resp.Data, NextCursor: resp.Pagination.NextCursor}, nil
	})
}

func (c *DefaultDataClient) Activity(ctx context.Context, q ActivityQuery) (_ Page[Activity], err error) {
	done := c.kit.Start(ctx, opActivity)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opActivity, func(ctx context.Context, span trace.Span) (Page[Activity], error) {
		span.SetAttributes(pageAttrs(q.Limit, q.Cursor)...)
		span.SetAttributes(
			attribute.String(attrUser, q.User),
			attribute.StringSlice(attrConditionIDs, q.Conditions),
		)

		params := url.Values{}
		setString(params, "user", q.User)
		setCSV(params, "type", q.Types)
		setCSV(params, "condition", q.Conditions)
		setString(params, "event_id", q.EventID)
		setString(params, "side", string(q.Side))
		setInt64(params, "start", q.Start)
		setInt64(params, "end", q.End)
		setString(params, "sort_direction", q.SortDirection)
		setInt(params, "limit", q.Limit)
		setString(params, "cursor", q.Cursor)

		var resp dataEnvelope[[]Activity]
		if err := c.core.get(ctx, "/v2/activity", params, &resp); err != nil {
			return Page[Activity]{}, failed(ctx, c.log, opActivity, err)
		}

		span.SetAttributes(resultAttrs(len(resp.Data), resp.Pagination.NextCursor)...)
		c.log.Debug(ctx, "Fetched activity", "user", q.User, "count", len(resp.Data))

		return Page[Activity]{Items: resp.Data, NextCursor: resp.Pagination.NextCursor}, nil
	})
}

func (c *DefaultDataClient) Holders(ctx context.Context, q HoldersQuery) (_ []TokenHolders, err error) {
	done := c.kit.Start(ctx, opHolders)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opHolders, func(ctx context.Context, span trace.Span) ([]TokenHolders, error) {
		span.SetAttributes(
			attribute.StringSlice(attrConditionIDs, q.Conditions),
			attribute.Int(attrLimit, q.Limit),
		)

		params := url.Values{}
		setCSV(params, "condition", q.Conditions)
		setInt(params, "limit", q.Limit)
		setDecimal(params, "min_balance", q.MinBalance)

		var resp dataEnvelope[[]TokenHolders]
		if err := c.core.get(ctx, "/v2/holders", params, &resp); err != nil {
			return nil, failed(ctx, c.log, opHolders, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(resp.Data)))
		c.log.Debug(ctx, "Fetched holders", "tokens", len(resp.Data))

		return resp.Data, nil
	})
}

// OpenInterest returns one row per condition, or a single "GLOBAL" row when none is given.
func (c *DefaultDataClient) OpenInterest(ctx context.Context, conditionIDs ...string) (_ []OpenInterest, err error) {
	done := c.kit.Start(ctx, opOpenInterest)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opOpenInterest, func(ctx context.Context, span trace.Span) ([]OpenInterest, error) {
		span.SetAttributes(attribute.StringSlice(attrConditionIDs, conditionIDs))

		params := url.Values{}
		setCSV(params, "condition", conditionIDs)

		var resp dataEnvelope[[]OpenInterest]
		if err := c.core.get(ctx, "/v2/oi", params, &resp); err != nil {
			return nil, failed(ctx, c.log, opOpenInterest, err)
		}

		span.SetAttributes(attribute.Int(attrResults, len(resp.Data)))
		c.log.Debug(ctx, "Fetched open interest", "conditions", len(conditionIDs), "rows", len(resp.Data))

		return resp.Data, nil
	})
}

func (c *DefaultDataClient) PriceHistory(ctx context.Context, q PriceHistoryQuery) (_ Page[PricePoint], err error) {
	done := c.kit.Start(ctx, opPriceHistory)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opPriceHistory, func(ctx context.Context, span trace.Span) (Page[PricePoint], error) {
		span.SetAttributes(pageAttrs(q.Limit, q.Cursor)...)
		span.SetAttributes(attribute.String(attrTokenID, q.TokenID))

		params := url.Values{}
		setString(params, "token_id", q.TokenID)
		setString(params, "interval", q.Interval)
		setInt64(params, "start", q.Start)
		setInt64(params, "end", q.End)
		setInt(params, "bucket_seconds", q.BucketSeconds)
		setInt(params, "limit", q.Limit)
		setString(params, "cursor", q.Cursor)

		var resp dataEnvelope[[]PricePoint]
		if err := c.core.get(ctx, "/v2/prices-history", params, &resp); err != nil {
			return Page[PricePoint]{}, failed(ctx, c.log, opPriceHistory, err)
		}

		span.SetAttributes(resultAttrs(len(resp.Data), resp.Pagination.NextCursor)...)
		c.log.Debug(ctx, "Fetched price history", "token_id", q.TokenID, "points", len(resp.Data))

		return Page[PricePoint]{Items: resp.Data, NextCursor: resp.Pagination.NextCursor}, nil
	})
}
