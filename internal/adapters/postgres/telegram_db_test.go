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

func TestTelegramLinks(t *testing.T) {
	ctx := context.Background()

	t.Run("link, relink and unlink", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewTelegramLinks(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

		_, err := repo.ForUser(ctx, alice.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		link := domain.TelegramLink{UserID: alice.ID, TelegramUserID: 111, ChatID: 111, Username: "alice_tg", FirstName: "Alice", LinkedAt: at}
		require.NoError(t, repo.Save(ctx, link))

		got, err := repo.ForUser(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, link, got)

		got, err = repo.ForTelegramUser(ctx, 111)
		require.NoError(t, err)
		assert.Equal(t, alice.ID, got.UserID)

		// Alice links another Telegram account: it replaces the first.
		link.TelegramUserID, link.ChatID, link.Username = 222, 222, "alice2"
		require.NoError(t, repo.Save(ctx, link))
		_, err = repo.ForTelegramUser(ctx, 111)
		require.ErrorIs(t, err, domain.ErrNotFound)

		// The same Telegram account links Bob: Alice loses it.
		require.NoError(t, repo.Save(ctx, domain.TelegramLink{UserID: bob.ID, TelegramUserID: 222, ChatID: 222, LinkedAt: at}))
		_, err = repo.ForUser(ctx, alice.ID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		got, err = repo.ForTelegramUser(ctx, 222)
		require.NoError(t, err)
		assert.Equal(t, bob.ID, got.UserID)

		require.NoError(t, repo.Delete(ctx, bob.ID))
		require.ErrorIs(t, repo.Delete(ctx, bob.ID), domain.ErrNotFound)
	})

	t.Run("codes are single use and expire", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewTelegramLinks(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

		require.NoError(t, repo.SaveCode(ctx, alice.ID, []byte("first"), at.Add(10*time.Minute)))
		require.NoError(t, repo.SaveCode(ctx, alice.ID, []byte("second"), at.Add(10*time.Minute)))

		_, err := repo.ConsumeCode(ctx, []byte("first"), at)
		require.ErrorIs(t, err, domain.ErrNotFound, "a new code replaces the old one")

		user, err := repo.ConsumeCode(ctx, []byte("second"), at.Add(time.Minute))
		require.NoError(t, err)
		assert.Equal(t, alice.ID, user)

		_, err = repo.ConsumeCode(ctx, []byte("second"), at.Add(time.Minute))
		require.ErrorIs(t, err, domain.ErrNotFound, "used once")

		require.NoError(t, repo.SaveCode(ctx, alice.ID, []byte("late"), at.Add(10*time.Minute)))
		_, err = repo.ConsumeCode(ctx, []byte("late"), at.Add(10*time.Minute))
		require.ErrorIs(t, err, domain.ErrNotFound, "expired")
	})

	t.Run("links go with the account", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewTelegramLinks(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		require.NoError(t, repo.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 1, ChatID: 1, LinkedAt: at}))
		require.NoError(t, repo.SaveCode(ctx, alice.ID, []byte("c"), at.Add(time.Minute)))

		_, err := db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, string(alice.ID))
		require.NoError(t, err)

		_, err = repo.ForTelegramUser(ctx, 1)
		require.ErrorIs(t, err, domain.ErrNotFound)
		_, err = repo.ConsumeCode(ctx, []byte("c"), at)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}
