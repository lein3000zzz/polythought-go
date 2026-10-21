package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// PluginStore keeps what plugin nodes store (D288): JSON values by key, for each node of a
// workflow and the plugin it is of. A node's store counts as its memory and goes as it
// does: NodeMemory's Count, Forget and ForgetOthers take it in.
type PluginStore interface {
	// Get is the value under key, and whether there is one.
	Get(ctx context.Context, n PluginNode, key string) (json.RawMessage, bool, error)

	// Set keeps value under key at now, in place of what was there. It is
	// domain.ErrStoreFull when the node's keys and values would take more than max bytes;
	// two at once count each other's.
	Set(ctx context.Context, n PluginNode, key string, value json.RawMessage, max int, now time.Time) error

	// Delete removes key, if it is there.
	Delete(ctx context.Context, n PluginNode, key string) error

	// Keys are the node's keys starting with prefix, in order, after after, at most limit.
	Keys(ctx context.Context, n PluginNode, prefix, after string, limit int) ([]string, error)
}

// PluginNode is a workflow's node, as a plugin's.
type PluginNode struct {
	Workflow domain.WorkflowID
	Node     domain.NodeID
	Plugin   string
}
