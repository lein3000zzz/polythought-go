package logic_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

func runSample(t *testing.T, node domain.NodeID, params map[string]any, items ...map[string]any) (engine.Ports, error) {
	t.Helper()

	s := logic.Sample{}
	in := engine.Ports{}

	for _, j := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: j})
	}

	return s.Run(context.Background(), engine.RunContext{
		WorkflowID: "wf-1", NodeID: node, Params: engine.LiteralParams(s.Type(), params),
	}, in)
}

func TestSample(t *testing.T) {
	var trades []map[string]any
	for i := range 1000 {
		trades = append(trades, map[string]any{"id": fmt.Sprint(i), "txHash": fmt.Sprintf("0xtx%d", i)})
	}

	out, err := runSample(t, "sample", map[string]any{"chance": "30"}, trades...)
	require.NoError(t, err)

	taken := kept(out, engine.DefaultPort)
	assert.InDelta(t, 300, len(taken), 50, "about 3 in 10, by txHash")
	assert.Len(t, out[logic.PortRest], 1000-len(taken), "the rest leave from rest (D142)")

	again, err := runSample(t, "sample", map[string]any{"chance": "30"}, trades...)
	require.NoError(t, err)
	assert.Equal(t, taken, kept(again, engine.DefaultPort), "the same ones every time (D140)")

	other, err := runSample(t, "sample2", map[string]any{"chance": "30"}, trades...)
	require.NoError(t, err)
	assert.NotEqual(t, taken, kept(other, engine.DefaultPort), "another node takes others")

	t.Run("all, none, and items it cannot tell apart", func(t *testing.T) {
		out, err := runSample(t, "sample", map[string]any{"chance": 100}, trades[:3]...)
		require.NoError(t, err)
		assert.Len(t, out[engine.DefaultPort], 3)

		out, err = runSample(t, "sample", map[string]any{"chance": "0", "field": "id"}, trades[:3]...)
		require.NoError(t, err)
		assert.Len(t, out[logic.PortRest], 3)

		out, err = runSample(t, "sample", map[string]any{"chance": "50", "field": "ghost"}, trades[:1]...)
		require.NoError(t, err)
		assert.Len(t, out[engine.ErrorPort], 1)
	})

	t.Run("a share outside 0 to 100 fails the node", func(t *testing.T) {
		_, err := runSample(t, "sample", map[string]any{"chance": "130"}, trades[:1]...)
		require.ErrorIs(t, err, engine.ErrInvalidParam)
	})
}
