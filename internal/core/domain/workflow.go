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
	Description string // what it does, in its owner's words; searched with its name
	Active      bool
	Nodes       []NodeSpec
	Connections []Connection

	// Version increases with every saved change; updates name the version they edited,
	// so concurrent edits cannot silently overwrite each other.
	Version   int
	CreatedAt time.Time
	UpdatedAt time.Time

	// ActedBy is the agent or token that switched it on, or changed it while it was on,
	// named on its runs until it is switched on or off again (D407); zero for the user.
	ActedBy Actor

	// Pin is its place among its owner's pinned workflows, from 1; 0 when it is not
	// pinned. Pinning is how the list shows it, not a change to it: its version stays.
	Pin int

	// Borrowed is what of it another user wrote, kept from their library entry it was made
	// from until its user changes it (A21).
	Borrowed Borrowed
}

// Borrowed is the text of a workflow another user wrote: its name, and the nodes whose
// names and notes are theirs. Agents get it as others' words, never the user's (A21).
type Borrowed struct {
	Name  bool
	Nodes []NodeID
}

// Node says node's name and notes are another user's.
func (b Borrowed) Node(node NodeID) bool { return slices.Contains(b.Nodes, node) }

// BorrowedText stands for another user's text where an agent sees it, the text itself under
// untrusted: a workflow saved with it keeps the text it had (A21).
const BorrowedText = "(another user's words: see untrusted)"

// CanvasPosition is where a node sits on the editor canvas. It never affects execution.
type CanvasPosition struct {
	X float64
	Y float64
}

// NodeSpec is a node as configured in the editor. Type selects the implementation.
type NodeSpec struct {
	ID       NodeID
	Type     string // "polymarket.orderbook", "logic.if"
	Name     string
	Params   map[string]any
	Position CanvasPosition
	Notes    string // shown with the node in the editor; never affects execution

	// ContinueOnFail routes items that failed to the node's error port instead of
	// failing the execution.
	ContinueOnFail bool
}

// HiddenPersonal stands for the value of a personal setting, such as a webhook's URL, that
// a token or an agent may not see (D401, D402): a workflow saved with it keeps the value
// it had.
func HiddenPersonal() map[string]any { return map[string]any{"$personal": "hidden"} }

// IsHiddenPersonal says v stands for a hidden personal setting.
func IsHiddenPersonal(v any) bool {
	m, ok := v.(map[string]any)
	return ok && len(m) == 1 && m["$personal"] == "hidden"
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

// Validate checks the graph's structure and reports every problem at once, as a
// *ValidationError. It does not know node types: whether a type or port exists is
// checked where types are registered.
func (w Workflow) Validate() error {
	var v ValidationError

	problem := func(node NodeID, format string, args ...any) {
		v.Add(Problem{NodeID: node, Message: fmt.Sprintf(format, args...)})
	}

	if len(w.Nodes) == 0 {
		problem("", "no nodes")
	}

	seen := make(map[NodeID]bool, len(w.Nodes))
	for i, n := range w.Nodes {
		switch {
		case n.ID == "":
			problem("", "node %d has no id", i)
		case seen[n.ID]:
			problem(n.ID, "duplicate node id %q", n.ID)
		}

		if n.Type == "" {
			problem(n.ID, "node %q has no type", n.ID)
		}

		seen[n.ID] = true
	}

	unique := make(map[Connection]bool, len(w.Connections))
	for _, c := range w.Connections {
		if !seen[c.From] {
			problem(c.To, "connection from unknown node %q", c.From)
		}

		if !seen[c.To] {
			problem(c.From, "connection to unknown node %q", c.To)
		}

		if c.FromPort == "" || c.ToPort == "" {
			problem(c.From, "connection %s -> %s has an unnamed port", c.From, c.To)
		}

		if c.From == c.To {
			problem(c.From, "node %q is connected to itself", c.From)
		}

		if unique[c] {
			problem(c.From, "duplicate connection %s.%s -> %s.%s", c.From, c.FromPort, c.To, c.ToPort)
		}

		unique[c] = true
	}

	if len(v.Problems) == 0 {
		if _, stuck := w.order(); len(stuck) > 0 {
			for _, id := range stuck {
				problem(id, "node %q: cycle, cannot order %v", id, stuck)
			}
		}
	}

	return v.Err()
}

// TopologicalOrder returns node IDs so that every node comes after all nodes feeding it.
// Ties keep declaration order, so the result is deterministic. A cycle is an error.
// Connections to unknown nodes are ignored; Validate reports them.
func (w Workflow) TopologicalOrder() ([]NodeID, error) {
	order, stuck := w.order()
	if len(stuck) > 0 {
		return nil, fmt.Errorf("%w: cycle, cannot order %v", ErrInvalidWorkflow, stuck)
	}

	return order, nil
}

// order sorts the nodes topologically. stuck lists, sorted, the nodes that could not be
// placed because of a cycle: those in it and those downstream of it.
func (w Workflow) order() (order, stuck []NodeID) {
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

	order = make([]NodeID, 0, len(indegree))
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
			for id := range indegree {
				if !placed[id] {
					stuck = append(stuck, id)
				}
			}

			slices.Sort(stuck)

			return order, stuck
		}
	}

	return order, nil
}
