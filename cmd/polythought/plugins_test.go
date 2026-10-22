package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// onlyWeather lets everyone use the plugin weather, and nobody news.
type onlyWeather struct{ asked int }

func (g *onlyWeather) PluginAllowed(_ context.Context, _ domain.UserID, plugin string) (bool, error) {
	g.asked++
	return plugin == "weather", nil
}

type pluginNode struct{ typ engine.NodeType }

func (n pluginNode) Type() engine.NodeType { return n.typ }

func (pluginNode) Run(context.Context, engine.RunContext, engine.Ports) (engine.Ports, error) {
	return engine.Ports{}, nil
}

// The node list a user gets leaves out the plugins they may not use (D330).
func TestNodeTypesOfPlugins(t *testing.T) {
	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(triggers.Manual{}))

	for _, name := range []string{"plugin.weather.forecast", "plugin.weather.rain", "plugin.news.headlines"} {
		plugin := "weather"
		if name == "plugin.news.headlines" {
			plugin = "news"
		}

		require.NoError(t, r.RegisterNode(pluginNode{typ: engine.NodeType{
			Name: name, Inputs: []string{engine.DefaultPort}, Outputs: []string{engine.DefaultPort}, Plugin: plugin,
		}}))
	}

	gate := &onlyWeather{}

	types, err := nodeTypes{types: r, plugins: gate}.Types(context.Background(), auth.Principal{User: domain.User{ID: "alice"}})
	require.NoError(t, err)

	var names []string
	for _, ti := range types {
		names = append(names, ti.Name)
	}

	assert.ElementsMatch(t, []string{"trigger.manual", "plugin.weather.forecast", "plugin.weather.rain"}, names)
	assert.Equal(t, 2, gate.asked, "once a plugin")
}

// A plugin's caps are its own where its card sets them, and the Config page's elsewhere
// (D346).
func TestPluginCaps(t *testing.T) {
	r, err := config.Settings()
	require.NoError(t, err)

	s := r.Snapshot(map[string]any{config.KeyPluginDataCap: 512})

	assert.Equal(t, pluginhost.Caps{Data: 512 << 20, Memory: 256 << 20}, pluginCaps(s, domain.PluginCaps{}))
	assert.Equal(t, pluginhost.Caps{Data: 512 << 20, Memory: 1 << 30}, pluginCaps(s, domain.PluginCaps{MemoryMiB: 1024}))
}
