package logic_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

func TestDedupe(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	mem := newMemory()
	node := logic.NewDedupe(mem).WithClock(func() time.Time { return now })

	run := func(workflow domain.WorkflowID, params map[string]any, ids ...string) ([]string, int) {
		t.Helper()

		in := engine.Ports{}
		for _, id := range ids {
			in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{"tx": id}})
		}

		out, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: workflow, NodeID: "new",
			Params: engine.LiteralParams(node.Type(), params),
		}, in)
		require.NoError(t, err)

		var passed []string
		for _, it := range out[engine.DefaultPort] {
			passed = append(passed, it.JSON["tx"].(string))
		}

		return passed, len(out[engine.ErrorPort])
	}

	byTx := map[string]any{"field": "tx", "forget": "1h"}

	passed, _ := run("wf-1", byTx, "a", "b", "a")
	assert.Equal(t, []string{"a", "b"}, passed, "a repeat within one run is dropped too")

	now = now.Add(30 * time.Minute)
	passed, _ = run("wf-1", byTx, "a", "c")
	assert.Equal(t, []string{"c"}, passed, "seen half an hour ago")

	passed, _ = run("wf-2", byTx, "a")
	assert.Equal(t, []string{"a"}, passed, "each workflow remembers on its own")

	now = now.Add(31 * time.Minute)
	passed, _ = run("wf-1", byTx, "a")
	assert.Equal(t, []string{"a"}, passed, "new again once forgotten")

	passed, failed := run("wf-1", map[string]any{"field": "missing"}, "a")
	assert.Empty(t, passed)
	assert.Equal(t, 1, failed, "an item without the field cannot be told apart")

	n, err := mem.Count(context.Background(), "wf-1", "new", now)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "what it remembers is in node memory: a again, and c; b expired")

	t.Run("memory that fails fails the node", func(t *testing.T) {
		mem.err = errors.New("database down")

		_, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "new", Params: engine.LiteralParams(node.Type(), byTx),
		}, engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"tx": "z"}}}})
		require.ErrorContains(t, err, "remember the items: database down")
	})
}
