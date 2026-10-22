package pluginhost_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// echoFile is the test plugin as the plugins directory would give it, its manifest.json
// what its program describes, changed by change.
func echoFile(t *testing.T, change func(m map[string]any)) ports.PluginFile {
	t.Helper()

	prog := program(t, echo)

	p, err := pluginhost.Start(context.Background(), prog, sandboxed(pluginhost.Deps{}))
	require.NoError(t, err)

	m := p.Manifest()
	p.Close()

	// manifest.json names the SDK, as Describe does: the host refuses another major's
	// before it runs anything.
	raw, err := json.Marshal(m)
	require.NoError(t, err)

	if change != nil {
		var asMap map[string]any
		require.NoError(t, json.Unmarshal(raw, &asMap))
		change(asMap)

		raw, err = json.Marshal(asMap)
		require.NoError(t, err)
	}

	return ports.PluginFile{ID: m.ID, Version: m.Version, Manifest: raw, Program: prog.Path, Dir: prog.Dir, Data: prog.Data}
}

func registry(t *testing.T) *engine.Registry {
	t.Helper()

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterCredentialType(credtypes.HTTPHeaderType()))
	require.NoError(t, r.RegisterTrigger(triggers.Manual{}))

	return r
}

func TestRuntime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	r := registry(t)
	rt := pluginhost.NewRuntime(ctx, sandboxed(pluginhost.Deps{}), r)
	t.Cleanup(rt.Close)

	t.Run("loaded: registered, its scenarios kept, supervised", func(t *testing.T) {
		require.NoError(t, rt.Load(ctx, echoFile(t, nil)))

		_, _, ok := r.Type("plugin.echo.shout")
		assert.True(t, ok)
		assert.Len(t, rt.Scenarios(), 1)

		st, ok := rt.Status("echo")
		require.True(t, ok)
		assert.Equal(t, "running", st.State)
		assert.Equal(t, "1.2.0", st.Version)

		assert.Error(t, rt.Start("echo"), "only a crashed one starts again")
		assert.Error(t, rt.Load(ctx, echoFile(t, nil)), "once")
	})

	t.Run("refused when its program says otherwise than its manifest.json", func(t *testing.T) {
		other := pluginhost.NewRuntime(ctx, sandboxed(pluginhost.Deps{}), registry(t))
		t.Cleanup(other.Close)

		err := other.Load(ctx, echoFile(t, func(m map[string]any) { m["permissions"] = []string{"fetch:*"} }))
		require.EqualError(t, err, "its program describes itself otherwise than its manifest.json")

		_, ok := other.Status("echo")
		assert.False(t, ok, "nothing of it kept")
	})

	_, ok := rt.Status("none")
	assert.False(t, ok)
}
