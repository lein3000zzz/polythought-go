package wallets_test

import (
	"context"
	"encoding/json"
	"sync"
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

const (
	ownerAddr  = domain.WalletAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	sessionKey = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	keySigner  = domain.WalletAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	signature  = "0x5ign"
)

var typedData = json.RawMessage(`{"primaryType":"Batch"}`)

type sessionMocks struct {
	repo     *portsmocks.MockWalletRepository
	keys     *portsmocks.MockTradingKeys
	store    *portsmocks.MockSessionKeyRepository
	relayer  *portsmocks.MockSessionKeyRelayer
	reminder *portsmocks.MockSessionKeyReminder
}

// clock is a time tests can move.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = t
}

func sessionService(t *testing.T, available bool) (*wallets.Service, sessionMocks, *clock) {
	t.Helper()

	m := sessionMocks{
		repo:     portsmocks.NewMockWalletRepository(t),
		keys:     portsmocks.NewMockTradingKeys(t),
		store:    portsmocks.NewMockSessionKeyRepository(t),
		relayer:  portsmocks.NewMockSessionKeyRelayer(t),
		reminder: portsmocks.NewMockSessionKeyReminder(t),
	}
	m.relayer.EXPECT().Available().Return(available).Maybe()

	c := &clock{at: now}

	svc := wallets.New(m.repo, fakeBox(t), portsmocks.NewMockCredentialsVerifier(t), logging.Noop(),
		wallets.WithClock(c.now), wallets.WithKeys(m.keys, func() bool { return false }),
		wallets.WithSessionKeys(m.store, m.relayer, m.reminder))

	return svc, m, c
}

func authorization() ports.SessionKeyBatch {
	return ports.SessionKeyBatch{
		Wallet: walletAddr, Owner: ownerAddr, Signer: keySigner, Nonce: "7",
		ValidUntil: now.Add(4315 * time.Hour), Deadline: now.Add(10 * time.Minute),
	}
}

// storedKey is a key of the wallet in state, sealed as the service seals it.
func storedKey(state domain.SessionKeyState) ports.SealedSessionKey {
	b := authorization()
	k := ports.SealedSessionKey{
		Info: domain.SessionKey{
			WalletID: wallet.ID, Signer: keySigner, Owner: ownerAddr, State: state,
			ValidUntil: b.ValidUntil, CreatedAt: now, UpdatedAt: now,
		},
		Nonce: b.Nonce, Deadline: b.Deadline,
	}

	if state != domain.SessionKeyActive {
		k.Key = []byte("session-keys/w-1/" + string(keySigner) + "|" + sessionKey)
	}

	if state == domain.SessionKeyAuthorizing {
		k.TransactionID = "tx-1"
	}

	return k
}

func TestTakesSessionKeys(t *testing.T) {
	svc, _, _ := sessionService(t, true)
	assert.True(t, svc.TakesSessionKeys())

	svc, _, _ = sessionService(t, false)
	assert.False(t, svc.TakesSessionKeys(), "no Builder key")

	plain, _ := newService(t)
	assert.False(t, plain.TakesSessionKeys())
}

func TestStartSessionKey(t *testing.T) {
	t.Run("a key made, sealed, and the batch to sign", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)

		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.relayer.EXPECT().IsDepositWallet(ownerAddr, walletAddr).Return(true)
		m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeyActive)}, nil)
		m.relayer.EXPECT().NewKey().Return(sessionKey, keySigner, nil)
		m.relayer.EXPECT().Authorization(mock.Anything, ownerAddr, walletAddr, keySigner).Return(authorization(), nil)
		m.relayer.EXPECT().TypedData(authorization()).Return(typedData, nil)

		var added ports.SealedSessionKey
		m.store.EXPECT().AddSessionKey(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, k ports.SealedSessionKey) error {
			added = k
			return nil
		})

		// The browser's account, in lower case as wallets give it.
		got, err := svc.StartSessionKey(t.Context(), session, wallet.ID, "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266")
		require.NoError(t, err)

		assert.Equal(t, storedKey(domain.SessionKeySigning), added)
		assert.Equal(t, added.Info, got.Key)
		assert.Equal(t, typedData, got.TypedData)
	})

	t.Run("not the owner's Deposit Wallet: nothing made", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)

		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.relayer.EXPECT().IsDepositWallet(ownerAddr, walletAddr).Return(false)

		_, err := svc.StartSessionKey(t.Context(), session, wallet.ID, string(ownerAddr))
		require.ErrorIs(t, err, domain.ErrNotDepositWallet)
	})

	t.Run("not while one is being authorised", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)

		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.relayer.EXPECT().IsDepositWallet(ownerAddr, walletAddr).Return(true)
		m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeyAuthorizing)}, nil)

		_, err := svc.StartSessionKey(t.Context(), session, wallet.ID, string(ownerAddr))
		require.ErrorIs(t, err, domain.ErrSessionKeyPending)
	})

	t.Run("refused", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil).Maybe()

		_, err := svc.StartSessionKey(t.Context(), token, wallet.ID, string(ownerAddr))
		require.ErrorIs(t, err, domain.ErrForbidden, "from a browser session only")

		_, err = svc.StartSessionKey(t.Context(), session, wallet.ID, "0x123")
		require.ErrorIs(t, err, domain.ErrInvalidAddress)

		off, _, _ := sessionService(t, false)
		_, err = off.StartSessionKey(t.Context(), session, wallet.ID, string(ownerAddr))
		require.ErrorIs(t, err, domain.ErrSessionKeysOff)
	})
}

