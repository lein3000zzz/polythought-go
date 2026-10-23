package nodes_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
)

type pluginNode struct{ trades bool }

func (n pluginNode) Type() engine.NodeType {
	name := "plugin.watch"
	if n.trades {
		name = "plugin.buy"
	}

	return engine.NodeType{Name: name, Label: name, Trades: n.trades}
}

func (pluginNode) Run(context.Context, engine.RunContext, engine.Ports) (engine.Ports, error) {
	return nil, nil
}

// A workflow places orders with a trading node, or a plugin's that says it trades (D399).
func TestPlacesOrders(t *testing.T) {
	r := engine.NewRegistry()
	require.NoError(t, r.RegisterNode(pluginNode{trades: true}))
	require.NoError(t, r.RegisterNode(pluginNode{}))

	with := func(types ...string) domain.Workflow {
		var w domain.Workflow
		for i, typ := range types {
			w.Nodes = append(w.Nodes, domain.NodeSpec{ID: domain.NodeID(rune('a' + i)), Type: typ})
		}

		return w
	}

	for types, want := range map[string]domain.Workflow{
		"place an order":    with("trigger.manual", "trading.placeOrder"),
		"place several":     with("trading.placeOrders"),
		"cancel them":       with("trading.cancelOrders"),
		"a plugin's trades": with("plugin.buy"),
	} {
		assert.True(t, nodes.PlacesOrders(r, want), types)
	}

	for types, w := range map[string]domain.Workflow{
		"watching":                with("trigger.manual", "trading.riskGuard", "output.log"),
		"a plugin's that watches": with("plugin.watch"),
		"an unknown type":         with("custom.thing"),
	} {
		assert.False(t, nodes.PlacesOrders(r, w), types)
	}
}
