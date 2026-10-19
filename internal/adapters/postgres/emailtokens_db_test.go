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
)

func TestEmailTokens(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, tokens *postgres.EmailTokens)
	}{
		{"used once, by purpose, until it expires", func(t *testing.T, db *postgres.DB, tokens *postgres.EmailTokens) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			tok := domain.EmailToken{UserID: u.ID, Purpose: domain.EmailTokenVerify, CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour)}
			require.NoError(t, tokens.Create(ctx, tok, []byte("hash-1")))

			_, err := tokens.Take(ctx, []byte("hash-1"), domain.EmailTokenReset, at)
			require.ErrorIs(t, err, domain.ErrNotFound, "another purpose")

			_, err = tokens.Take(ctx, []byte("hash-1"), domain.EmailTokenVerify, at.Add(25*time.Hour))
			require.ErrorIs(t, err, domain.ErrNotFound, "expired")

			id, err := tokens.Take(ctx, []byte("hash-1"), domain.EmailTokenVerify, at.Add(time.Hour))
			require.NoError(t, err)
			assert.Equal(t, u.ID, id)

			_, err = tokens.Take(ctx, []byte("hash-1"), domain.EmailTokenVerify, at.Add(time.Hour))
			require.ErrorIs(t, err, domain.ErrNotFound, "used")
		}},
		{"counted, deleted", func(t *testing.T, db *postgres.DB, tokens *postgres.EmailTokens) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			other := seedUser(t, db, "b@x.io", domain.RoleUser)

			for i, h := range []string{"h1", "h2", "h3"} {
				created := at.Add(time.Duration(i) * time.Hour)
				require.NoError(t, tokens.Create(ctx, domain.EmailToken{UserID: u.ID, Purpose: domain.EmailTokenVerify, CreatedAt: created, ExpiresAt: created.Add(time.Hour)}, []byte(h)))
			}
			require.NoError(t, tokens.Create(ctx, domain.EmailToken{UserID: u.ID, Purpose: domain.EmailTokenReset, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}, []byte("r1")))
			require.NoError(t, tokens.Create(ctx, domain.EmailToken{UserID: other.ID, Purpose: domain.EmailTokenVerify, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}, []byte("o1")))

			n, err := tokens.CountSince(ctx, u.ID, domain.EmailTokenVerify, at.Add(time.Hour))
			require.NoError(t, err)
			assert.Equal(t, 2, n)

			require.NoError(t, tokens.DeleteForUser(ctx, u.ID, domain.EmailTokenVerify))
			n, err = tokens.CountSince(ctx, u.ID, domain.EmailTokenVerify, at)
			require.NoError(t, err)
			assert.Zero(t, n)

			n, err = tokens.CountSince(ctx, u.ID, domain.EmailTokenReset, at)
			require.NoError(t, err)
			assert.Equal(t, 1, n, "the other purpose stays")

			gone, err := tokens.DeleteExpired(ctx, at.Add(time.Hour))
			require.NoError(t, err)
			assert.Equal(t, int64(2), gone)
		}},
		{"go with their account", func(t *testing.T, db *postgres.DB, tokens *postgres.EmailTokens) {
			u := seedUnverified(t, db, "a@x.io", at)
			require.NoError(t, tokens.Create(ctx, domain.EmailToken{UserID: u.ID, Purpose: domain.EmailTokenVerify, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}, []byte("h1")))
			require.NoError(t, postgres.NewUsers(db).DeleteUnverified(ctx, u.ID))

			_, err := tokens.Take(ctx, []byte("h1"), domain.EmailTokenVerify, at)
			require.ErrorIs(t, err, domain.ErrNotFound)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewEmailTokens(db))
		})
	}
}

func seedUnverified(t *testing.T, db *postgres.DB, email string, created time.Time) domain.User {
	t.Helper()

	u := domain.User{ID: domain.UserID(newID()), Email: email, Role: domain.RoleUser, CreatedAt: created, UpdatedAt: created}
	require.NoError(t, postgres.NewUsers(db).Create(context.Background(), u, "hash"))

	return u
}

func TestUnverifiedUsers(t *testing.T) {
	ctx := context.Background()

	t.Run("verified, then kept", func(t *testing.T) {
		db := fresh(t)
		users := postgres.NewUsers(db)
		u := seedUnverified(t, db, "a@x.io", at)

		got, err := users.ByID(ctx, u.ID)
		require.NoError(t, err)
		assert.True(t, got.EmailVerifiedAt.IsZero())

		require.NoError(t, users.SetEmailVerified(ctx, u.ID, at.Add(time.Hour)))

		got, err = users.ByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, at.Add(time.Hour), got.EmailVerifiedAt)

		require.ErrorIs(t, users.DeleteUnverified(ctx, u.ID), domain.ErrNotFound, "verified now")
	})

	t.Run("pruned after their time", func(t *testing.T) {
		db := fresh(t)
		users := postgres.NewUsers(db)

		old := seedUnverified(t, db, "old@x.io", at)
		young := seedUnverified(t, db, "young@x.io", at.Add(6*24*time.Hour))
		verified := seedUser(t, db, "v@x.io", domain.RoleUser)
		require.NoError(t, users.SetEmailVerified(ctx, verified.ID, at))

		n, err := users.PruneUnverified(ctx, at.Add(time.Hour))
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)

		_, err = users.ByID(ctx, old.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		for _, id := range []domain.UserID{young.ID, verified.ID} {
			_, err = users.ByID(ctx, id)
			require.NoError(t, err)
		}
	})
}
