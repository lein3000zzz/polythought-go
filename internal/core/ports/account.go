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

// TradingKeys reads what a private key controls and makes its API credentials (D154,
// D155). Keys cross it decrypted, in memory only.
type TradingKeys interface {
	// Account says how key controls wallet: the signer, the key's address, and the
	// signature type. domain.ErrKeyNotForWallet when it controls neither the wallet nor
	// its proxy, Safe or Deposit Wallet; domain.ErrInvalidInput when it is no key.
	Account(key string, wallet domain.WalletAddress) (signer domain.WalletAddress, typ domain.SignatureType, err error)

	// APICredentials makes the key's account an API key, or gives its key again.
	APICredentials(ctx context.Context, key string) (domain.PolymarketCredentials, error)
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
