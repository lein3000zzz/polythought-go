//go:build integration

// An end-to-end run of the built-in nodes against live Polymarket.
// Run with `make test-integration`.

package nodes_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pmadapter "github.com/lein3000zzz/polythought-go/internal/adapters/polymarket"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func TestLiveSpreadWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	util := observability.NoopUtility()

	gamma, err := pm.NewGammaClient(util, pm.HTTPConfig{})
	require.NoError(t, err)

	clob, err := pm.NewClobClient(util, pm.HTTPConfig{})
	require.NoError(t, err)

	data, err := pm.NewDataClient(util, pm.HTTPConfig{})
	require.NoError(t, err)

	reader := pmadapter.NewReader(gamma, clob, data)

	registry := engine.NewRegistry()
	require.NoError(t, nodes.Register(registry, nodes.Deps{Markets: reader, OrderBooks: reader, Logger: util.Logger}))

	executor, err := engine.NewExecutor(util, registry)
	require.NoError(t, err)

	main := engine.DefaultPort
	wf := domain.Workflow{
		ID: "live",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: triggers.ManualType},
			{ID: "markets", Type: "polymarket.markets", Params: map[string]any{"limit": 3}},
			{ID: "books", Type: "polymarket.orderbook"},
			{ID: "tight", Type: logic.IfType, Params: map[string]any{"field": "spread", "operator": "lte", "value": "0.05"}},
			{ID: "logTight", Type: "output.log"},
			{ID: "logWide", Type: "output.log"},
		},
		Connections: []domain.Connection{
			{From: "manual", FromPort: main, To: "markets", ToPort: main},
			{From: "markets", FromPort: main, To: "books", ToPort: main},
			{From: "books", FromPort: main, To: "tight", ToPort: main},
			{From: "tight", FromPort: logic.PortTrue, To: "logTight", ToPort: main},
			{From: "tight", FromPort: logic.PortFalse, To: "logWide", ToPort: main},
		},
	}

	res, err := executor.Execute(ctx, wf, "manual", triggers.ManualOutput())
	require.NoError(t, err)

	exec := res.Execution
	require.Equal(t, domain.ExecutionSucceeded, exec.Status, exec.Error)

	markets := res.Outputs["markets"][main]
	books := res.Outputs["books"][main]
	routed := res.Outputs["tight"][logic.PortTrue]
	routed = append(routed, res.Outputs["tight"][logic.PortFalse]...)

	assert.Len(t, markets, 3)
	assert.GreaterOrEqual(t, len(books), len(markets), "at least one book per market")
	assert.Len(t, routed, len(books), "every book is routed one way or the other")

	for _, b := range books {
		assert.NotEmpty(t, b.JSON["tokenId"])
		assert.NotEmpty(t, b.JSON["question"])
	}
}
