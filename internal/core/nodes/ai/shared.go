package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// SharedTool is one of the agent's read-only tools, as others offer it too (D402): the
// MCP server's market tools are these, so they read and answer the same.
type SharedTool struct {
	Name        string
	Description string
	Schema      json.RawMessage // closed, every field required
}

// SharedTools are the tools, in the order the node offers them.
func SharedTools() []SharedTool {
	out := make([]SharedTool, len(ToolNames))
	for i, name := range ToolNames {
		t := tools[name].llm
		out[i] = SharedTool{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}

	return out
}

// RunShared runs a tool by the name it is offered as, with input, giving at most results
// results.
func RunShared(ctx context.Context, s Sources, name string, input json.RawMessage, results int, now time.Time) (any, error) {
	for _, key := range ToolNames {
		if def := tools[key]; def.llm.Name == name {
			out, _, err := def.run(ctx, s, input, results, now)
			return out, err
		}
	}

	return nil, fmt.Errorf("no tool is named %s", name)
}
