package output_test

import (
	"context"
	"testing"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	loggingmocks "github.com/lein3000zzz/polythought-go/internal/mocks/logging"
)

func TestLog(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string]any
		items       []engine.Item
		wantMessage string
	}{
		{
			name:        "default message",
			items:       []engine.Item{{JSON: map[string]any{"a": 1}}},
			wantMessage: "Workflow item",
		},
		{
			name:        "custom message, every item",
			params:      map[string]any{"message": "Tight spread"},
			items:       []engine.Item{{JSON: map[string]any{"a": 1}}, {JSON: map[string]any{"a": 2}}},
			wantMessage: "Tight spread",
		},
		{
			name: "no items, no logs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := loggingmocks.NewMockLogger(t)
			scoped := loggingmocks.NewMockLogger(t)
			base.EXPECT().With([]any{"component", "node.log"}).Return(scoped)

			for _, it := range tt.items {
				scoped.EXPECT().Info(mock.Anything, tt.wantMessage, []any{
					"workflow_id", domain.WorkflowID("wf"),
					"execution_id", domain.ExecutionID("ex"),
					"node_id", domain.NodeID("log"),
					"item", it.JSON,
				}).Once()
			}

			node := output.NewLog(base)
			rc := engine.RunContext{WorkflowID: "wf", ExecutionID: "ex", NodeID: "log", Params: engine.LiteralParams(node.Type(), tt.params)}

			out, err := node.Run(context.Background(), rc, engine.Ports{engine.DefaultPort: tt.items})
			require.NoError(t, err)

			got := out[engine.DefaultPort]
			require.Len(t, got, len(tt.items))

			for i, it := range tt.items {
				assert.Equal(t, it.JSON, got[i].JSON, "items pass through unchanged")
			}
		})
	}
}
