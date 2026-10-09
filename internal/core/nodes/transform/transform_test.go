package transform_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/transform"
)

func run(t *testing.T, node engine.Node, params map[string]any, items ...map[string]any) (engine.Ports, error) {
	t.Helper()

	in := engine.Ports{}
	for _, j := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: j})
	}

	return node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}, in)
}

func names(out engine.Ports) []string {
	var got []string
	for _, it := range out[engine.DefaultPort] {
		got = append(got, it.JSON["name"].(string))
	}

	return got
}

func TestTop(t *testing.T) {
	items := []map[string]any{
		{"name": "a", "liquidity": "500"},
		{"name": "b", "liquidity": decimal.RequireFromString("2000")},
		{"name": "c"},
		{"name": "d", "liquidity": 1000},
		{"name": "e", "liquidity": "n/a"},
	}

	tests := []struct {
		name    string
		params  map[string]any
		want    []string
		wantErr error
	}{
		{name: "largest first by default", params: map[string]any{"field": "liquidity", "limit": 2}, want: []string{"b", "d"}},
		{name: "smallest first", params: map[string]any{"field": "liquidity", "order": "asc", "limit": 2}, want: []string{"a", "d"}},
		{name: "items without the number go last", params: map[string]any{"field": "liquidity"}, want: []string{"b", "d", "a", "c", "e"}},
		{name: "keep at least one", params: map[string]any{"field": "liquidity", "limit": 0}, wantErr: engine.ErrInvalidParam},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, transform.Top{}, tt.params, items...)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, names(out))
		})
	}

	t.Run("each kept item points at the one it came from", func(t *testing.T) {
		out, err := run(t, transform.Top{}, map[string]any{"field": "liquidity", "limit": 1}, items...)
		require.NoError(t, err)
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, out[engine.DefaultPort][0].Source)
	})
}

func TestPick(t *testing.T) {
	out, err := run(t, transform.Pick{}, map[string]any{"fields": []any{"question", "outcomes.0.price", "missing"}},
		map[string]any{
			"question": "Rain?",
			"spread":   "0.01",
			"outcomes": []any{map[string]any{"price": "0.7"}},
		},
	)
	require.NoError(t, err)

	require.Len(t, out[engine.DefaultPort], 1)
	assert.Equal(t, map[string]any{"question": "Rain?", "outcomes.0.price": "0.7"}, out[engine.DefaultPort][0].JSON)
}
