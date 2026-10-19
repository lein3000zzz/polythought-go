package polymarket

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var _ ClobAccountClient = (*DefaultClobClient)(nil)

const (
	opCreateAPIKey     = "create_api_key"
	opAPIKeys          = "api_keys"
	opOpenOrders       = "open_orders"
	opBalanceAllowance = "balance_allowance"
)

const attrAddress = "polymarket.address"

// CreateOrDeriveAPIKey makes the account of auth's key an API key, or gives its key
// again when it has one, as the official client does: a 400 to making one means it exists.
func (c *DefaultClobClient) CreateOrDeriveAPIKey(ctx context.Context, auth L1Auth) (_ L2Credentials, err error) {
	done := c.kit.Start(ctx, opCreateAPIKey)
	defer func() { done(succeeded(err)) }()

	return tracing.InScope(ctx, c.scope, opCreateAPIKey, func(ctx context.Context, span trace.Span) (L2Credentials, error) {
		span.SetAttributes(attribute.String(attrAddress, auth.Signer.Address()))

		var resp struct {
			APIKey     string `json:"apiKey"`
			Secret     string `json:"secret"`
			Passphrase string `json:"passphrase"`
		}

		err := c.core.do(ctx, http.MethodPost, "/auth/api-key", nil, nil, auth, &resp)

		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusBadRequest {
			err = c.core.do(ctx, http.MethodGet, "/auth/derive-api-key", nil, nil, auth, &resp)
		}

		if err != nil {
			return L2Credentials{}, failed(ctx, c.log, opCreateAPIKey, err)
		}

		c.log.Info(ctx, "Made a Polymarket API key", "address", auth.Signer.Address())

		return L2Credentials{Address: auth.Signer.Address(), APIKey: resp.APIKey, Secret: resp.Secret, Passphrase: resp.Passphrase}, nil
	})
}

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
