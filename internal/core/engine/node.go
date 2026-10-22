package engine

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const (
	DefaultPort = "main"

	// ErrorPort receives items that failed. The engine routes it like any other port
	// when the node continues on failure, and fails the execution otherwise.
	ErrorPort = "error"
)

// ItemRef points at the input item an output item was derived from (n8n pairedItem).
type ItemRef struct {
	Port  string
	Index int
}

// Item is one record flowing between nodes. Fan-out hands the same item to every
// connected node, so nodes must treat input items, JSON included, as read-only.
type Item struct {
	JSON   map[string]any
	Source []ItemRef
	Err    error // set only on items emitted on ErrorPort
}

// Ports maps a port name to its items. Branching nodes (if, switch) emit on several
// output ports; merge-style nodes read several input ports.
type Ports map[string][]Item

func (p Ports) Add(port string, items ...Item) {
	p[port] = append(p[port], items...)
}

// Counts returns the number of items per port.
func (p Ports) Counts() map[string]int {
	counts := make(map[string]int, len(p))
	for port, items := range p {
		counts[port] = len(items)
	}

	return counts
}

// Len is the number of items across all ports.
func (p Ports) Len() int {
	n := 0
	for _, items := range p {
		n += len(items)
	}

	return n
}

// Fail emits the input item on ErrorPort, keeping its data so an error branch can use it.
func (p Ports) Fail(in Item, src ItemRef, err error) {
	p.Add(ErrorPort, Item{JSON: in.JSON, Source: []ItemRef{src}, Err: err})
}

type Params map[string]any

// ParamResolver evaluates a node's parameter expressions against one input item.
type ParamResolver interface {
	Resolve(src ItemRef) (Params, error)
}

type RunContext struct {
	ExecutionID domain.ExecutionID
	StartedAt   time.Time // the execution's
	WorkflowID  domain.WorkflowID
	OwnerID     domain.UserID // nodes act on this user's behalf, e.g. with their wallets
	NodeID      domain.NodeID
	Params      ParamResolver
}

type TriggerContext struct {
	WorkflowID domain.WorkflowID
	OwnerID    domain.UserID
	NodeID     domain.NodeID
	Params     Params // resolved once, there is no input item

	// Resumed says the workflow was running before this start, which picks it up again:
	// the server started, or the workflow was saved while active. A trigger that can
	// look back may catch up on what happened meanwhile; on a first start there is
	// nothing to catch up on.
	Resumed bool
}

type ParamKind string

const (
	ParamString   ParamKind = "string"
	ParamNumber   ParamKind = "number" // a whole number
	ParamDecimal  ParamKind = "decimal"
	ParamBool     ParamKind = "bool"
	ParamList     ParamKind = "list"     // of strings
	ParamDuration ParamKind = "duration" // a Go duration string: "30s", "5m"
	ParamWallet   ParamKind = "wallet"   // the ID of one of the owner's wallets
	ParamTokens   ParamKind = "tokens"   // a list of token IDs
	ParamAddress  ParamKind = "address"  // a Polygon address: 0x and 40 hex digits

	// ParamTable is a list of rows, each an object of the cells its Columns describe
	// (D80): a trader per row, say.
	ParamTable ParamKind = "table"

	// ParamCredential is the ID of one of the owner's credentials, of a type the param's
	// CredentialTypes name.
	ParamCredential ParamKind = "credential"

	// ParamCode is TypeScript, the code of a code node.
	ParamCode ParamKind = "code"
)

// ParamSpec describes one parameter: how to read it, and how an editor should present it.
type ParamSpec struct {
	Name        string
	Label       string
	Description string
	Kind        ParamKind
	Required    bool
	Default     any

	// Options lists the allowed values of a string parameter, or of each element of a
	// list parameter. Empty means any value.
	Options []string

	// Sensitive marks values that are personal, like a webhook URL: sharing a workflow
	// leaves them out. Wallet and credential params are personal regardless.
	Sensitive bool

	// CredentialTypes name the kinds of credential a credential param takes: any one of
	// them, such as any provider's key (D254).
	CredentialTypes []string

	// ModelsOf, on a text param, names the credential param whose key's models it
	// suggests, as the provider lists them; any other name can still be typed (D251).
	ModelsOf string

	// Multiline marks a text param of several lines, a prompt, say, for an editor.
	Multiline bool

	// Field marks a text param, or each value of a list param, as a path to a field of
	// the items the node receives, like spread or outcomes.0.price. FieldKinds, when
	// set, are the kinds that field may be.
	Field      bool
	FieldKinds []FieldKind

	// Input, for a field param, is the input port whose items it names; empty for main.
	Input string

	// Template marks a text param the node fills in itself, per item, with
	// Params.Template, so it can escape the values: a message in HTML, say.
	Template bool

	// Numbers lets a text param hold a number too, which the node reads as its text with
	// Params.Text: a value compared as a number or as text, say.
	Numbers bool

	// Columns are the cells of each row of a table param, in order, keyed by Name. Each
	// is a kind a cell can hold (CellKinds), with Options for one of a few choices.
	// Cells are plain values: only the whole table takes an expression (D82).
	Columns []ParamSpec
}

// CellKinds are the kinds a table's columns may be (D80).
var CellKinds = []ParamKind{ParamString, ParamNumber, ParamDecimal, ParamBool, ParamAddress}

// MaxRows is the most rows a table param may have.
const MaxRows = 500

// Personal reports whether the value belongs to whoever configured it, so sharing must
// leave it out.
func (p ParamSpec) Personal() bool {
	return p.Sensitive || p.Kind == ParamWallet || p.Kind == ParamCredential
}

// NodeType describes a kind of node for the registry, the editor and workflow validation.
type NodeType struct {
	Name        string // "polymarket.orderbook", "logic.if"
	Label       string
	Description string
	Inputs      []string
	Outputs     []string
	Params      []ParamSpec

	// OutputsParam names a list param whose values are more output ports, after Outputs:
	// buttons [Yes, No] give a port for each. Empty means the type's ports are fixed.
	// With OutputsColumn, it names a table instead, and that text column of each row
	// names a port: a rule each, say.
	OutputsParam  string
	OutputsColumn string

	// Items describes what it emits; nil when nobody knows, as for code whose author did
	// not say.
	Items *Items

	// Code says it runs code its owner wrote, which the admin's code switch covers.
	Code bool

	// Remembers says it keeps what it saw across runs, in node memory (D121), which its
	// settings show and Forget clears (D137).
	Remembers bool

	// Plugin is the ID of the plugin it comes from (phase 5), whose owner may restrict who
	// uses it (D282); empty for a built-in.
	Plugin string

	// SeesItems lets its settings' expressions see items, every item it receives on main,
	// beside each one's fields by name: for a node that takes them together (D257). A
	// field named items is then item.items.
	SeesItems bool
}

// Node is a step that transforms items. Per-item failures go to ErrorPort via Ports.Fail;
// a returned error fails the whole node (bad config, cancelled context).
type Node interface {
	Type() NodeType
	Run(ctx context.Context, rc RunContext, in Ports) (Ports, error)
}

// Emit starts one execution with the trigger's output.
type Emit func(ctx context.Context, out Ports) error

// Trigger starts executions: schedules, market streams, webhooks. Start blocks until ctx is done.
type Trigger interface {
	Type() NodeType
	Start(ctx context.Context, tc TriggerContext, emit Emit) error
}
