package domain

import "context"

type NodeID string

// Item is one record flowing between nodes, n8n style.
type Item map[string]any

// DefaultPort is the port used by nodes with a single input or output.
const DefaultPort = "main"

// Ports maps a port name to its items. Branching nodes (if, switch) emit on several
// output ports; merge-style nodes read several input ports.
type Ports map[string][]Item

// Node is the runtime form of a workflow step.
type Node interface {
	ID() NodeID
	Run(ctx context.Context, input Ports) (Ports, error)
}