func TestSignSessionKey(t *testing.T) {
	signing := func(m sessionMocks) {
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{
			storedKey(domain.SessionKeySigning), storedKey(domain.SessionKeyActive),
		}, nil)
	}

	t.Run("submitted", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		signing(m)
		c.set(now.Add(time.Minute))

		m.relayer.EXPECT().Signer(authorization(), signature).Return(ownerAddr, nil)
		m.relayer.EXPECT().Submit(mock.Anything, authorization(), signature).Return("tx-1", nil)

		want := storedKey(domain.SessionKeyAuthorizing)
		want.Info.UpdatedAt = now.Add(time.Minute)
		m.store.EXPECT().SaveSessionKey(mock.Anything, want).Return(nil)

		got, err := svc.SignSessionKey(t.Context(), session, wallet.ID, " "+signature+"\n")
		require.NoError(t, err)
		assert.Equal(t, domain.SessionKeyAuthorizing, got.State)
	})

	t.Run("someone else's signature is not sent", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		signing(m)
		m.relayer.EXPECT().Signer(authorization(), signature).Return(keySigner, nil)

		_, err := svc.SignSessionKey(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrWrongSigner)
		assert.Contains(t, err.Error(), string(keySigner))
	})

	t.Run("too late", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		signing(m)
		c.set(now.Add(11 * time.Minute))

		_, err := svc.SignSessionKey(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrSignatureExpired)
	})

	t.Run("refused by the Relayer: the key fails", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		signing(m)
		m.relayer.EXPECT().Signer(authorization(), signature).Return(ownerAddr, nil)
		m.relayer.EXPECT().Submit(mock.Anything, authorization(), signature).Return("", domain.ErrRelayerRefused)
		m.store.EXPECT().SaveSessionKey(mock.Anything, mock.MatchedBy(func(k ports.SealedSessionKey) bool {
			return k.Info.State == domain.SessionKeyFailed && k.Info.Error == domain.ErrRelayerRefused.Error()
		})).Return(nil)

		_, err := svc.SignSessionKey(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrRelayerRefused)
	})

	t.Run("the Relayer not there: it may be sent again", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		signing(m)
		m.relayer.EXPECT().Signer(authorization(), signature).Return(ownerAddr, nil)
		m.relayer.EXPECT().Submit(mock.Anything, authorization(), signature).Return("", domain.ErrUnavailable)

		_, err := svc.SignSessionKey(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrUnavailable)
	})

	t.Run("nothing to sign", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeyActive)}, nil)

		_, err := svc.SignSessionKey(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestConfirmSessionKeys(t *testing.T) {
	authorizing := func(m sessionMocks) {
		m.store.EXPECT().AuthorizingSessionKeys(mock.Anything).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeyAuthorizing)}, nil)
	}

	failed := func(m sessionMocks, reason string) {
		m.store.EXPECT().SaveSessionKey(mock.Anything, mock.MatchedBy(func(k ports.SealedSessionKey) bool {
			return k.Info.State == domain.SessionKeyFailed && k.Info.Error == reason
		})).Return(nil)
	}

	t.Run("confirmed: its API key made, and both the wallet's credentials", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		authorizing(m)
		c.set(now.Add(time.Minute))

		m.relayer.EXPECT().Confirmed(mock.Anything, "tx-1").Return(true, nil)
		m.keys.EXPECT().APICredentials(mock.Anything, sessionKey).Return(domain.PolymarketCredentials{
			APIKey: apiKey, Secret: secret, Passphrase: passphrase, Signer: keySigner,
		}, nil)
		m.repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)

		var (
			active ports.SealedSessionKey
			creds  ports.SealedCredentials
		)

		m.store.EXPECT().ActivateSessionKey(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, k ports.SealedSessionKey, c ports.SealedCredentials) error {
				active, creds = k, c
				return nil
			})

		svc.ConfirmSessionKeys(t.Context())

		assert.Equal(t, domain.SessionKeyActive, active.Info.State)
		assert.Nil(t, active.Key, "the key moves to the credentials")
		assert.Equal(t, domain.CredentialsInfo{
			WalletID: wallet.ID, APIKeyHint: "4f6d…9f01", Signer: keySigner, SignatureType: domain.SignatureDepositWallet,
			HasKey: true, SessionKey: true, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute),
		}, creds.Info)
		assert.Equal(t, "polymarket-credentials/w-1/private_key|"+sessionKey, string(creds.PrivateKey))
	})

	t.Run("pending: asked again later", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		authorizing(m)
		c.set(now.Add(5 * time.Minute))
		m.relayer.EXPECT().Confirmed(mock.Anything, "tx-1").Return(false, nil)

		svc.ConfirmSessionKeys(t.Context())
	})

	t.Run("pending too long: failed", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		authorizing(m)
		c.set(now.Add(16 * time.Minute))
		m.relayer.EXPECT().Confirmed(mock.Anything, "tx-1").Return(false, domain.ErrUnavailable)
		failed(m, "not confirmed in 15 minutes")

		svc.ConfirmSessionKeys(t.Context())
	})

	t.Run("its transaction failed", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		authorizing(m)
		m.relayer.EXPECT().Confirmed(mock.Anything, "tx-1").Return(false, domain.ErrRelayerRefused)
		failed(m, domain.ErrRelayerRefused.Error())

		svc.ConfirmSessionKeys(t.Context())
	})

	t.Run("confirmed, no API key yet: asked again, then failed", func(t *testing.T) {
		svc, m, c := sessionService(t, true)
		m.store.EXPECT().AuthorizingSessionKeys(mock.Anything).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeyAuthorizing)}, nil).Times(2)
		m.relayer.EXPECT().Confirmed(mock.Anything, "tx-1").Return(true, nil).Times(2)
		m.keys.EXPECT().APICredentials(mock.Anything, sessionKey).Return(domain.PolymarketCredentials{}, domain.ErrCredentialsRejected).Times(2)

		c.set(now.Add(time.Minute))
		svc.ConfirmSessionKeys(t.Context())

		failed(m, "authorised, but Polymarket gave it no API key: "+domain.ErrCredentialsRejected.Error())
		c.set(now.Add(16 * time.Minute))
		svc.ConfirmSessionKeys(t.Context())
	})

	t.Run("removed meanwhile", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		authorizing(m)
		m.relayer.EXPECT().Confirmed(mock.Anything, "tx-1").Return(true, nil)
		m.keys.EXPECT().APICredentials(mock.Anything, sessionKey).Return(domain.PolymarketCredentials{APIKey: apiKey}, nil)
		m.repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)
		m.store.EXPECT().ActivateSessionKey(mock.Anything, mock.Anything, mock.Anything).Return(domain.ErrNotFound)

		svc.ConfirmSessionKeys(t.Context())
	})
}

