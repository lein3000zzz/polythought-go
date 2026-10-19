package wallets_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const privateKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

func keyService(t *testing.T, allowed bool) (*wallets.Service, *portsmocks.MockWalletRepository, *portsmocks.MockTradingKeys) {
	t.Helper()

	repo := portsmocks.NewMockWalletRepository(t)
	keys := portsmocks.NewMockTradingKeys(t)
	verifier := portsmocks.NewMockCredentialsVerifier(t)
	verifier.EXPECT().VerifyCredentials(mock.Anything, mock.Anything).Return(nil).Maybe()

	svc := wallets.New(repo, fakeBox(t), verifier, logging.Noop(),
		wallets.WithClock(func() time.Time { return now }), wallets.WithKeys(keys, func() bool { return allowed }))

	return svc, repo, keys
}

func TestImportKey(t *testing.T) {
	t.Run("kept sealed, with API credentials made from it (D153, D154)", func(t *testing.T) {
		svc, repo, keys := keyService(t, true)

		repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)
		keys.EXPECT().Account(privateKey, walletAddr).Return(signerAddr, domain.SignaturePolyProxy, nil)
		keys.EXPECT().APICredentials(mock.Anything, privateKey).Return(domain.PolymarketCredentials{
			APIKey: apiKey, Secret: secret, Passphrase: passphrase, Signer: signerAddr,
		}, nil)

		var saved ports.SealedCredentials
		repo.EXPECT().SaveCredentials(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, c ports.SealedCredentials) error {
			saved = c
			return nil
		})

		info, err := svc.ImportKey(t.Context(), session, wallet.ID, "  "+privateKey+"\n")
		require.NoError(t, err)

		assert.Equal(t, "polymarket-credentials/w-1/private_key|"+privateKey, string(saved.PrivateKey), "sealed, bound to its wallet")
		assert.Equal(t, "polymarket-credentials/w-1/api_key|"+apiKey, string(saved.APIKey))
		assert.True(t, info.HasKey)
		assert.Equal(t, domain.WalletAddress(signerAddr), info.Signer)
		assert.Equal(t, domain.SignaturePolyProxy, info.SignatureType, "as the key controls the wallet")
	})

	t.Run("a key that does not control the wallet is refused before anything is asked (D155)", func(t *testing.T) {
		svc, repo, keys := keyService(t, true)

		repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		keys.EXPECT().Account(privateKey, walletAddr).Return("", 0, domain.ErrKeyNotForWallet)

		_, err := svc.ImportKey(t.Context(), session, wallet.ID, privateKey)
		require.ErrorIs(t, err, domain.ErrKeyNotForWallet)
	})

	t.Run("while the instance takes none (D21)", func(t *testing.T) {
		svc, _, _ := keyService(t, false)
		assert.False(t, svc.TakesKeys())

		_, err := svc.ImportKey(t.Context(), session, wallet.ID, privateKey)
		require.ErrorIs(t, err, domain.ErrKeyImportOff)
	})

	t.Run("from a browser session only", func(t *testing.T) {
		svc, _, _ := keyService(t, true)

		_, err := svc.ImportKey(t.Context(), token, wallet.ID, privateKey)
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("when Polymarket makes no API key, nothing is kept", func(t *testing.T) {
		svc, repo, keys := keyService(t, true)

		repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		keys.EXPECT().Account(privateKey, walletAddr).Return(walletAddr, domain.SignatureEOA, nil)
		keys.EXPECT().APICredentials(mock.Anything, privateKey).Return(domain.PolymarketCredentials{}, domain.ErrUnavailable)

		_, err := svc.ImportKey(t.Context(), session, wallet.ID, privateKey)
		require.ErrorIs(t, err, domain.ErrUnavailable)
	})

	t.Run("pasted credentials replace a key", func(t *testing.T) {
		svc, repo, _ := keyService(t, true)

		repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{PrivateKey: []byte("sealed")}, nil)
		repo.EXPECT().SaveCredentials(mock.Anything, mock.MatchedBy(func(c ports.SealedCredentials) bool {
			return c.PrivateKey == nil && !c.Info.HasKey
		})).Return(nil)

		_, err := svc.SetCredentials(t.Context(), session, wallet.ID, wallets.CredentialsInput{
			APIKey: apiKey, Secret: secret, Passphrase: passphrase, SignatureType: domain.SignatureEOA,
		})
		require.NoError(t, err)
	})
}
