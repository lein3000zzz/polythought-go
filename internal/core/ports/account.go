package ports

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// CredentialsVerifier asks the exchange whether credentials work. A refusal is
// domain.ErrCredentialsRejected; failing to ask is domain.ErrUnavailable.
type CredentialsVerifier interface {
	VerifyCredentials(ctx context.Context, creds domain.PolymarketCredentials) error
}

type OrderFilter struct {
	MarketID domain.MarketID
	TokenID  domain.TokenID
	Cursor   string
}

// AccountReader reads an account's private exchange data with its credentials.
type AccountReader interface {
	CredentialsVerifier
	OpenOrders(ctx context.Context, creds domain.PolymarketCredentials, f OrderFilter) (Page[domain.Order], error)
	// CollateralBalance is the account's spendable collateral in USD.
	CollateralBalance(ctx context.Context, creds domain.PolymarketCredentials) (decimal.Decimal, error)
}

// WalletDirectory resolves a user's linked wallets, for nodes acting on their behalf.
// It only ever returns the owner's own wallets.
type WalletDirectory interface {
	Wallet(ctx context.Context, owner domain.UserID, id domain.WalletID) (domain.Wallet, error)
	Credentials(ctx context.Context, owner domain.UserID, id domain.WalletID) (domain.PolymarketCredentials, error)
}
