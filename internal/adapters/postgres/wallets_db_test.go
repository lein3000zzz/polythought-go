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

const addrA = domain.WalletAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")

func seedWallet(t *testing.T, db *postgres.DB, u domain.User, addr domain.WalletAddress) domain.Wallet {
	t.Helper()

	w := domain.Wallet{ID: domain.WalletID(newID()), UserID: u.ID, Address: addr, Label: "main", CreatedAt: at}
	require.NoError(t, postgres.NewWallets(db).Create(context.Background(), w))

	return w
}

func sealed(w domain.Wallet, hint string, updated time.Time) ports.SealedCredentials {
	return ports.SealedCredentials{
		Info: domain.CredentialsInfo{
			WalletID: w.ID, APIKeyHint: hint, Signer: w.Address, SignatureType: domain.SignaturePolyProxy,
			CreatedAt: at, UpdatedAt: updated,
		},
		APIKey: []byte("k-" + hint), Secret: []byte("s-" + hint), Passphrase: []byte("p-" + hint),
	}
}

func TestWallets(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets)
	}{
		{"round trip", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			w := seedWallet(t, db, u, addrA)

			got, err := wallets.Get(ctx, u.ID, w.ID)
			require.NoError(t, err)
			assert.Equal(t, w, got)
		}},
		{"an address is linked once per user, in any case", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			seedWallet(t, db, u, addrA)

			dup := domain.Wallet{ID: domain.WalletID(newID()), UserID: u.ID, Address: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", CreatedAt: at}
			assert.ErrorIs(t, wallets.Create(ctx, dup), domain.ErrWalletExists)

			other := seedUser(t, db, "b@x.io", domain.RoleUser)
			seedWallet(t, db, other, addrA) // another user may follow the same address
		}},
		{"wallets of other users are not found", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			owner := seedUser(t, db, "a@x.io", domain.RoleUser)
			stranger := seedUser(t, db, "b@x.io", domain.RoleUser)
			w := seedWallet(t, db, owner, addrA)

			_, err := wallets.Get(ctx, stranger.ID, w.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
			assert.ErrorIs(t, wallets.Delete(ctx, stranger.ID, w.ID), domain.ErrNotFound)

			list, err := wallets.ListForUser(ctx, stranger.ID)
			require.NoError(t, err)
			assert.Empty(t, list)
		}},
		{"listing flags stored credentials", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			plain := seedWallet(t, db, u, addrA)
			withCreds := seedWallet(t, db, u, "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359")
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(withCreds, "abcd", at)))

			list, err := wallets.ListForUser(ctx, u.ID)
			require.NoError(t, err)
			require.Len(t, list, 2)
			assert.Equal(t, plain.ID, list[0].ID)
			assert.False(t, list[0].HasCredentials)
			assert.True(t, list[1].HasCredentials)

			n, err := wallets.CountForUser(ctx, u.ID)
			require.NoError(t, err)
			assert.Equal(t, 2, n)
		}},
		{"credentials are replaced, keeping their creation time", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "old1", at)))

			next := sealed(w, "new2", at.Add(time.Hour))
			next.Info.CreatedAt = at.Add(time.Hour) // ignored on update
			require.NoError(t, wallets.SaveCredentials(ctx, next))

			got, err := wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, "new2", got.Info.APIKeyHint)
			assert.Equal(t, []byte("s-new2"), got.Secret)
			assert.Equal(t, at, got.Info.CreatedAt)
			assert.Equal(t, at.Add(time.Hour), got.Info.UpdatedAt)
			assert.Equal(t, domain.SignaturePolyProxy, got.Info.SignatureType)
		}},
		{"a key is kept with the credentials, and pasted ones without it clear it", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			w := seedWallet(t, db, u, addrA)

			withKey := sealed(w, "abcd", at)
			withKey.PrivateKey = []byte("sealed-key")
			require.NoError(t, wallets.SaveCredentials(ctx, withKey))

			got, err := wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, []byte("sealed-key"), got.PrivateKey)
			assert.True(t, got.Info.HasKey)

			listed, err := wallets.Get(ctx, u.ID, w.ID)
			require.NoError(t, err)
			assert.True(t, listed.HasKey)

			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "efgh", at.Add(time.Hour))))

			got, err = wallets.Credentials(ctx, w.ID)
			require.NoError(t, err)
			assert.Nil(t, got.PrivateKey)
			assert.False(t, got.Info.HasKey)

			listed, err = wallets.Get(ctx, u.ID, w.ID)
			require.NoError(t, err)
			assert.True(t, listed.HasCredentials)
			assert.False(t, listed.HasKey)
		}},
		{"unlinking removes credentials", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			w := seedWallet(t, db, u, addrA)
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "abcd", at)))
			require.NoError(t, wallets.Delete(ctx, u.ID, w.ID))

			_, err := wallets.Credentials(ctx, w.ID)
			assert.ErrorIs(t, err, domain.ErrNotFound)
		}},
		{"credentials can be deleted on their own", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			require.NoError(t, wallets.SaveCredentials(ctx, sealed(w, "abcd", at)))
			require.NoError(t, wallets.DeleteCredentials(ctx, w.ID))
			assert.ErrorIs(t, wallets.DeleteCredentials(ctx, w.ID), domain.ErrNotFound)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewWallets(db))
		})
	}
}

func TestWalletHeartbeat(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	repo := postgres.NewWallets(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)
	w := seedWallet(t, db, alice, "0x1111111111111111111111111111111111111111")

	got, err := repo.Get(ctx, alice.ID, w.ID)
	require.NoError(t, err)
	assert.False(t, got.Heartbeat, "off until its owner turns it on (D214)")

	require.NoError(t, repo.SetHeartbeat(ctx, alice.ID, w.ID, true))

	got, err = repo.Get(ctx, alice.ID, w.ID)
	require.NoError(t, err)
	assert.True(t, got.Heartbeat)

	require.ErrorIs(t, repo.SetHeartbeat(ctx, bob.ID, w.ID, false), domain.ErrNotFound, "someone else's")
}
