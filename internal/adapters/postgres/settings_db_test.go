//go:build db

package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func TestSettings(t *testing.T) {
	ctx := context.Background()

	t.Run("saves, replaces and resets, auditing each change", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewSettings(db)
		admin := seedUser(t, db, "root@example.com", domain.RoleAdmin)

		all, err := repo.All(ctx)
		require.NoError(t, err)
		assert.Empty(t, all)

		require.NoError(t, repo.Apply(ctx, []ports.SettingChange{
			{Key: "auth.registration", Value: json.RawMessage(`"open"`), OldValue: json.RawMessage(`"invite"`), NewValue: json.RawMessage(`"open"`)},
			{Key: "telegram.token", Sealed: []byte{1, 2, 3}, Secret: true},
		}, admin.ID, at))

		require.NoError(t, repo.Apply(ctx, []ports.SettingChange{
			{Key: "auth.registration", Value: json.RawMessage(`"closed"`), OldValue: json.RawMessage(`"open"`), NewValue: json.RawMessage(`"closed"`)},
			{Key: "http.trusted_proxies", Value: json.RawMessage(`["10.0.0.0/8"]`), OldValue: json.RawMessage(`[]`), NewValue: json.RawMessage(`["10.0.0.0/8"]`)},
		}, admin.ID, at.Add(time.Minute)))

		all, err = repo.All(ctx)
		require.NoError(t, err)
		assert.Equal(t, []ports.StoredSetting{
			{Key: "auth.registration", Value: json.RawMessage(`"closed"`)},
			{Key: "http.trusted_proxies", Value: json.RawMessage(`["10.0.0.0/8"]`)},
			{Key: "telegram.token", Sealed: []byte{1, 2, 3}},
		}, all)

		require.NoError(t, repo.Apply(ctx, []ports.SettingChange{
			{Key: "auth.registration", Reset: true, OldValue: json.RawMessage(`"closed"`), NewValue: json.RawMessage(`"invite"`)},
			{Key: "telegram.token", Reset: true, Secret: true},
		}, admin.ID, at.Add(2*time.Minute)))

		all, err = repo.All(ctx)
		require.NoError(t, err)
		require.Len(t, all, 1)
		assert.Equal(t, "http.trusted_proxies", all[0].Key)

		page, err := repo.History(ctx, ports.PageFilter{Limit: 4})
		require.NoError(t, err)
		require.Len(t, page.Items, 4)
		assert.NotEmpty(t, page.NextCursor)

		newest := page.Items[0]
		assert.Equal(t, "telegram.token", newest.Key)
		assert.True(t, newest.Secret)
		assert.Nil(t, newest.OldValue)
		assert.Nil(t, newest.NewValue)
		assert.Equal(t, admin.ID, newest.ActorID)
		assert.Equal(t, "root@example.com", newest.ActorEmail)
		assert.Equal(t, at.Add(2*time.Minute), newest.At)

		reset := page.Items[1]
		assert.Equal(t, "auth.registration", reset.Key)
		assert.JSONEq(t, `"closed"`, string(reset.OldValue))
		assert.JSONEq(t, `"invite"`, string(reset.NewValue))

		rest, err := repo.History(ctx, ports.PageFilter{Limit: 4, Cursor: page.NextCursor})
		require.NoError(t, err)
		require.Len(t, rest.Items, 2)
		assert.Empty(t, rest.NextCursor)
		assert.Equal(t, at, rest.Items[1].At, "oldest last")

		_, err = repo.History(ctx, ports.PageFilter{Cursor: "nope"})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
	})

	t.Run("a failed change saves nothing", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewSettings(db)
		admin := seedUser(t, db, "root@example.com", domain.RoleAdmin)

		err := repo.Apply(ctx, []ports.SettingChange{
			{Key: "auth.registration", Value: json.RawMessage(`"open"`)},
			{Key: "broken"}, // neither a value nor a sealed secret
		}, admin.ID, at)
		require.Error(t, err)

		all, err := repo.All(ctx)
		require.NoError(t, err)
		assert.Empty(t, all)

		page, err := repo.History(ctx, ports.PageFilter{})
		require.NoError(t, err)
		assert.Empty(t, page.Items)
	})

	t.Run("history outlives the actor's account", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewSettings(db)
		admin := seedUser(t, db, "root@example.com", domain.RoleAdmin)

		require.NoError(t, repo.Apply(ctx, []ports.SettingChange{{Key: "log.level", Value: json.RawMessage(`"debug"`)}}, admin.ID, at))

		_, err := db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, string(admin.ID))
		require.NoError(t, err)

		all, err := repo.All(ctx)
		require.NoError(t, err)
		assert.Len(t, all, 1)

		page, err := repo.History(ctx, ports.PageFilter{})
		require.NoError(t, err)
		require.Len(t, page.Items, 1)
		assert.Empty(t, page.Items[0].ActorID)
		assert.Empty(t, page.Items[0].ActorEmail)
	})
}
