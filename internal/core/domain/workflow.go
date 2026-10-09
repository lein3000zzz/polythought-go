package domain

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// ErrInvalidWorkflow is matched by every problem Validate reports.
var ErrInvalidWorkflow = errors.New("invalid workflow")

// ErrConflict means the workflow changed since the caller read it.
var ErrConflict = errors.New("changed by someone else, reload and retry")

// Workflow is the graph a user builds: nodes and the connections between their ports.
type Workflow struct {
	ID          WorkflowID
	OwnerID     UserID
	Name        string
	Active      bool
	Nodes       []NodeSpec
	Connections []Connection

	// Version increases with every saved change; updates name the version they edited,
	// so concurrent edits cannot silently overwrite each other.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NodeSpec is a node as configured in the editor. Type selects the implementation.
type NodeSpec struct {
	ID     NodeID
	Type   string // "polymarket.orderbook", "logic.if"
	Name   string
	Params map[string]any

	// ContinueOnFail routes items that failed to the node's error port instead of
	// failing the execution.
	ContinueOnFail bool
}

// Connection feeds the items one node emits on FromPort into another node's ToPort.
type Connection struct {
	From     NodeID
	FromPort string
	To       NodeID
	ToPort   string
}

func (w Workflow) Node(id NodeID) (NodeSpec, bool) {
	for _, n := range w.Nodes {
		if n.ID == id {
			return n, true
		}
	}

	return NodeSpec{}, false
}

// Outgoing returns the connections leaving a node, in declaration order.
func (w Workflow) Outgoing(id NodeID) []Connection {
	var out []Connection

	for _, c := range w.Connections {
		if c.From == id {
			out = append(out, c)
		}
	}

	return out
}

// Incoming returns the connections entering a node, in declaration order.
func (w Workflow) Incoming(id NodeID) []Connection {
	var in []Connection

	for _, c := range w.Connections {
		if c.To == id {
			in = append(in, c)
		}
	}

	return in
}

// Validate checks the graph's structure and reports every problem at once. It does not
// know node types: whether a type or port exists is checked where types are registered.
func (w Workflow) Validate() error {
	var problems []error

	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("%w: "+format, append([]any{ErrInvalidWorkflow}, args...)...))
	}

	if len(w.Nodes) == 0 {
		problem("no nodes")
	}

	seen := make(map[NodeID]bool, len(w.Nodes))
	for i, n := range w.Nodes {
		switch {
		case n.ID == "":
			problem("node %d has no id", i)
		case seen[n.ID]:
			problem("duplicate node id %q", n.ID)
		}

		if n.Type == "" {
			problem("node %q has no type", n.ID)
		}

		seen[n.ID] = true
	}

	unique := make(map[Connection]bool, len(w.Connections))
	for _, c := range w.Connections {
		if !seen[c.From] {
			problem("connection from unknown node %q", c.From)
		}

		if !seen[c.To] {
			problem("connection to unknown node %q", c.To)
		}

		if c.FromPort == "" || c.ToPort == "" {
			problem("connection %s -> %s has an unnamed port", c.From, c.To)
		}

		if c.From == c.To {
			problem("node %q is connected to itself", c.From)
		}

		if unique[c] {
			problem("duplicate connection %s.%s -> %s.%s", c.From, c.FromPort, c.To, c.ToPort)
		}

		unique[c] = true
	}

	if len(problems) == 0 {
		if _, err := w.TopologicalOrder(); err != nil {
			problems = append(problems, err)
		}
	}

	return errors.Join(problems...)
}

// TopologicalOrder returns node IDs so that every node comes after all nodes feeding it.
// Ties keep declaration order, so the result is deterministic. A cycle is an error.
// Connections to unknown nodes are ignored; Validate reports them.
func (w Workflow) TopologicalOrder() ([]NodeID, error) {
	indegree := make(map[NodeID]int, len(w.Nodes))
	for _, n := range w.Nodes {
		indegree[n.ID] = 0
	}

	for _, c := range w.Connections {
		if _, known := indegree[c.From]; !known {
			continue
		}

		if _, known := indegree[c.To]; known {
			indegree[c.To]++
		}
	}

	order := make([]NodeID, 0, len(indegree))
	placed := make(map[NodeID]bool, len(indegree))

	// Repeated passes in declaration order: O(n²) worst case, fine for editor-sized graphs.
	for len(order) < len(indegree) {
		progressed := false

		for _, n := range w.Nodes {
			if placed[n.ID] || indegree[n.ID] > 0 {
				continue
			}

			placed[n.ID] = true
			order = append(order, n.ID)
			progressed = true

			for _, c := range w.Outgoing(n.ID) {
				if _, known := indegree[c.To]; known {
					indegree[c.To]--
				}
			}
		}

		if !progressed {
			var stuck []NodeID

			for id := range indegree {
				if !placed[id] {
					stuck = append(stuck, id)
				}
			}

			slices.Sort(stuck)

			return nil, fmt.Errorf("%w: cycle, cannot order %v", ErrInvalidWorkflow, stuck)
		}
	}

	return order, nil
}
