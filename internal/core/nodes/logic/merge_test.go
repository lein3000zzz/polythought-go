package logic_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

// A: markets t1 to t3, and one without a token. B: holdings of t2 and t3, t3 twice, and
// one of t9; B names the token asset.
var (
	sideA = []map[string]any{
		{"id": "a1", "tokenId": "t1"}, {"id": "a2", "tokenId": "t2"}, {"id": "a3", "tokenId": "t3"}, {"id": "a4"},
	}
	sideB = []map[string]any{
		{"id": "b1", "asset": "t2", "shares": "5"}, {"id": "b2", "asset": "t3", "shares": "7"},
		{"id": "b3", "asset": "t3", "shares": "1"}, {"id": "b4", "asset": "t9"},
	}
)

func runMerge(t *testing.T, params map[string]any) (engine.Ports, error) {
	t.Helper()

	node := logic.Merge{}
	in := engine.Ports{}

	for _, j := range sideA {
		in.Add(logic.PortA, engine.Item{JSON: j})
	}

	for _, j := range sideB {
		in.Add(logic.PortB, engine.Item{JSON: j})
	}

	p := map[string]any{"field": "tokenId", "fieldB": "asset"}
	for k, v := range params {
		p[k] = v
	}

	return node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), p)}, in)
}

func TestMerge(t *testing.T) {
	for _, tt := range []struct {
		mode string
		want []string
	}{
		{mode: "", want: []string{"a2", "a3"}}, // both, by default
		{mode: engine.MergeBoth, want: []string{"a2", "a3"}},
		{mode: engine.MergeOnlyA, want: []string{"a1", "a4"}},
		{mode: engine.MergeOnlyOne, want: []string{"a1", "a4", "b4"}},
		{mode: engine.MergeEither, want: []string{"a1", "a2", "a3", "a4", "b4"}},
	} {
		t.Run("keep "+tt.mode, func(t *testing.T) {
			params := map[string]any{}
			if tt.mode != "" {
				params["mode"] = tt.mode
			}

			out, err := runMerge(t, params)
			require.NoError(t, err)
			assert.Equal(t, tt.want, kept(out, engine.DefaultPort))
		})
	}

	t.Run("join: A's matches, the first B item of the key under b (D134 to D136)", func(t *testing.T) {
		out, err := runMerge(t, map[string]any{"mode": engine.MergeJoin})
		require.NoError(t, err)

		require.Len(t, out[engine.DefaultPort], 2)
		joined := out[engine.DefaultPort][1]
		assert.Equal(t, map[string]any{"id": "a3", "tokenId": "t3", "b": sideB[1]}, joined.JSON)
		assert.Equal(t, []engine.ItemRef{{Port: logic.PortA, Index: 2}, {Port: logic.PortB, Index: 1}}, joined.Source)
		assert.NotContains(t, sideA[2], "b", "A's item is left as it was")

		out, err = runMerge(t, map[string]any{"mode": engine.MergeJoin, "joinedAs": "holding"})
		require.NoError(t, err)
		assert.Contains(t, out[engine.DefaultPort][0].JSON, "holding")
	})

	t.Run("B's field is A's when empty", func(t *testing.T) {
		out, err := runMerge(t, map[string]any{"fieldB": "", "field": "id"})
		require.NoError(t, err)
		assert.Empty(t, out[engine.DefaultPort], "no B item has an id of A's")
	})

	t.Run("an unknown way fails the node", func(t *testing.T) {
		_, err := runMerge(t, map[string]any{"mode": "most"})
		require.ErrorIs(t, err, engine.ErrInvalidParam)
	})
}
