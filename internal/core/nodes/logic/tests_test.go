package logic_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

// trades for the tests: Alice's big buy, Bob's small one, and a sell of Alice's.
var trades = []map[string]any{
	{"id": "1", "trader": map[string]any{"label": "Alice"}, "side": "buy", "dollars": "250"},
	{"id": "2", "trader": map[string]any{"label": "Bob"}, "side": "buy", "dollars": "20"},
	{"id": "3", "trader": map[string]any{"label": "Alice"}, "side": "sell", "dollars": "40"},
}

func kept(out engine.Ports, port string) []string {
	var ids []string
	for _, it := range out[port] {
		ids = append(ids, it.JSON["id"].(string))
	}

	return ids
}

func TestMoreTests(t *testing.T) {
	// Alice's (the first test), and buys (More tests).
	params := func(match string) map[string]any {
		p := map[string]any{
			"field": "trader.label", "operator": "contains", "value": "alice",
			"tests": []any{map[string]any{"field": "side", "operator": "contains", "value": "buy"}},
		}
		if match != "" {
			p["match"] = match
		}

		return p
	}

	for _, tt := range []struct {
		match string
		want  []string
	}{
		{match: "", want: []string{"1"}}, // all, by default
		{match: logic.MatchAll, want: []string{"1"}},
		{match: logic.MatchAny, want: []string{"1", "2", "3"}},
		{match: logic.MatchNone, want: nil},
		{match: logic.MatchOne, want: []string{"2", "3"}},
	} {
		t.Run("match "+tt.match, func(t *testing.T) {
			out, err := runFilter(t, params(tt.match), trades...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, kept(out, engine.DefaultPort))
		})
	}

	t.Run("a number compared in More tests, a number in the cell", func(t *testing.T) {
		p := params("")
		p["tests"] = []any{map[string]any{"field": "dollars", "operator": "gte", "value": 100}}

		out, err := runFilter(t, p, trades...)
		require.NoError(t, err)
		assert.Equal(t, []string{"1"}, kept(out, engine.DefaultPort))
	})

	t.Run("a field one cannot test fails the item, whatever the others say (D128)", func(t *testing.T) {
		p := params(logic.MatchAny)
		p["tests"] = []any{map[string]any{"field": "trader.label", "operator": "gt", "value": "1"}}

		out, err := runFilter(t, p, trades...)
		require.NoError(t, err)
		assert.Empty(t, out[engine.DefaultPort], "Alice passes the first test, but her label is no number")
		assert.Len(t, out[engine.ErrorPort], 3)
	})

	t.Run("a row comparing with text fails the node, saying which", func(t *testing.T) {
		p := params("")
		p["tests"] = []any{map[string]any{"field": "dollars", "operator": "lt", "value": "lots"}}

		_, err := runFilter(t, p, trades...)
		require.ErrorIs(t, err, engine.ErrInvalidParam)
		assert.ErrorContains(t, err, `"tests": in row 1: "lots" is not a number`)
	})

	t.Run("an unknown match fails the node", func(t *testing.T) {
		_, err := runFilter(t, params("most"), trades...)
		require.ErrorIs(t, err, engine.ErrInvalidParam)
	})
}

func TestIfHasFiltersTests(t *testing.T) {
	node := logic.If{}
	run := func(params map[string]any) engine.Ports {
		t.Helper()

		in := engine.Ports{}
		for _, j := range trades {
			in.Add(engine.DefaultPort, engine.Item{JSON: j})
		}

		out, err := node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}, in)
		require.NoError(t, err)

		return out
	}

	out := run(map[string]any{"field": "trader.label", "operator": "contains", "value": "Bob"})
	assert.Equal(t, []string{"2"}, kept(out, logic.PortTrue), "text, at a path (D127)")
	assert.Equal(t, []string{"1", "3"}, kept(out, logic.PortFalse))

	out = run(map[string]any{
		"field": "dollars", "value": 50.5, // a number, as saved before
		"tests": []any{map[string]any{"field": "side", "operator": "contains", "value": "sell"}},
		"match": logic.MatchOne,
	})
	assert.Equal(t, []string{"1", "3"}, kept(out, logic.PortTrue), "over $50.50, or a sell, not both")
	assert.Equal(t, []string{"2"}, kept(out, logic.PortFalse))
}

func TestTestSettingsTakeNumbers(t *testing.T) {
	var value, tests engine.ParamSpec

	for _, p := range (logic.If{}).Type().Params {
		switch p.Name {
		case "value":
			value = p
		case "tests":
			tests = p
		}
	}

	require.NoError(t, value.CheckValue(1000), "saved Ifs with a number still save")
	require.NoError(t, value.CheckValue("SNOW"))
	require.Error(t, value.CheckValue(true))
	require.NoError(t, tests.CheckValue([]any{map[string]any{"field": "dollars", "value": 100}}))
}
