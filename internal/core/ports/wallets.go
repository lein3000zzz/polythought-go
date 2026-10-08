package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// SealedCredentials are stored credentials: the secrets encrypted by a SecretBox, the
// rest in the clear.
type SealedCredentials struct {
	Info       domain.CredentialsInfo
	APIKey     []byte
	Secret     []byte
	Passphrase []byte
}

// WalletRepository scopes every lookup to the owning user, so one user can never reach
// another's wallet; a wallet of someone else is simply not found.
type WalletRepository interface {
	// Create returns domain.ErrWalletExists if the user already linked the address.
	Create(ctx context.Context, w domain.Wallet) error
	ListForUser(ctx context.Context, userID domain.UserID) ([]domain.Wallet, error)
	Get(ctx context.Context, userID domain.UserID, id domain.WalletID) (domain.Wallet, error)
	CountForUser(ctx context.Context, userID domain.UserID) (int, error)
	// Delete removes the wallet and its credentials.
	Delete(ctx context.Context, userID domain.UserID, id domain.WalletID) error

	// SaveCredentials creates or replaces a wallet's credentials.
	SaveCredentials(ctx context.Context, c SealedCredentials) error
	Credentials(ctx context.Context, walletID domain.WalletID) (SealedCredentials, error)
	DeleteCredentials(ctx context.Context, walletID domain.WalletID) error
}
