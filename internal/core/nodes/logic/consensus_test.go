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

func TestConsensus(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	mem := newMemory()
	node := logic.NewConsensus(mem).WithClock(func() time.Time { return now })

	run := func(params map[string]any, items ...map[string]any) engine.Ports {
		t.Helper()

		in := engine.Ports{}
		for _, j := range items {
			in.Add(engine.DefaultPort, engine.Item{JSON: j})
		}

		out, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "agree", Params: engine.LiteralParams(node.Type(), params),
		}, in)
		require.NoError(t, err)

		return out
	}
	trade := func(id, who, token, side string) map[string]any {
		return map[string]any{"id": id, "tokenId": token, "side": side, "trader": map[string]any{"address": who}}
	}

	// By default 2 traders, the same outcome and side, within 6 hours.
	out := run(nil, trade("1", "alice", "t-yes", "buy"), trade("2", "alice", "t-yes", "buy"), trade("3", "bob", "t-yes", "sell"))
	assert.Equal(t, []string{"1", "2", "3"}, kept(out, logic.PortAlone), "one trader twice, and a sell, are no agreement")

	now = now.Add(time.Hour)
	out = run(nil, trade("4", "bob", "t-yes", "buy"), trade("5", "carol", "t-yes", "buy"))
	assert.Equal(t, []string{"4", "5"}, kept(out, engine.DefaultPort), "bob makes two, and carol after him goes on too (D144)")

	now = now.Add(5 * time.Hour)
	out = run(nil, trade("6", "dave", "t-yes", "buy"))
	assert.Equal(t, []string{"6"}, kept(out, engine.DefaultPort), "bob and carol 5 hours before still count")

	now = now.Add(7 * time.Hour)
	out = run(nil, trade("7", "erin", "t-yes", "buy"))
	assert.Equal(t, []string{"7"}, kept(out, logic.PortAlone), "too long since the others")

	t.Run("other fields, and more traders", func(t *testing.T) {
		params := map[string]any{"needed": 3, "within": "1h", "on": []any{"tokenId"}, "who": "trader.address"}
		out := run(params, trade("8", "a", "t-no", "buy"), trade("9", "b", "t-no", "sell"), trade("10", "c", "t-no", "buy"))
		assert.Equal(t, []string{"10"}, kept(out, engine.DefaultPort), "on the outcome alone, buys and sells together")
	})

	t.Run("an item without the fields fails; bad settings fail the node", func(t *testing.T) {
		out := run(nil, map[string]any{"id": "x", "tokenId": "t"})
		assert.Len(t, out[engine.ErrorPort], 1)

		_, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "agree", Params: engine.LiteralParams(node.Type(), map[string]any{"needed": 0}),
		}, engine.Ports{engine.DefaultPort: {{JSON: trade("1", "a", "t", "buy")}}})
		require.ErrorIs(t, err, engine.ErrInvalidParam)
	})

	t.Run("memory that fails fails the node", func(t *testing.T) {
		mem.err = errors.New("database down")

		_, err := node.Run(context.Background(), engine.RunContext{
			WorkflowID: "wf-1", NodeID: "agree", Params: engine.LiteralParams(node.Type(), nil),
		}, engine.Ports{engine.DefaultPort: {{JSON: trade("1", "a", "t", "buy")}}})
		require.ErrorContains(t, err, "count who agrees: database down")
	})
}
