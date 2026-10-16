// Package customnodes runs custom nodes: node types a user defined, with settings, ports
// and items from a form and TypeScript that does the work. Their run(items, ctx) is
// called once per node run with every item (D53), in the sandbox, with the SDK and its
// data requests for the workflow's owner.
package customnodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Export is the function of a custom node's code that runs it.
const Export = "run"

// Node is one custom node.
type Node struct {
	def  domain.CustomNode
	typ  engine.NodeType
	code ports.CodeCaller
	log  logging.Logger
}

var _ engine.Node = (*Node)(nil)

// New makes the node a definition describes. Registering it checks the type.
func New(def domain.CustomNode, code ports.CodeCaller, log logging.Logger) *Node {
	return &Node{def: def, typ: TypeOf(def), code: code, log: log}
}

// TypeOf is the node type a definition describes: custom.<name>, one input, the outputs
// and settings it lists, and its items.
func TypeOf(def domain.CustomNode) engine.NodeType {
	t := engine.NodeType{
		Name: engine.CustomTypePrefix + def.Name, Label: def.Label, Description: def.Description,
		Inputs: []string{engine.DefaultPort}, Outputs: slices.Clone(def.Outputs),
		Params: make([]engine.ParamSpec, len(def.Settings)),
		Code:   true,
	}

	for i, s := range def.Settings {
		kinds := make([]engine.FieldKind, len(s.FieldKinds))
		for j, k := range s.FieldKinds {
			kinds[j] = engine.FieldKind(k)
		}

		t.Params[i] = engine.ParamSpec{
			Name: s.Name, Label: s.Label, Description: s.Description, Kind: engine.ParamKind(s.Kind),
			Required: s.Required, Default: s.Default, Options: slices.Clone(s.Options),
			Field: s.Field, FieldKinds: kinds,
		}
	}

	if def.Items.Passed {
		t.Items = shape.Passed()
	} else {
		t.Items = &engine.Items{Fields: Fields(def.Items.Fields)}
	}

	return t
}

// Fields are the item fields a definition declares, as node types describe them.
func Fields(fields []domain.CustomField) []engine.Field {
	out := make([]engine.Field, len(fields))

	for i, f := range fields {
		out[i] = engine.Field{
			Name: f.Name, Description: f.Description, Kind: engine.FieldKind(f.Kind),
			Optional: f.Optional, Nullable: f.Nullable, Options: slices.Clone(f.Options),
			Fields: Fields(f.Fields),
		}

		if f.Of != nil {
			of := Fields([]domain.CustomField{*f.Of})[0]
			out[i].Of = &of
		}
	}

	return out
}

func (n *Node) Type() engine.NodeType { return n.typ }

func (n *Node) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return out, nil
	}

	refs := make([]engine.ItemRef, len(items))
	args := make([]any, len(items))

	for i, it := range items {
		refs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
		args[i] = it.JSON
	}

	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	res, err := n.code.Call(ctx, ports.CodeCall{
		Owner: rc.OwnerID, Source: n.def.Source, Export: Export, Args: []any{args},
		Params:    maps.Clone(p),
		Execution: ports.CodeExecution{ID: string(rc.ExecutionID), WorkflowID: string(rc.WorkflowID), StartedAt: rc.StartedAt},
	})

	for _, l := range res.Logs {
		n.log.Info(ctx, l.Text, "workflow_id", rc.WorkflowID, "execution_id", rc.ExecutionID, "node_id", rc.NodeID, "level", l.Level)
	}

	var codeErr *domain.CodeError
	if errors.As(err, &codeErr) {
		failAll(out, items, refs, err)
		return out, nil
	}

	if err != nil {
		return nil, err
	}

	sent, err := n.sent(res.Output)
	if err != nil {
		// No one item to blame: every item it was given failed, for an error path to take.
		failAll(out, items, refs, err)
		return out, nil
	}

	for _, port := range n.typ.Outputs {
		out.Add(port, sent[port]...)
	}

	return out, nil
}

// sent reads what run returned: the items, with one output; with several, the items of
// each port, by name, a port left out sending none. Declared fields are checked (D55).
func (n *Node) sent(raw json.RawMessage) (map[string][]engine.Item, error) {
	var v any
	if err := decode(raw, &v); err != nil {
		return nil, fmt.Errorf("run returned what is not JSON: %w", err)
	}

	byPort := map[string]any{}

	if len(n.typ.Outputs) == 1 {
		byPort[n.typ.Outputs[0]] = v
	} else {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("run must return the items of each output by name, like { %s: [...] }, not %s",
				n.typ.Outputs[0], describe(raw))
		}

		for _, port := range slices.Sorted(maps.Keys(m)) {
			if !slices.Contains(n.typ.Outputs, port) {
				return nil, fmt.Errorf("run returned items for %q, which is not one of its outputs: %s",
					port, strings.Join(n.typ.Outputs, ", "))
			}
		}

		byPort = m
	}

	out := make(map[string][]engine.Item, len(byPort))

	for _, port := range n.typ.Outputs {
		raw, given := byPort[port]
		if !given {
			continue
		}

		list, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of items, not %s", n.where(port), describe(mustJSON(raw)))
		}

		for i, e := range list {
			obj, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s item %d is %s, not an object", n.where(port), i, describe(mustJSON(e)))
			}

			if !n.typ.Items.Received() {
				if err := shape.Has(n.typ.Items.Fields, obj); err != nil {
					return nil, fmt.Errorf("%s item %d does not match the node's fields: %w", n.where(port), i, err)
				}
			}

			out[port] = append(out[port], engine.Item{JSON: obj})
		}
	}

	return out, nil
}

// where names what run returned for port, as the code has it.
func (n *Node) where(port string) string {
	if len(n.typ.Outputs) == 1 {
		return "what run returned"
	}

	return fmt.Sprintf("what run returned for %s", port)
}

func failAll(out engine.Ports, items []engine.Item, refs []engine.ItemRef, err error) {
	for i, it := range items {
		out.Fail(it, refs[i], err)
	}
}

// decode reads JSON with numbers kept exact, as items carry them.
func decode(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	return dec.Decode(v)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// describe shows what the code gave, briefly.
func describe(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "nothing"
	}

	if r := []rune(s); len(r) > 60 {
		s = string(r[:59]) + "…"
	}

	return s
}
