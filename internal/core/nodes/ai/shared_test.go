package ai

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The shared tools are the node's own, under the names the node offers them.
func TestSharedTools(t *testing.T) {
	shared := SharedTools()
	require.Len(t, shared, len(ToolNames))

	for i, key := range ToolNames {
		assert.Equal(t, tools[key].llm.Name, shared[i].Name)
		assert.JSONEq(t, string(tools[key].llm.Schema), string(shared[i].Schema))
	}

	_, err := RunShared(context.Background(), Sources{}, "no_such_tool", nil, 1, time.Now())
	require.EqualError(t, err, "no tool is named no_such_tool")
}
