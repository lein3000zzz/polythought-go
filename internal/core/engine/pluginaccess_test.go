package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// pluginGate allows the plugins it is told, and remembers what it was asked.
type pluginGate struct {
	allowed map[string]bool
	err     error
	asked   []string
}

func (g *pluginGate) PluginAllowed(_ context.Context, owner domain.UserID, plugin string) (bool, error) {
	g.asked = append(g.asked, string(owner)+":"+plugin)
	return g.allowed[plugin], g.err
}

// pluginRegistry has a node of each of two plugins, counting their runs.
func pluginRegistry(t *testing.T, ran *int) *engine.Registry {
	t.Helper()

	r := testRegistry(t)

	for _, plugin := range []string{"weather", "news"} {
		require.NoError(t, r.RegisterNode(fakeNode{
			typ: engine.NodeType{Name: "plugin." + plugin + ".x", Inputs: mainPorts(), Outputs: mainPorts(), Plugin: plugin},
			run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
				*ran++
				return in, nil
			},
		}))
	}

	return r
}

func TestCheckPluginAccess(t *testing.T) {
	var ran int

	r := pluginRegistry(t, &ran)
	w := withCode(spec("w1", "plugin.weather.x", nil), spec("a", "test.pass", nil), spec("n1", "plugin.news.x", nil), spec("w2", "plugin.weather.x", nil))

	assert.Equal(t, map[string][]domain.NodeID{"weather": {"w1", "w2"}, "news": {"n1"}}, r.PluginNodes(w))

	g := &pluginGate{allowed: map[string]bool{"news": true}}
	err := r.CheckPluginAccess(t.Context(), w, g)

	var v *domain.ValidationError
	require.ErrorAs(t, err, &v)
	assert.ErrorIs(t, err, domain.ErrPluginNotAllowed)
	require.Len(t, v.Problems, 2, "each node of the plugin refused")
	assert.Equal(t, `node "w1": its owner may not use the plugin weather`, v.Problems[0].Message)
	assert.ElementsMatch(t, []string{"alice:weather", "alice:news"}, g.asked, "once a plugin")

	assert.NoError(t, r.CheckPluginAccess(t.Context(), withCode(spec("a", "test.pass", nil)), &pluginGate{}), "no plugin, nothing asked")

	failing := errors.New("db down")
	err = r.CheckPluginAccess(t.Context(), w, &pluginGate{err: failing})
	assert.ErrorIs(t, err, failing)
	assert.NotErrorAs(t, err, &v)
}

func TestExecuteChecksPluginAccess(t *testing.T) {
	var ran int

	g := &pluginGate{allowed: map[string]bool{"news": true}}

	ex, err := engine.NewExecutor(observability.NoopUtility(), pluginRegistry(t, &ran),
		engine.WithClock(tickingClock()), engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
		engine.WithPluginGate(g))
	require.NoError(t, err)

	res, err := ex.Execute(t.Context(), withCode(spec("n1", "plugin.news.x", nil), spec("w1", "plugin.weather.x", nil)), "trigger", fired(item("n", 1)))
	require.NoError(t, err)

	assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
	assert.Equal(t, "its owner may not use the plugin weather, so the workflow does not run", res.Execution.Error)
	assert.Zero(t, ran, "no node run, the allowed one's neither")

	g.allowed["weather"] = true
	res, err = ex.Execute(t.Context(), withCode(spec("n1", "plugin.news.x", nil), spec("w1", "plugin.weather.x", nil)), "trigger", fired(item("n", 1)))
	require.NoError(t, err)
	assert.Equal(t, domain.ExecutionSucceeded, res.Execution.Status)
	assert.Equal(t, 2, ran)
}
