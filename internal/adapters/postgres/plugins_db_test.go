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
)

func TestPlugins(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	repo := postgres.NewPlugins(db)
	admin := seedUser(t, db, "admin@example.com", domain.RoleAdmin)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	v1 := domain.PluginVersion{
		PluginID: "weather", Version: "1.0.0", Manifest: json.RawMessage(`{"id": "weather", "version": "1.0.0"}`),
		Signer: "Alice", KeyID: "E7620F1842B4E81F", InstalledBy: admin.ID, InstalledAt: at,
	}

	t.Run("installed: enabled, for everyone, waiting for approval", func(t *testing.T) {
		require.NoError(t, repo.AddVersion(ctx, v1, domain.PluginAudit{PluginID: "weather", Version: "1.0.0", Action: domain.PluginInstalled, By: admin.ID, At: at}))

		p, err := repo.Get(ctx, "weather")
		require.NoError(t, err)
		assert.Empty(t, p.Version, "nothing runs before an approval")
		assert.True(t, p.Enabled)
		assert.True(t, p.Everyone)
		require.Len(t, p.Versions, 1)
		assert.False(t, p.Versions[0].Approved())
		assert.Equal(t, "Alice", p.Versions[0].Signer)
		assert.JSONEq(t, string(v1.Manifest), string(p.Versions[0].Manifest))

		require.ErrorIs(t, repo.AddVersion(ctx, v1, domain.PluginAudit{PluginID: "weather", Action: domain.PluginInstalled, At: at}), domain.ErrPluginInstalled)

		_, err = repo.Get(ctx, "none")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("approved, then an upgrade beside it", func(t *testing.T) {
		require.NoError(t, repo.Approve(ctx, domain.PluginAudit{PluginID: "weather", Version: "1.0.0", Action: domain.PluginApproved, By: admin.ID, At: at.Add(time.Minute)}))

		v2 := v1
		v2.Version, v2.InstalledAt = "1.1.0", at.Add(time.Hour)
		require.NoError(t, repo.AddVersion(ctx, v2, domain.PluginAudit{PluginID: "weather", Version: "1.1.0", Action: domain.PluginInstalled, By: admin.ID, At: v2.InstalledAt}))

		p, err := repo.Get(ctx, "weather")
		require.NoError(t, err)
		assert.Equal(t, "1.0.0", p.Version, "the approved one runs meanwhile (D318)")
		require.Len(t, p.Versions, 2)
		assert.Equal(t, "1.1.0", p.Versions[0].Version, "newest first")

		old, ok := p.Find("1.0.0")
		require.True(t, ok)
		assert.True(t, old.Approved())
		assert.Equal(t, admin.ID, old.ApprovedBy)

		require.NoError(t, repo.Approve(ctx, domain.PluginAudit{PluginID: "weather", Version: "1.1.0", Action: domain.PluginApproved, By: admin.ID, At: at.Add(2 * time.Hour)}))
		require.NoError(t, repo.RemoveVersion(ctx, domain.PluginAudit{PluginID: "weather", Version: "1.0.0", Action: domain.PluginRemoved, At: at.Add(3 * time.Hour)}))

		p, _ = repo.Get(ctx, "weather")
		assert.Equal(t, "1.1.0", p.Version)
		assert.Len(t, p.Versions, 1)

		require.ErrorIs(t, repo.Approve(ctx, domain.PluginAudit{PluginID: "weather", Version: "9.9.9", Action: domain.PluginApproved, At: at}), domain.ErrNotFound)
	})

	t.Run("enabled, disabled, and who may use it", func(t *testing.T) {
		require.NoError(t, repo.SetEnabled(ctx, false, domain.PluginAudit{PluginID: "weather", Action: domain.PluginDisabled, By: admin.ID, At: at}))
		require.NoError(t, repo.SetUsers(ctx, false, []domain.UserID{bob.ID}, domain.PluginAudit{PluginID: "weather", Action: domain.PluginUsers, By: admin.ID, At: at}))

		p, _ := repo.Get(ctx, "weather")
		assert.False(t, p.Enabled)
		assert.False(t, p.Everyone)
		assert.Equal(t, []domain.UserID{bob.ID}, p.Users)
		assert.True(t, p.Allows(bob.ID))
		assert.False(t, p.Allows(admin.ID), "admins too, when an admin chose")

		require.NoError(t, repo.SetUsers(ctx, true, nil, domain.PluginAudit{PluginID: "weather", Action: domain.PluginUsers, At: at}))
		p, _ = repo.Get(ctx, "weather")
		assert.True(t, p.Everyone)
		assert.Empty(t, p.Users)

		list, err := repo.List(ctx)
		require.NoError(t, err)
		require.Len(t, list, 1)
		assert.Equal(t, "weather", list[0].ID)
	})

	t.Run("uninstalled, what was done kept", func(t *testing.T) {
		require.NoError(t, repo.Delete(ctx, domain.PluginAudit{PluginID: "weather", Action: domain.PluginUninstalled, By: admin.ID, At: at.Add(4 * time.Hour)}))

		_, err := repo.Get(ctx, "weather")
		require.ErrorIs(t, err, domain.ErrNotFound)

		history, err := repo.History(ctx, "weather", 50)
		require.NoError(t, err)

		actions := make([]string, len(history))
		for i, h := range history {
			actions[i] = h.Action
		}

		assert.Equal(t, domain.PluginUninstalled, actions[0], "newest first")
		assert.Contains(t, actions, domain.PluginInstalled)
		assert.Equal(t, admin.ID, history[0].By)

		require.ErrorIs(t, repo.Delete(ctx, domain.PluginAudit{PluginID: "weather", Action: domain.PluginUninstalled, At: at}), domain.ErrNotFound)
	})
}
