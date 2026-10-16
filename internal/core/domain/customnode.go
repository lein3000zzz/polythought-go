package domain

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

type CustomNodeID string

// CustomNode is a node type a user defined: its settings, ports and items as a form has
// them, and TypeScript that does the work, run(items, ctx). Its type is custom.<Name>,
// in its owner's own namespace.
type CustomNode struct {
	ID          CustomNodeID
	OwnerID     UserID
	Name        string // lowercase, like spread-split; fixed once made
	Label       string
	Description string
	Settings    []CustomSetting
	Outputs     []string // its output ports, in order
	Items       CustomItems
	Source      string
	Version     int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CustomSetting is one setting of a custom node, as a node type's setting is described.
type CustomSetting struct {
	Name        string
	Label       string
	Description string
	Kind        string // string, number, decimal, bool, duration, list or wallet
	Required    bool
	Default     any
	Options     []string // the values a string, or each value of a list, may take
	Field       bool     // a path to a field of the items it gets
	FieldKinds  []string // the kinds that field may be; empty means any
}

// CustomItems is what a custom node sends on: the items it got, or ones with its fields.
type CustomItems struct {
	Passed bool
	Fields []CustomField
}

// CustomField is a field of the items a custom node makes, as node types describe them.
type CustomField struct {
	Name        string
	Description string
	Kind        string // text, number, decimal, bool, time, list, object or any
	Optional    bool
	Nullable    bool
	Options     []string     // the values text may take
	Of          *CustomField // what a list holds
	Fields      []CustomField
}

var (
	// ErrCustomNodeTaken means the owner already has a custom node of that name.
	ErrCustomNodeTaken = errors.New("you already have a custom node of that name")
	// ErrBreaksWorkflows means a change to a custom node would break workflows that use
	// it; a *BreaksError says which.
	ErrBreaksWorkflows = errors.New("the change breaks workflows that use the node")
)

// BreaksError lists the workflows a change to a custom node would break, and how: what
// saving them would then refuse (D50).
type BreaksError struct {
	Workflows []BrokenWorkflow
}

type BrokenWorkflow struct {
	ID       WorkflowID
	Name     string
	Problems []Problem
}

func (e *BreaksError) Error() string {
	names := make([]string, len(e.Workflows))
	for i, w := range e.Workflows {
		names[i] = strconv.Quote(w.Name)
	}

	return ErrBreaksWorkflows.Error() + ": " + strings.Join(names, ", ")
}

func (e *BreaksError) Unwrap() error { return ErrBreaksWorkflows }
