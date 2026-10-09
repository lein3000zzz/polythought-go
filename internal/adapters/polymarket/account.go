package polymarket

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.AccountReader = (*Account)(nil)

// collateralDecimals is the CLOB's fixed-point scale for balances.
const collateralDecimals = 6

// Account serves the account ports from the CLOB's L2 endpoints.
type Account struct {
	clob pm.ClobAccountClient
}

func NewAccount(clob pm.ClobAccountClient) *Account {
	return &Account{clob: clob}
}

// VerifyCredentials lists the account's API keys: the call only succeeds with valid
// credentials, and the key must be among those listed.
func (a *Account) VerifyCredentials(ctx context.Context, creds domain.PolymarketCredentials) error {
	keys, err := a.clob.APIKeys(ctx, toL2(creds))
	if err != nil {
		return accountError(err)
	}

	if !slices.ContainsFunc(keys, func(k string) bool { return strings.EqualFold(k, creds.APIKey) }) {
		return fmt.Errorf("%w: the key is not listed for %s", domain.ErrCredentialsRejected, creds.Signer)
	}

	return nil
}

func (a *Account) OpenOrders(ctx context.Context, creds domain.PolymarketCredentials, f ports.OrderFilter) (ports.Page[domain.Order], error) {
	page, err := a.clob.OpenOrders(ctx, toL2(creds), pm.OpenOrdersQuery{
		Market:  string(f.MarketID),
		AssetID: string(f.TokenID),
		Cursor:  f.Cursor,
	})
	if err != nil {
		return ports.Page[domain.Order]{}, accountError(err)
	}

	return mapPage(page, toOrder), nil
}

func (a *Account) CollateralBalance(ctx context.Context, creds domain.PolymarketCredentials) (decimal.Decimal, error) {
	b, err := a.clob.BalanceAllowance(ctx, toL2(creds), pm.BalanceQuery{
		AssetType:     pm.AssetCollateral,
		SignatureType: int(creds.SignatureType),
	})
	if err != nil {
		return decimal.Zero, accountError(err)
	}

	return b.Balance.Shift(-collateralDecimals), nil
}

func toL2(c domain.PolymarketCredentials) pm.L2Credentials {
	return pm.L2Credentials{Address: string(c.Signer), APIKey: c.APIKey, Secret: c.Secret, Passphrase: c.Passphrase}
}

// accountError maps client errors to the domain: a refusal is the user's to fix, anything
// else means the exchange could not answer. Refusals carry no upstream detail, which the
// client has already logged; it would only add noise for the user.
func accountError(err error) error {
	switch {
	case errors.Is(err, pm.ErrUnauthorized), errors.Is(err, pm.ErrInvalidSecret):
		return domain.ErrCredentialsRejected
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
	}
}

func toOrder(o pm.OpenOrder) domain.Order {
	order := domain.Order{
		ID:        o.ID,
		MarketID:  domain.MarketID(o.Market),
		TokenID:   domain.TokenID(o.AssetID),
		Outcome:   o.Outcome,
		Side:      toSide(o.Side),
		Price:     o.Price,
		Size:      o.OriginalSize,
		Filled:    o.SizeMatched,
		Status:    strings.ToLower(o.Status),
		Type:      o.OrderType,
		CreatedAt: time.Unix(o.CreatedAt, 0).UTC(),
	}

	if exp, err := strconv.ParseInt(o.Expiration, 10, 64); err == nil && exp > 0 {
		order.ExpiresAt = time.Unix(exp, 0).UTC()
	}

	return order
}