func TestRevokeSessionKey(t *testing.T) {
	revocation := func() ports.SessionKeyBatch {
		b := authorization()
		b.Revoke, b.ValidUntil, b.Nonce = true, time.Time{}, "8"

		return b
	}

	activeKey := func(m sessionMocks) {
		k := storedKey(domain.SessionKeyActive)
		k.Nonce = "8"

		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeySigning), k}, nil)
	}

	t.Run("the batch to sign", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{storedKey(domain.SessionKeyActive)}, nil)
		m.relayer.EXPECT().Revocation(mock.Anything, ownerAddr, walletAddr, keySigner).Return(revocation(), nil)
		m.relayer.EXPECT().TypedData(revocation()).Return(typedData, nil)
		m.store.EXPECT().SaveSessionKey(mock.Anything, mock.MatchedBy(func(k ports.SealedSessionKey) bool {
			return k.Info.State == domain.SessionKeyActive && k.Nonce == "8"
		})).Return(nil)

		got, err := svc.StartRevocation(t.Context(), session, wallet.ID)
		require.NoError(t, err)
		assert.Equal(t, typedData, got.TypedData)
	})

	t.Run("revoked and removed", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		activeKey(m)
		m.relayer.EXPECT().Signer(revocation(), signature).Return(ownerAddr, nil)
		m.relayer.EXPECT().Submit(mock.Anything, revocation(), signature).Return("tx-2", nil)
		m.store.EXPECT().DeleteSessionKeys(mock.Anything, wallet.ID).Return(nil)

		got, err := svc.RevokeSessionKey(t.Context(), session, wallet.ID, signature)
		require.NoError(t, err)
		assert.Equal(t, wallets.Revocation{Revoked: true}, got)
	})

	t.Run("removed when the Relayer does not take it (D159)", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		activeKey(m)
		m.relayer.EXPECT().Signer(revocation(), signature).Return(ownerAddr, nil)
		m.relayer.EXPECT().Submit(mock.Anything, revocation(), signature).Return("", domain.ErrUnavailable)
		m.store.EXPECT().DeleteSessionKeys(mock.Anything, wallet.ID).Return(nil)

		got, err := svc.RevokeSessionKey(t.Context(), session, wallet.ID, signature)
		require.NoError(t, err)
		assert.Equal(t, wallets.Revocation{Problem: domain.ErrUnavailable.Error()}, got)
	})

	t.Run("someone else's signature removes nothing", func(t *testing.T) {
		svc, m, _ := sessionService(t, true)
		activeKey(m)
		m.relayer.EXPECT().Signer(revocation(), signature).Return(keySigner, nil)

		_, err := svc.RevokeSessionKey(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrWrongSigner)
	})

	t.Run("removed without signing", func(t *testing.T) {
		svc, m, _ := sessionService(t, false)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.store.EXPECT().DeleteSessionKeys(mock.Anything, wallet.ID).Return(nil)

		require.NoError(t, svc.RemoveSessionKey(t.Context(), token, wallet.ID), "even without a Builder key, from a token")
	})
}

