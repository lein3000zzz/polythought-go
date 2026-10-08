package polymarket

import (
	"context"
	"net/url"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
)

var _ DataClient = (*DefaultDataClient)(nil)

// DefaultDataClient targets Data API v2 only; v1 is retired on 2026-10-24.
// Cursors do not carry filters: page with the same query, changing only Cursor.
// A miss is an empty result, not ErrNotFound.
type DefaultDataClient struct {
	core *httpCore
}

type dataEnvelope[T any] struct {
	Data       T `json:"data"`
	Pagination struct {
		NextCursor string `json:"next_cursor"`
	} `json:"pagination"`
}

func NewDataClient(util *observability.Utility, cfg HTTPConfig) (*DefaultDataClient, error) {
	core, err := newHTTPCore(util, "data", cfg.withDefaults(DefaultDataURL, defaultDataRPS))
	if err != nil {
		return nil, err
	}

	return &DefaultDataClient{core: core}, nil
}

func (c *DefaultDataClient) Positions(ctx context.Context, q PositionsQuery) (Page[Position], error) {
	params := url.Values{}
	setString(params, "user", q.User)
	setCSV(params, "condition", q.Conditions)
	setString(params, "status", q.Status)
	setString(params, "event_id", q.EventID)
	setString(params, "sort_by", q.SortBy)
	setString(params, "sort_direction", q.SortDirection)
	setInt(params, "limit", q.Limit)
	setString(params, "cursor", q.Cursor)

	return getDataPage[Position](ctx, c.core, "positions", "/v2/positions", params)
}

func (c *DefaultDataClient) Trades(ctx context.Context, q TradesQuery) (Page[Trade], error) {
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

	return getDataPage[Trade](ctx, c.core, "trades", "/v2/trades", params)
}

func (c *DefaultDataClient) Activity(ctx context.Context, q ActivityQuery) (Page[Activity], error) {
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

	return getDataPage[Activity](ctx, c.core, "activity", "/v2/activity", params)
}

func (c *DefaultDataClient) Holders(ctx context.Context, q HoldersQuery) ([]TokenHolders, error) {
	params := url.Values{}
	setCSV(params, "condition", q.Conditions)
	setInt(params, "limit", q.Limit)
	setString(params, "min_balance", q.MinBalance)

	var resp dataEnvelope[[]TokenHolders]
	err := c.core.get(ctx, "holders", "/v2/holders", params, &resp)

	return resp.Data, err
}

// OpenInterest returns one row per condition, or a single "GLOBAL" row when none is given.
func (c *DefaultDataClient) OpenInterest(ctx context.Context, conditionIDs ...string) ([]OpenInterest, error) {
	params := url.Values{}
	setCSV(params, "condition", conditionIDs)

	var resp dataEnvelope[[]OpenInterest]
	err := c.core.get(ctx, "open_interest", "/v2/oi", params, &resp)

	return resp.Data, err
}

func (c *DefaultDataClient) PriceHistory(ctx context.Context, q PriceHistoryQuery) (Page[PricePoint], error) {
	params := url.Values{}
	setString(params, "token_id", q.TokenID)
	setString(params, "interval", q.Interval)
	setInt64(params, "start", q.Start)
	setInt64(params, "end", q.End)
	setInt(params, "bucket_seconds", q.BucketSeconds)
	setInt(params, "limit", q.Limit)
	setString(params, "cursor", q.Cursor)

	return getDataPage[PricePoint](ctx, c.core, "price_history", "/v2/prices-history", params)
}

func getDataPage[T any](ctx context.Context, core *httpCore, op, path string, params url.Values) (Page[T], error) {
	var resp dataEnvelope[[]T]
	if err := core.get(ctx, op, path, params, &resp); err != nil {
		return Page[T]{}, err
	}

	return Page[T]{Items: resp.Data, NextCursor: resp.Pagination.NextCursor}, nil
}
