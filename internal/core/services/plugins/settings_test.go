package plugins_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
)

// settingsStore keeps plugins' settings in memory.
type settingsStore struct {
	mu     sync.Mutex
	stored map[string]map[string]ports.StoredPluginSetting
	audits []domain.PluginAudit
}

func (s *settingsStore) Settings(_ context.Context, plugin string) ([]ports.StoredPluginSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []ports.StoredPluginSetting
	for _, st := range s.stored[plugin] {
		out = append(out, st)
	}

	return out, nil
}

func (s *settingsStore) SetSettings(_ context.Context, plugin string, changes []ports.PluginSettingChange, a domain.PluginAudit) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stored[plugin] == nil {
		s.stored[plugin] = map[string]ports.StoredPluginSetting{}
	}

	for _, c := range changes {
		if c.Value == nil && c.Sealed == nil {
			delete(s.stored[plugin], c.Key)
			continue
		}

		s.stored[plugin][c.Key] = ports.StoredPluginSetting(c)
	}

	s.audits = append(s.audits, a)

	return nil
}

// box seals by marking the text with what it is bound to.
type box struct{}

func (box) Seal(_ context.Context, plain, aad []byte) ([]byte, error) {
	return []byte(string(aad) + "|" + string(plain)), nil
}

func (box) Open(_ context.Context, sealed, aad []byte) ([]byte, error) {
	bound, plain, ok := strings.Cut(string(sealed), "|")
	if !ok || bound != string(aad) {
		return nil, errors.New("not sealed for this")
	}

	return []byte(plain), nil
}

func withSettings(id, version string) ports.PluginFile {
	f := file(id, version)
	f.Manifest, _ = json.Marshal(map[string]any{
		"id": id, "version": version,
		"settings": []map[string]any{
			{"key": "units", "label": "Units", "kind": "enum", "options": []string{"celsius", "kelvin"}, "default": "celsius"},
			{"key": "days", "label": "Days ahead", "kind": "int", "default": 3},
			{"key": "every", "label": "Every", "kind": "duration", "default": "1h"},
			{"key": "api_key", "label": "API key", "kind": "string", "secret": true},
		},
	})

	return f
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	store := &settingsStore{stored: map[string]map[string]ports.StoredPluginSetting{}}

	f.s = plugins.New(f.repo, f.files, f.runtime, f.workflows, logging.Noop(), plugins.WithSettings(store, box{}))

	// Its version's settings, once approved.
	f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.0"}] = withSettings("weather", "1.0.0")
	_, err := f.s.Adopt(ctx, admin, "weather", "1.0.0")
	require.NoError(t, err)

	got, err := f.s.Settings(ctx, admin, "weather")
	require.NoError(t, err)
	assert.Empty(t, got, "none before an approval: what runs declares them")

	require.NoError(t, f.s.Approve(ctx, admin, "weather", "1.0.0", nil))
	require.NoError(t, f.s.Load(ctx))

	t.Run("their defaults, for each run", func(t *testing.T) {
		got, err := f.s.Settings(ctx, admin, "weather")
		require.NoError(t, err)
		require.Len(t, got, 4)
		assert.Equal(t, "celsius", got[0].Value)
		assert.False(t, got[0].IsSet)

		assert.Equal(t, map[string]any{"units": "celsius", "days": float64(3), "every": "1h", "api_key": ""}, f.s.RunSettings("weather"))
	})

	t.Run("saved, a secret sealed, for the next run", func(t *testing.T) {
		require.NoError(t, f.s.SetSettings(ctx, admin, "weather", map[string]json.RawMessage{
			"units": json.RawMessage(`"kelvin"`), "every": json.RawMessage(`"30m"`), "api_key": json.RawMessage(`"k-123"`),
		}))

		assert.Equal(t, map[string]any{"units": "kelvin", "days": float64(3), "every": "30m", "api_key": "k-123"}, f.s.RunSettings("weather"))
		assert.Equal(t, "plugin-setting:weather:api_key|k-123", string(store.stored["weather"]["api_key"].Sealed), "bound to its plugin and key")
		assert.Nil(t, store.stored["weather"]["api_key"].Value)

		got, _ := f.s.Settings(ctx, admin, "weather")
		assert.Nil(t, got[3].Value, "a secret is never shown again")
		assert.True(t, got[3].IsSet)

		assert.Equal(t, `api_key changed; every: "1h" → "30m"; units: "celsius" → "kelvin"`, store.audits[0].Detail, "audited, the secret's value not")
	})

	t.Run("reset to the default", func(t *testing.T) {
		require.NoError(t, f.s.SetSettings(ctx, admin, "weather", map[string]json.RawMessage{"units": json.RawMessage(`null`), "api_key": json.RawMessage(`""`)}))

		assert.Equal(t, "celsius", f.s.RunSettings("weather")["units"])
		assert.Equal(t, "", f.s.RunSettings("weather")["api_key"])
	})

	t.Run("refused whole when one is wrong", func(t *testing.T) {
		err := f.s.SetSettings(ctx, admin, "weather", map[string]json.RawMessage{
			"units": json.RawMessage(`"fahrenheit"`), "days": json.RawMessage(`"ten"`), "colour": json.RawMessage(`"red"`), "every": json.RawMessage(`"2h"`),
		})

		var v *domain.ValidationError
		require.ErrorAs(t, err, &v)
		require.Len(t, v.Problems, 3)
		assert.Equal(t, "colour", v.Problems[0].Param)
		assert.Equal(t, "30m", f.s.RunSettings("weather")["every"], "nothing saved, every's either")

		require.ErrorIs(t, f.s.SetSettings(ctx, alice, "weather", nil), domain.ErrForbidden)
	})

	t.Run("a secret an upgrade declares not secret: unset, never opened (P18)", func(t *testing.T) {
		require.NoError(t, f.s.SetSettings(ctx, admin, "weather", map[string]json.RawMessage{"api_key": json.RawMessage(`"sk-live-123"`)}))

		upgrade := withSettings("weather", "1.0.1")
		upgrade.Manifest = []byte(strings.Replace(string(upgrade.Manifest), `"secret":true`, `"secret":false`, 1))
		f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.1"}] = upgrade

		_, err := f.s.Adopt(ctx, admin, "weather", "1.0.1")
		require.NoError(t, err)
		require.NoError(t, f.s.Approve(ctx, admin, "weather", "1.0.1", nil))

		got, err := f.s.Settings(ctx, admin, "weather")
		require.NoError(t, err)
		assert.Equal(t, "api_key", got[3].Key)
		assert.False(t, got[3].Secret)
		assert.Equal(t, "", got[3].Value, "its default")
		assert.False(t, got[3].IsSet)
		assert.Equal(t, "", f.s.RunSettings("weather")["api_key"])

		require.NoError(t, f.s.SetSettings(ctx, admin, "weather", map[string]json.RawMessage{"api_key": json.RawMessage(`"x"`)}))
		assert.Equal(t, `api_key: "" → "x"`, store.audits[len(store.audits)-1].Detail, "never the secret in clear")
	})
}
