package logic_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

func runSwitch(t *testing.T, rules []any, items ...map[string]any) (engine.Ports, error) {
	t.Helper()

	node := logic.Switch{}
	in := engine.Ports{}

	for _, j := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: j})
	}

	return node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), map[string]any{"rules": rules})}, in)
}

func rule(port, field, op string, value any) map[string]any {
	return map[string]any{"port": port, "field": field, "operator": op, "value": value}
}

func TestSwitch(t *testing.T) {
	// Price bands first, then Alice's, then the rest (D129, D130).
	rules := []any{
		rule("longshot", "price", "lt", "0.2"),
		rule("favourite", "price", "gte", 0.8),
		rule("alice", "trader.label", "is", "ALICE"),
	}
	trades := []map[string]any{
		{"id": "1", "price": "0.1", "trader": map[string]any{"label": "Alice"}}, // both: the first rule takes it
		{"id": "2", "price": "0.9", "trader": map[string]any{"label": "Bob"}},
		{"id": "3", "price": "0.5", "trader": map[string]any{"label": "Alice"}},
		{"id": "4", "price": "0.5", "trader": map[string]any{"label": "Alicia"}}, // is compares whole
		{"id": "5", "trader": map[string]any{"label": "Bob"}},                    // no price: no band
	}

	out, err := runSwitch(t, rules, trades...)
	require.NoError(t, err)

	assert.Equal(t, []string{"1"}, kept(out, "longshot"))
	assert.Equal(t, []string{"2"}, kept(out, "favourite"))
	assert.Equal(t, []string{"3"}, kept(out, "alice"))
	assert.Equal(t, []string{"4", "5"}, kept(out, logic.PortOther))
	assert.Equal(t, 5, out.Len(), "each item leaves once")

	t.Run("its ports are the rules'", func(t *testing.T) {
		assert.Equal(t, []string{"other", "longshot", "favourite", "alice"}, logic.Switch{}.Type().OutputsOf(map[string]any{"rules": rules}))
	})

	t.Run("a field a rule cannot test fails the item", func(t *testing.T) {
		out, err := runSwitch(t, []any{rule("big", "trader.label", "gt", "1"), rule("any", "id", "exists", nil)}, trades[0])
		require.NoError(t, err)
		assert.Len(t, out[engine.ErrorPort], 1, "no telling where it goes")
		assert.Empty(t, out["any"])
	})

	t.Run("a rule comparing with text fails the node, saying which", func(t *testing.T) {
		_, err := runSwitch(t, []any{rule("ok", "id", "exists", nil), rule("bad", "price", "lt", "cheap")}, trades[1])
		require.ErrorIs(t, err, engine.ErrInvalidParam)
		assert.ErrorContains(t, err, `"rules": in row 2: "cheap" is not a number`)
	})
}

func TestTextIs(t *testing.T) {
	out, err := runFilter(t, map[string]any{"field": "trader.label", "operator": "is", "value": "alice"}, trades...)
	require.NoError(t, err)
	assert.Equal(t, []string{"1", "3"}, kept(out, engine.DefaultPort), "whole, in any case (D131)")

	out, err = runFilter(t, map[string]any{"field": "trader.label", "operator": "isNot", "value": "Alice"}, trades...)
	require.NoError(t, err)
	assert.Equal(t, []string{"2"}, kept(out, engine.DefaultPort))
}
