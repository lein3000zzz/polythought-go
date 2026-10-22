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

func TestPlugins(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	repo := postgres.NewPlugins(db)
	admin := seedUser(t, db, "admin@example.com", domain.RoleAdmin)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	v1 := domain.PluginVersion{
		PluginID: "weather", Version: "1.0.0", Manifest: json.RawMessage(`{"id": "weather", "version": "1.0.0"}`),
		Signer: "Alice", KeyID: "E7620F1842B4E81F", Digest: "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
		InstalledBy: admin.ID, InstalledAt: at,
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
		assert.Equal(t, v1.Digest, p.Versions[0].Digest)
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

	t.Run("its signer as the trusted keys now say, its digest once (P16, P21)", func(t *testing.T) {
		audit := domain.PluginAudit{PluginID: "weather", Version: "1.1.0", Action: domain.PluginSigner, At: at.Add(3*time.Hour + time.Minute)}
		require.NoError(t, repo.SetSigner(ctx, "", "E7620F1842B4E81F", audit))

		p, _ := repo.Get(ctx, "weather")
		assert.Empty(t, p.Versions[0].Signer)
		assert.Equal(t, "E7620F1842B4E81F", p.Versions[0].KeyID)

		audit.Action = domain.PluginDigest
		require.ErrorIs(t, repo.SetDigest(ctx, "other", audit), domain.ErrNotFound, "never in place of one")

		_, err := db.Pool.Exec(ctx, `UPDATE plugin_versions SET digest = '' WHERE plugin_id = 'weather'`) // as recorded before 00035
		require.NoError(t, err)
		require.NoError(t, repo.SetDigest(ctx, "recorded", audit))

		p, _ = repo.Get(ctx, "weather")
		assert.Equal(t, "recorded", p.Versions[0].Digest)

		history, _ := repo.History(ctx, "weather", 2)
		assert.Equal(t, []string{domain.PluginDigest, domain.PluginSigner}, []string{history[0].Action, history[1].Action})

		audit.Version = "9.9.9"
		require.ErrorIs(t, repo.SetSigner(ctx, "Bob", "", audit), domain.ErrNotFound)
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

	t.Run("its own settings, a secret sealed, reset to the default", func(t *testing.T) {
		change := []ports.PluginSettingChange{
			{Key: "units", Value: json.RawMessage(`"kelvin"`)},
			{Key: "api_key", Sealed: []byte("sealed bytes")},
		}
		require.NoError(t, repo.SetSettings(ctx, "weather", change, domain.PluginAudit{PluginID: "weather", Action: domain.PluginSettings, Detail: "units, api_key", By: admin.ID, At: at}))

		got, err := repo.Settings(ctx, "weather")
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "api_key", got[0].Key)
		assert.Equal(t, []byte("sealed bytes"), got[0].Sealed)
		assert.Nil(t, got[0].Value)
		assert.JSONEq(t, `"kelvin"`, string(got[1].Value))

		require.NoError(t, repo.SetSettings(ctx, "weather", []ports.PluginSettingChange{{Key: "units"}}, domain.PluginAudit{PluginID: "weather", Action: domain.PluginSettings, At: at}))
		got, _ = repo.Settings(ctx, "weather")
		assert.Len(t, got, 1, "reset to its default")
	})

	t.Run("its own caps, then the Config page's again (D346)", func(t *testing.T) {
		require.NoError(t, repo.SetCaps(ctx, domain.PluginCaps{DataMiB: 2048}, domain.PluginAudit{PluginID: "weather", Action: domain.PluginCapsSet, By: admin.ID, At: at}))

		p, _ := repo.Get(ctx, "weather")
		assert.Equal(t, domain.PluginCaps{DataMiB: 2048}, p.Caps)

		require.NoError(t, repo.SetCaps(ctx, domain.PluginCaps{}, domain.PluginAudit{PluginID: "weather", Action: domain.PluginCapsSet, At: at}))
		p, _ = repo.Get(ctx, "weather")
		assert.Zero(t, p.Caps)

		require.ErrorIs(t, repo.SetCaps(ctx, domain.PluginCaps{MemoryMiB: 512}, domain.PluginAudit{PluginID: "none", Action: domain.PluginCapsSet, At: at}), domain.ErrNotFound)
	})

	t.Run("uninstalled, what was done kept", func(t *testing.T) {
		require.NoError(t, repo.Delete(ctx, domain.PluginAudit{PluginID: "weather", Action: domain.PluginUninstalled, By: admin.ID, At: at.Add(4 * time.Hour)}, false))

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

		require.ErrorIs(t, repo.Delete(ctx, domain.PluginAudit{PluginID: "weather", Action: domain.PluginUninstalled, At: at}, false), domain.ErrNotFound)
	})

	t.Run("what its nodes stored: left behind, then forgotten, or gone with it (D342, D348)", func(t *testing.T) {
		store := postgres.NewPluginStore(db)

		w := newWorkflow(bob.ID, "stores")
		require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))

		node := func(plugin string) ports.PluginNode {
			return ports.PluginNode{Workflow: w.ID, Node: "n", Plugin: plugin}
		}

		for _, plugin := range []string{"weather", "news"} {
			require.NoError(t, store.Set(ctx, node(plugin), "k", json.RawMessage(`"v"`), 1024, at))
		}

		news := v1
		news.PluginID = "news"
		require.NoError(t, repo.AddVersion(ctx, news, domain.PluginAudit{PluginID: "news", Version: "1.0.0", Action: domain.PluginInstalled, At: at}))

		stored, err := repo.StoreBytes(ctx)
		require.NoError(t, err)
		assert.Equal(t, map[string]int64{"weather": 4, "news": 4}, stored, "weather's kept as it was uninstalled above, news's installed")

		for _, id := range []string{"weather", "news"} {
			require.NoError(t, repo.Bind(ctx, domain.PluginKey{PluginID: id, KeyID: "K-" + id, Signer: "Alice", BoundAt: at}))
		}

		require.NoError(t, repo.ForgetStores(ctx, domain.PluginAudit{PluginID: "news", Action: domain.PluginDataRemoved, At: at}))
		_, ok, err := store.Get(ctx, node("news"), "k")
		require.NoError(t, err)
		assert.True(t, ok, "never an installed plugin's")

		_, bound, _ := repo.Key(ctx, "news")
		assert.True(t, bound, "nor its key")

		require.NoError(t, repo.ForgetStores(ctx, domain.PluginAudit{PluginID: "weather", Action: domain.PluginDataRemoved, By: admin.ID, At: at.Add(5 * time.Hour)}))
		stored, _ = repo.StoreBytes(ctx)
		assert.Equal(t, map[string]int64{"news": 4}, stored)

		_, bound, _ = repo.Key(ctx, "weather")
		assert.False(t, bound, "its key gone with its data (D366)")

		history, _ := repo.History(ctx, "weather", 1)
		require.Len(t, history, 1)
		assert.Equal(t, domain.PluginDataRemoved, history[0].Action)

		require.NoError(t, repo.Delete(ctx, domain.PluginAudit{PluginID: "news", Action: domain.PluginUninstalled, At: at}, true))
		_, ok, _ = store.Get(ctx, node("news"), "k")
		assert.False(t, ok, "gone with it")

		stored, _ = repo.StoreBytes(ctx)
		assert.Empty(t, stored)

		_, bound, _ = repo.Key(ctx, "news")
		assert.False(t, bound, "its key too, uninstalled with its data")
	})

	t.Run("a plugin's ID bound to its first key, which stays (D365)", func(t *testing.T) {
		_, ok, err := repo.Key(ctx, "rain")
		require.NoError(t, err)
		assert.False(t, ok)

		require.NoError(t, repo.Bind(ctx, domain.PluginKey{PluginID: "rain", KeyID: "K1", Signer: "Alice", BoundAt: at}))
		require.NoError(t, repo.Bind(ctx, domain.PluginKey{PluginID: "rain", KeyID: "K2", Signer: "Mallory", BoundAt: at}))

		k, ok, err := repo.Key(ctx, "rain")
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "K1", k.KeyID, "the first stays")
		assert.Equal(t, "Alice", k.Signer)
		assert.True(t, at.Equal(k.BoundAt), "bound at %s", k.BoundAt)
	})
}
