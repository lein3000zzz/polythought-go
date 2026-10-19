//go:build db

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	signerA = domain.WalletAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	signerB = domain.WalletAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC")
	ownerA  = domain.WalletAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
)

func sessionKey(w domain.Wallet, signer domain.WalletAddress, state domain.SessionKeyState, created time.Time) ports.SealedSessionKey {
	k := ports.SealedSessionKey{
		Info: domain.SessionKey{
			WalletID: w.ID, Signer: signer, Owner: ownerA, State: state,
			ValidUntil: created.Add(4315 * time.Hour), CreatedAt: created, UpdatedAt: created,
		},
		Nonce: "7", Deadline: created.Add(10 * time.Minute),
	}

	if state != domain.SessionKeyActive {
		k.Key = []byte("sealed-" + string(signer))
	}

	return k
}

func signers(t *testing.T, wallets *postgres.Wallets, w domain.Wallet) map[domain.WalletAddress]domain.SessionKeyState {
	t.Helper()

	keys, err := wallets.SessionKeys(context.Background(), w.ID)
	require.NoError(t, err)

	out := map[domain.WalletAddress]domain.SessionKeyState{}
	for _, k := range keys {
		out[k.Info.Signer] = k.Info.State
	}

	return out
}

func TestSessionKeys(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets)
	}{
		{"round trip", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			k := sessionKey(w, signerA, domain.SessionKeySigning, at)
			require.NoError(t, wallets.AddSessionKey(ctx, k))

			got, err := wallets.SessionKeys(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, []ports.SealedSessionKey{k}, got)

			k.Info.State, k.TransactionID, k.Info.UpdatedAt = domain.SessionKeyAuthorizing, "tx-1", at.Add(time.Minute)
			require.NoError(t, wallets.SaveSessionKey(ctx, k))

			got, err = wallets.SessionKeys(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, []ports.SealedSessionKey{k}, got)

			authorizing, err := wallets.AuthorizingSessionKeys(ctx)
			require.NoError(t, err)
			assert.Equal(t, []ports.SealedSessionKey{k}, authorizing)
		}},
		{"a new key replaces the pending ones, not the active one", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyActive, at)))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerB, domain.SessionKeyFailed, at.Add(time.Hour))))

			third := domain.WalletAddress("0x90F79bf6EB2c4f870365E785982E1f101E93b906")
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, third, domain.SessionKeySigning, at.Add(2*time.Hour))))

			assert.Equal(t, map[domain.WalletAddress]domain.SessionKeyState{
				signerA: domain.SessionKeyActive, third: domain.SessionKeySigning,
			}, signers(t, wallets, w))

			keys, err := wallets.SessionKeys(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, third, keys[0].Info.Signer, "newest first")
		}},
		{"a saved key that is gone is not found", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			assert.ErrorIs(t, wallets.SaveSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeySigning, at)), domain.ErrNotFound)
		}},
		{"activating replaces the active key and the credentials", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			w := seedWallet(t, db, u, addrA)
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "past", at)))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyActive, at)))

			k := sessionKey(w, signerB, domain.SessionKeyAuthorizing, at.Add(time.Hour))
			require.NoError(t, wallets.AddSessionKey(ctx, k))

			creds := sealed(w, "sess", at.Add(2*time.Hour))
			creds.Info.Signer, creds.Info.SignatureType, creds.Info.SessionKey, creds.Info.HasKey = signerB, domain.SignatureDepositWallet, true, true
			creds.PrivateKey = []byte("sealed-key")

			k.Info.State, k.Key, k.Info.UpdatedAt = domain.SessionKeyActive, nil, at.Add(2*time.Hour)
			require.NoError(t, wallets.ActivateSessionKey(ctx, k, creds))

			assert.Equal(t, map[domain.WalletAddress]domain.SessionKeyState{signerB: domain.SessionKeyActive}, signers(t, wallets, w))

			got, err := wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, creds.Info, got.Info)
			assert.Equal(t, []byte("sealed-key"), got.PrivateKey)

			listed, err := wallets.Get(ctx, u.ID, w.ID)
			require.NoError(t, err)
			assert.True(t, listed.HasKey)
			assert.True(t, listed.HasSessionKey)

			// Pasted credentials after it are not a session key's.
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "next", at.Add(3*time.Hour))))
			got, err = wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
			assert.False(t, got.Info.SessionKey)
		}},
		{"activating a key that is gone changes nothing", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "past", at)))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyActive, at)))

			k := sessionKey(w, signerB, domain.SessionKeyActive, at)
			creds := sealed(w, "sess", at)
			creds.Info.SessionKey = true

			assert.ErrorIs(t, wallets.ActivateSessionKey(ctx, k, creds), domain.ErrNotFound)
			assert.Equal(t, map[domain.WalletAddress]domain.SessionKeyState{signerA: domain.SessionKeyActive}, signers(t, wallets, w))

			got, err := wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, "past", got.Info.APIKeyHint)
		}},
		{"one active key a wallet", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyActive, at)))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerB, domain.SessionKeySigning, at)))

			k := sessionKey(w, signerB, domain.SessionKeyActive, at)
			assert.Error(t, wallets.SaveSessionKey(ctx, k))
		}},
		{"deleting removes the keys and the credentials they made", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyActive, at)))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerB, domain.SessionKeySigning, at)))

			creds := sealed(w, "sess", at)
			creds.Info.SessionKey = true
			require.NoError(t, wallets.SaveCredentials(ctx, creds))

			require.NoError(t, wallets.DeleteSessionKeys(ctx, w.ID))
			assert.Empty(t, signers(t, wallets, w))

			_, err := wallets.Credentials(ctx, w.ID)
			require.ErrorIs(t, err, domain.ErrNotFound)

			require.NoError(t, wallets.DeleteSessionKeys(ctx, w.ID), "again")
		}},
		{"deleting keeps credentials a session key did not make", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "past", at)))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyFailed, at)))

			require.NoError(t, wallets.DeleteSessionKeys(ctx, w.ID))

			_, err := wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
		}},
		{"unlinking removes the keys", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			w := seedWallet(t, db, u, addrA)
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(w, signerA, domain.SessionKeyActive, at)))
			require.NoError(t, wallets.Delete(ctx, u.ID, w.ID))
			assert.Empty(t, signers(t, wallets, w))
		}},
		{"expiring keys, until reminded", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			soon := seedWallet(t, db, u, addrA)
			later := seedWallet(t, db, u, "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359")
			pending := seedWallet(t, db, u, "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB")

			k := sessionKey(soon, signerA, domain.SessionKeyActive, at)
			require.NoError(t, wallets.AddSessionKey(ctx, k))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(later, signerA, domain.SessionKeyActive, at.Add(30*24*time.Hour))))
			require.NoError(t, wallets.AddSessionKey(ctx, sessionKey(pending, signerA, domain.SessionKeySigning, at)))

			before := k.Info.ValidUntil.Add(time.Hour)

			got, err := wallets.ExpiringSessionKeys(ctx, before)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, k.Info, got[0].Key)
			assert.Equal(t, soon.ID, got[0].Wallet.ID)
			assert.Equal(t, u.ID, got[0].Wallet.UserID)
			assert.Equal(t, soon.Label, got[0].Wallet.Label)

			require.NoError(t, wallets.MarkReminded(ctx, soon.ID, signerA, at.Add(time.Hour)))

			got, err = wallets.ExpiringSessionKeys(ctx, before)
			require.NoError(t, err)
			assert.Empty(t, got)

			// Saving it again, for a revocation, keeps it reminded.
			k.Nonce = "8"
			require.NoError(t, wallets.SaveSessionKey(ctx, k))
			got, err = wallets.ExpiringSessionKeys(ctx, before)
			require.NoError(t, err)
			assert.Empty(t, got)

			assert.ErrorIs(t, wallets.MarkReminded(ctx, soon.ID, signerB, at), domain.ErrNotFound)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewWallets(db))
		})
	}
}
