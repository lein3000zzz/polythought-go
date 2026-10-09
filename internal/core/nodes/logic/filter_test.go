package logic_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

func runFilter(t *testing.T, params map[string]any, items ...map[string]any) (engine.Ports, error) {
	t.Helper()

	node := logic.Filter{}
	in := engine.Ports{}

	for _, j := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: j})
	}

	return node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}, in)
}

func TestFilter(t *testing.T) {
	markets := []map[string]any{
		{"q": "Rain?", "spread": decimal.RequireFromString("0.01"), "outcomes": []any{map[string]any{"price": "0.7"}}},
		{"q": "Snow?", "spread": "0.05", "outcomes": []any{map[string]any{"price": "0.2"}}},
		{"q": "Hail?", "spread": nil},
	}

	tests := []struct {
		name     string
		params   map[string]any
		items    []map[string]any
		wantKept []string
		wantErr  error
		wantFail int
	}{
		{name: "number below a value", params: map[string]any{"field": "spread", "operator": "lt", "value": "0.02"}, items: markets, wantKept: []string{"Rain?"}},
		{name: "default test is greater than", params: map[string]any{"field": "spread", "value": "0.02"}, items: markets, wantKept: []string{"Snow?"}},
		{name: "a path into a list", params: map[string]any{"field": "outcomes.0.price", "operator": "gte", "value": "0.5"}, items: markets, wantKept: []string{"Rain?"}},
		{name: "text contains, any case", params: map[string]any{"field": "q", "operator": "contains", "value": "SNOW"}, items: markets, wantKept: []string{"Snow?"}},
		{name: "text does not contain", params: map[string]any{"field": "q", "operator": "notContains", "value": "now"}, items: markets, wantKept: []string{"Rain?", "Hail?"}},
		{name: "field exists", params: map[string]any{"field": "spread", "operator": "exists"}, items: markets, wantKept: []string{"Rain?", "Snow?"}},
		{name: "field missing", params: map[string]any{"field": "spread", "operator": "missing"}, items: markets, wantKept: []string{"Hail?"}},
		{name: "a value that is not a number fails the item", params: map[string]any{"field": "q", "operator": "gt", "value": "1"}, items: markets[:1], wantFail: 1},
		{name: "comparing with text fails the node", params: map[string]any{"field": "spread", "operator": "lt", "value": "lots"}, items: markets, wantErr: engine.ErrInvalidParam},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := runFilter(t, tt.params, tt.items...)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			var kept []string
			for _, it := range out[engine.DefaultPort] {
				kept = append(kept, it.JSON["q"].(string))
			}

			assert.Equal(t, tt.wantKept, kept)
			assert.Len(t, out[engine.ErrorPort], tt.wantFail)
		})
	}
}
