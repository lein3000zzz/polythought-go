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
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// sealed is plain as fakeBox seals it for the binding aad.
func sealed(aad, plain string) []byte {
	return []byte(aad + "|" + plain)
}

func sealedCredentials(w domain.WalletID, info domain.CredentialsInfo, key string) ports.SealedCredentials {
	c := ports.SealedCredentials{
		Info:       info,
		APIKey:     sealed("polymarket-credentials/"+string(w)+"/api_key", apiKey),
		Secret:     sealed("polymarket-credentials/"+string(w)+"/secret", secret),
		Passphrase: sealed("polymarket-credentials/"+string(w)+"/passphrase", passphrase),
	}

	if key != "" {
		c.PrivateKey = sealed("polymarket-credentials/"+string(w)+"/private_key", key)
	}

	return c
}

func TestOrderKey(t *testing.T) {
	ctx := context.Background()
	owner := domain.UserID("u-1")

	t.Run("an imported key", func(t *testing.T) {
		svc, repo, _ := keyService(t, false)
		w := domain.Wallet{ID: "w-1", UserID: owner, Address: walletAddr, HasCredentials: true, HasKey: true}
		info := domain.CredentialsInfo{WalletID: w.ID, Signer: walletAddr, SignatureType: domain.SignatureEOA, HasKey: true}

		repo.EXPECT().Get(mock.Anything, owner, w.ID).Return(w, nil)
		repo.EXPECT().Credentials(mock.Anything, w.ID).Return(sealedCredentials(w.ID, info, privateKey), nil)

		k, err := svc.OrderKey(ctx, owner, w.ID)
		require.NoError(t, err)
		assert.Equal(t, privateKey, k.Key)
		assert.Equal(t, apiKey, k.Credentials.APIKey)
		assert.Equal(t, secret, k.Credentials.Secret)
		assert.Equal(t, w, k.Wallet)
	})

	t.Run("the active session key", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		w := domain.Wallet{ID: "w-1", UserID: owner, Address: walletAddr, HasCredentials: true, HasSessionKey: true}
		info := domain.CredentialsInfo{WalletID: w.ID, Signer: keySigner, SignatureType: domain.SignatureDepositWallet, SessionKey: true}
		active := ports.SealedSessionKey{
			Info: domain.SessionKey{WalletID: w.ID, Signer: keySigner, State: domain.SessionKeyActive, ValidUntil: now.Add(24 * time.Hour)},
			Key:  sealed("session-keys/"+string(w.ID)+"/"+string(keySigner), sessionKey),
		}

		m.repo.EXPECT().Get(mock.Anything, owner, w.ID).Return(w, nil)
		m.repo.EXPECT().Credentials(mock.Anything, w.ID).Return(sealedCredentials(w.ID, info, ""), nil)
		m.store.EXPECT().SessionKeys(mock.Anything, w.ID).Return([]ports.SealedSessionKey{active}, nil)

		k, err := svc.OrderKey(ctx, owner, w.ID)
		require.NoError(t, err)
		assert.Equal(t, sessionKey, k.Key)
		assert.True(t, k.Credentials.SessionKey)

		t.Run("not once it expired", func(t *testing.T) {
			c.set(now.Add(25 * time.Hour))

			_, err := svc.OrderKey(ctx, owner, w.ID)
			require.ErrorIs(t, err, domain.ErrWalletCannotTrade)
			assert.ErrorContains(t, err, "expired")
		})
	})

	t.Run("no key: pasted credentials only", func(t *testing.T) {
		svc, repo, _ := keyService(t, false)
		w := domain.Wallet{ID: "w-1", UserID: owner, Address: walletAddr, HasCredentials: true}

		repo.EXPECT().Get(mock.Anything, owner, w.ID).Return(w, nil)

		_, err := svc.OrderKey(ctx, owner, w.ID)
		require.ErrorIs(t, err, domain.ErrWalletCannotTrade)
	})

	t.Run("someone else's wallet", func(t *testing.T) {
		svc, repo, _ := keyService(t, false)
		repo.EXPECT().Get(mock.Anything, owner, domain.WalletID("w-2")).Return(domain.Wallet{}, domain.ErrNotFound)

		_, err := svc.OrderKey(ctx, owner, "w-2")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestUnlinkWhileTradingLive(t *testing.T) {
	ctx := context.Background()
	owner := domain.User{ID: "u-1", Role: domain.RoleUser}
	p := auth.Principal{User: owner}
	w := domain.Wallet{ID: "w-1", UserID: owner.ID, Address: walletAddr}

	service := func(t *testing.T, from ...domain.WorkflowTrading) (*wallets.Service, *portsmocks.MockWalletRepository) {
		repo := portsmocks.NewMockWalletRepository(t)
		trading := portsmocks.NewMockWorkflowTradingRepository(t)
		trading.EXPECT().TradingFrom(mock.Anything, w.ID).Return(from, nil)
		repo.EXPECT().Get(mock.Anything, owner.ID, w.ID).Return(w, nil)

		return wallets.New(repo, fakeBox(t), portsmocks.NewMockCredentialsVerifier(t), logging.Noop(),
			wallets.WithWorkflowTrading(trading)), repo
	}

	t.Run("refused while a workflow trades live from it", func(t *testing.T) {
		svc, _ := service(t,
			domain.WorkflowTrading{WorkflowID: "wf-1", Mode: domain.ModePaper, WalletID: w.ID},
			domain.WorkflowTrading{WorkflowID: "wf-2", Mode: domain.ModeLive, WalletID: w.ID},
		)

		require.ErrorIs(t, svc.Unlink(ctx, p, w.ID), domain.ErrWalletTradesLive)
	})

	t.Run("unlinked when they trade on paper", func(t *testing.T) {
		svc, repo := service(t, domain.WorkflowTrading{WorkflowID: "wf-1", Mode: domain.ModePaper, WalletID: w.ID})
		repo.EXPECT().Delete(mock.Anything, owner.ID, w.ID).Return(nil)

		require.NoError(t, svc.Unlink(ctx, p, w.ID))
	})
}