func TestSessionKeysListed(t *testing.T) {
	svc, m, _ := sessionService(t, true)
	m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
	m.store.EXPECT().SessionKeys(mock.Anything, wallet.ID).Return([]ports.SealedSessionKey{
		storedKey(domain.SessionKeyFailed), storedKey(domain.SessionKeyActive),
	}, nil)

	got, err := svc.SessionKeys(t.Context(), token, wallet.ID)
	require.NoError(t, err)
	require.NotNil(t, got.Active)
	require.NotNil(t, got.Pending)
	assert.Equal(t, domain.SessionKeyActive, got.Active.State)
	assert.Equal(t, domain.SessionKeyFailed, got.Pending.State)
}

func TestRemindSessionKeys(t *testing.T) {
	svc, m, _ := sessionService(t, true)

	first, second := storedKey(domain.SessionKeyActive).Info, storedKey(domain.SessionKeyActive).Info
	other := domain.Wallet{ID: "w-2", UserID: owner.ID, Address: signerAddr}

	m.store.EXPECT().ExpiringSessionKeys(mock.Anything, now.Add(14*24*time.Hour)).Return([]ports.ExpiringSessionKey{
		{Key: first, Wallet: wallet}, {Key: second, Wallet: other},
	}, nil)
	m.reminder.EXPECT().RemindSessionKey(mock.Anything, owner.ID, wallet, first.ValidUntil).Return(nil)
	m.reminder.EXPECT().RemindSessionKey(mock.Anything, owner.ID, other, second.ValidUntil).Return(errDB)
	m.store.EXPECT().MarkReminded(mock.Anything, wallet.ID, keySigner, now).Return(nil) // once: the second is tried again

	svc.RemindSessionKeys(t.Context())
}

func TestCredentialsOfASessionKeyStay(t *testing.T) {
	withSessionKey := wallet
	withSessionKey.HasSessionKey, withSessionKey.HasKey, withSessionKey.HasCredentials = true, true, true

	svc, m, _ := sessionService(t, true)
	m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(withSessionKey, nil)

	_, err := svc.SetCredentials(t.Context(), session, wallet.ID, wallets.CredentialsInput{
		APIKey: apiKey, Secret: secret, Passphrase: passphrase,
	})
	require.ErrorIs(t, err, domain.ErrSessionKeyInUse)

	require.ErrorIs(t, svc.RemoveCredentials(t.Context(), session, wallet.ID), domain.ErrSessionKeyInUse)

	repo := portsmocks.NewMockWalletRepository(t)
	repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(withSessionKey, nil)

	imports := wallets.New(repo, fakeBox(t), portsmocks.NewMockCredentialsVerifier(t), logging.Noop(),
		wallets.WithKeys(portsmocks.NewMockTradingKeys(t), func() bool { return true }))

	_, err = imports.ImportKey(t.Context(), session, wallet.ID, privateKey)
	require.ErrorIs(t, err, domain.ErrSessionKeyInUse)
}
