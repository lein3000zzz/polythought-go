package logic_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

func TestThrottle(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	mem := newMemory()
	node := logic.NewThrottle(mem).WithClock(func() time.Time { return now })

	run := func(params map[string]any, items ...map[string]any) engine.Ports {
		t.Helper()

		in := engine.Ports{}
		for _, j := range items {
			in.Add(engine.DefaultPort, engine.Item{JSON: j})
		}

		out, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "throttle", Params: engine.LiteralParams(node.Type(), params),
		}, in)
		require.NoError(t, err)

		return out
	}

	perTrader := map[string]any{"limit": 2, "per": "24h", "field": "trader.label"}

	// Alice three times and Bob once, in that order: Alice's third is over (D143).
	out := run(perTrader, trades[0], trades[2], trades[1], trades[0])
	assert.Equal(t, []string{"1", "3", "2"}, kept(out, engine.DefaultPort), "in the order they came")
	assert.Equal(t, []string{"1"}, kept(out, logic.PortLimited))

	now = now.Add(23 * time.Hour)
	out = run(perTrader, trades[0], trades[1])
	assert.Equal(t, []string{"2"}, kept(out, engine.DefaultPort), "Bob's second in a day")
	assert.Equal(t, []string{"1"}, kept(out, logic.PortLimited), "Alice's two count for 24 hours")

	now = now.Add(2 * time.Hour)
	out = run(perTrader, trades[0])
	assert.Equal(t, []string{"1"}, kept(out, engine.DefaultPort), "25 hours on, room again")

	t.Run("all together without a field", func(t *testing.T) {
		out := run(map[string]any{"limit": 1}, trades...)
		assert.Equal(t, []string{"1"}, kept(out, engine.DefaultPort))
		assert.Equal(t, []string{"2", "3"}, kept(out, logic.PortLimited))
	})

	t.Run("an item without the field fails; a limit under one fails the node", func(t *testing.T) {
		out := run(map[string]any{"limit": 1, "field": "ghost"}, trades[0])
		assert.Len(t, out[engine.ErrorPort], 1)

		_, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "throttle", Params: engine.LiteralParams(node.Type(), map[string]any{"limit": 0}),
		}, engine.Ports{engine.DefaultPort: {{JSON: trades[0]}}})
		require.ErrorIs(t, err, engine.ErrInvalidParam)
	})

	t.Run("memory that fails fails the node", func(t *testing.T) {
		mem.err = errors.New("database down")

		_, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "throttle", Params: engine.LiteralParams(node.Type(), perTrader),
		}, engine.Ports{engine.DefaultPort: {{JSON: trades[0]}}})
		require.ErrorContains(t, err, "count the items: database down")
	})
}
