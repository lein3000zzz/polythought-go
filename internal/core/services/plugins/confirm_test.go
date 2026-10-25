package plugins_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
)

// holder holds what it is asked to where the admin has it wait, as the confirmations
// service does.
type holder struct {
	asks []domain.Ask
}

func (h *holder) Hold(_ context.Context, p auth.Principal, a domain.Ask) error {
	if !p.User.Unconfirmed.Waits(a.Kind) || p.Confirmed {
		return nil
	}

	h.asks = append(h.asks, a)

	return &domain.AwaitingError{Confirmation: domain.Confirmation{ID: "c-1"}}
}

// An admin's agent's work on plugins waits for the admin where they say so (D411):
// approving a version under a switch of its own, every other change under the other;
// installing waits for nothing, as nothing installed runs before an approval.
func TestAgentsOnPlugins(t *testing.T) {
	ctx := context.Background()
	agent := auth.Principal{User: admin.User, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeAdmin}}
	confirmed := agent
	confirmed.Confirmed = true

	setup := func(t *testing.T) (fixture, *holder) {
		t.Helper()

		f := newFixture()
		h := &holder{}
		f.s.UseConfirmations(h)
		installed(t, f, "weather", "1.0.0")

		return f, h
	}

	t.Run("installing goes ahead; approving waits, saying what it asks for", func(t *testing.T) {
		f, h := setup(t)

		_, err := f.s.Install(ctx, agent, archive("news", "1.0.0", "fetch:news.example"))
		require.NoError(t, err)
		assert.Empty(t, h.asks)

		require.ErrorIs(t, f.s.Approve(ctx, agent, "news", "1.0.0", []string{"fetch:news.example"}), domain.ErrAwaitsConfirmation)
		require.Len(t, h.asks, 1)
		assert.Equal(t, domain.Ask{
			Kind: domain.ConfirmPluginApprove,
			What: "Approve the plugin news 1.0.0, to run on the server, with the permissions fetch:news.example",
			Payload: plugins.Asked{
				Op: plugins.OpApprove, ID: "news", Version: "1.0.0", Permissions: []string{"fetch:news.example"},
			},
		}, h.asks[0])

		ip, _ := f.repo.Get(ctx, "news")
		assert.Empty(t, ip.Version, "not approved yet")

		require.NoError(t, f.s.Approve(ctx, confirmed, "news", "1.0.0", []string{"fetch:news.example"}))
		ip, _ = f.repo.Get(ctx, "news")
		assert.Equal(t, "1.0.0", ip.Version)
	})

	t.Run("approving what it does not ask for is refused at once, never asked", func(t *testing.T) {
		f, h := setup(t)

		_, err := f.s.Install(ctx, admin, archive("news", "1.0.0", "fetch:news.example"))
		require.NoError(t, err)

		require.ErrorIs(t, f.s.Approve(ctx, agent, "news", "1.0.0", []string{"fetch:*"}), domain.ErrInvalidInput)
		assert.Empty(t, h.asks)
	})

	t.Run("every other change waits under the other switch", func(t *testing.T) {
		f, h := setup(t)

		_, err := f.s.Install(ctx, admin, archive("weather", "1.1.0"))
		require.NoError(t, err)

		for what, do := range map[string]func(auth.Principal) error{
			"Remove the plugin weather's version 1.1.0":     func(p auth.Principal) error { return f.s.RemoveVersion(ctx, p, "weather", "1.1.0") },
			"Disable the plugin weather":                    func(p auth.Principal) error { return f.s.SetEnabled(ctx, p, "weather", false) },
			"Let only 1 chosen user use the plugin weather": func(p auth.Principal) error { return f.s.SetUsers(ctx, p, "weather", false, []domain.UserID{"alice"}) },
			"Start the plugin weather again":                func(p auth.Principal) error { return f.s.Start(ctx, p, "weather") },
			"Set the plugin weather's caps":                 func(p auth.Principal) error { return f.s.SetCaps(ctx, p, "weather", domain.PluginCaps{DataMiB: 64}) },
			"Uninstall the plugin weather":                  func(p auth.Principal) error { return f.s.Uninstall(ctx, p, "weather", false) },
		} {
			h.asks = nil

			require.ErrorIs(t, do(agent), domain.ErrAwaitsConfirmation, what)
			require.Len(t, h.asks, 1, what)
			assert.Equal(t, domain.ConfirmPluginChange, h.asks[0].Kind, what)
			assert.Contains(t, h.asks[0].What, what)
		}

		ip, _ := f.repo.Get(ctx, "weather")
		assert.True(t, ip.Enabled, "nothing done")
		assert.Len(t, ip.Versions, 2)
	})

	t.Run("where the admin lets them go, and the admin's own", func(t *testing.T) {
		f, h := setup(t)
		lets := agent
		lets.User.Unconfirmed.PluginChange = true

		require.NoError(t, f.s.SetEnabled(ctx, lets, "weather", false))
		require.NoError(t, f.s.SetEnabled(ctx, admin, "weather", true))
		assert.Empty(t, h.asks)

		_, err := f.s.Install(ctx, admin, archive("news", "1.0.0"))
		require.NoError(t, err)
		require.ErrorIs(t, f.s.Approve(ctx, lets, "news", "1.0.0", nil), domain.ErrAwaitsConfirmation, "approving has its own switch")
	})

	t.Run("its settings wait, a secret never the agent's", func(t *testing.T) {
		f := newFixture()
		store := &settingsStore{stored: map[string]map[string]ports.StoredPluginSetting{}}
		f.s = plugins.New(f.repo, f.files, f.runtime, f.workflows, logging.Noop(), plugins.WithSettings(store, box{}))
		h := &holder{}
		f.s.UseConfirmations(h)

		f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.0"}] = withSettings("weather", "1.0.0")
		_, err := f.s.Adopt(ctx, admin, "weather", "1.0.0")
		require.NoError(t, err)
		require.NoError(t, f.s.Approve(ctx, admin, "weather", "1.0.0", nil))
		require.NoError(t, f.s.Load(ctx))

		units := map[string]json.RawMessage{"units": json.RawMessage(`"kelvin"`)}
		require.ErrorIs(t, f.s.SetSettings(ctx, agent, "weather", units), domain.ErrAwaitsConfirmation)
		require.Len(t, h.asks, 1)
		assert.Equal(t, plugins.Asked{Op: plugins.OpSettings, ID: "weather", Settings: units}, h.asks[0].Payload)
		assert.Empty(t, store.audits, "nothing saved")

		lets := agent
		lets.User.Unconfirmed.PluginChange = true

		err = f.s.SetSettings(ctx, lets, "weather", map[string]json.RawMessage{"api_key": json.RawMessage(`"k-1"`)})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "API key is secret: an admin sets it at a browser")
		assert.Empty(t, store.audits)
	})
}
