package polymarket

import (
	"context"
	"net/url"
	"strconv"

	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ ClobAccountClient = (*DefaultClobClient)(nil)

const (
	opAPIKeys          = "api_keys"
	opOpenOrders       = "open_orders"
	opBalanceAllowance = "balance_allowance"
)

const attrAddress = "polymarket.address"

func (c *DefaultClobClient) APIKeys(ctx context.Context, creds L2Credentials) (_ []string, err error) {
	done := c.kit.Start(ctx, opAPIKeys)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opAPIKeys, func(ctx context.Context, span trace.Span) ([]string, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address))

		var resp struct {
			APIKeys []string `json:"apiKeys"`
		}

		if err := c.core.getL2(ctx, "/auth/api-keys", nil, creds, &resp); err != nil {
			return nil, failed(ctx, c.log, opAPIKeys, err)
		}

		return resp.APIKeys, nil
	})
}

func (c *DefaultClobClient) OpenOrders(ctx context.Context, creds L2Credentials, q OpenOrdersQuery) (_ Page[OpenOrder], err error) {
	done := c.kit.Start(ctx, opOpenOrders)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opOpenOrders, func(ctx context.Context, span trace.Span) (Page[OpenOrder], error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address), attribute.String(attrCursor, q.Cursor))

		cursor := q.Cursor
		if cursor == "" {
			cursor = clobFirstCursor
		}

		params := url.Values{"next_cursor": {cursor}}
		setString(params, "id", q.ID)
		setString(params, "market", q.Market)
		setString(params, "asset_id", q.AssetID)

		var resp struct {
			Data       []OpenOrder `json:"data"`
			NextCursor string      `json:"next_cursor"`
		}

		if err := c.core.getL2(ctx, "/data/orders", params, creds, &resp); err != nil {
			return Page[OpenOrder]{}, failed(ctx, c.log, opOpenOrders, err)
		}

		next := resp.NextCursor
		if next == clobEndCursor {
			next = ""
		}

		span.SetAttributes(resultAttrs(len(resp.Data), next)...)
		c.log.Debug(ctx, "Fetched open orders", "count", len(resp.Data))

		return Page[OpenOrder]{Items: resp.Data, NextCursor: next}, nil
	})
}

func (c *DefaultClobClient) BalanceAllowance(ctx context.Context, creds L2Credentials, q BalanceQuery) (_ BalanceAllowance, err error) {
	done := c.kit.Start(ctx, opBalanceAllowance)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opBalanceAllowance, func(ctx context.Context, span trace.Span) (BalanceAllowance, error) {
		span.SetAttributes(attribute.String(attrAddress, creds.Address), attribute.String("polymarket.asset_type", string(q.AssetType)))

		params := url.Values{
			"asset_type":     {string(q.AssetType)},
			"signature_type": {strconv.Itoa(q.SignatureType)},
		}
		setString(params, "token_id", q.TokenID)

		var out BalanceAllowance
		if err := c.core.getL2(ctx, "/balance-allowance", params, creds, &out); err != nil {
			return BalanceAllowance{}, failed(ctx, c.log, opBalanceAllowance, err)
		}

		return out, nil
	})
}
