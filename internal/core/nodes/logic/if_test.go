package logic_test

import (
	"context"
	"testing"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func TestIf(t *testing.T) {
	tests := []struct {
		name     string
		operator string // empty uses the default (gt)
		value    any
		field    any // nil leaves the field out of the item
		want     string
		wantErr  error
	}{
		{name: "default gt true", value: "0.5", field: "0.6", want: logic.PortTrue},
		{name: "default gt false on equal", value: "0.5", field: "0.5", want: logic.PortFalse},
		{name: "gte on equal", operator: "gte", value: 0.5, field: 0.5, want: logic.PortTrue},
		{name: "lt", operator: "lt", value: "0.02", field: decimal.RequireFromString("0.01"), want: logic.PortTrue},
		{name: "lte false", operator: "lte", value: "0.02", field: "0.03", want: logic.PortFalse},
		{name: "eq compares exactly", operator: "eq", value: "0.1", field: 0.1, want: logic.PortTrue},
		{name: "neq", operator: "neq", value: 1, field: 2, want: logic.PortTrue},
		{name: "missing field is false", value: "1", want: logic.PortFalse},
		{name: "non-numeric field fails the item", value: "1", field: "abc", want: engine.ErrorPort},
		{name: "unknown operator fails the node", operator: "between", value: "1", field: "1", wantErr: engine.ErrInvalidParam},
		{name: "non-numeric value fails the node", value: "lots", field: "1", wantErr: engine.ErrInvalidParam},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{"field": "v", "value": tt.value}
			if tt.operator != "" {
				params["operator"] = tt.operator
			}

			item := engine.Item{JSON: map[string]any{"other": 1}}
			if tt.field != nil {
				item.JSON["v"] = tt.field
			}

			node := logic.If{}
			rc := engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}

			out, err := node.Run(context.Background(), rc, engine.Ports{engine.DefaultPort: {item}})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			require.Len(t, out[tt.want], 1, "routed to %q, got %v", tt.want, out.Counts())
			assert.Equal(t, 1, out.Len())
			assert.Equal(t, item.JSON, out[tt.want][0].JSON)
			assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 0}}, out[tt.want][0].Source)
		})
	}
}
