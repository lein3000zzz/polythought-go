package engine

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

var ErrUnknownNodeType = errors.New("unknown node type")

// Registry holds the node and trigger implementations by type name.
type Registry struct {
	mu       sync.RWMutex
	nodes    map[string]Node
	triggers map[string]Trigger
}

func NewRegistry() *Registry {
	return &Registry{nodes: make(map[string]Node), triggers: make(map[string]Trigger)}
}

func (r *Registry) RegisterNode(n Node) error {
	return r.register(n.Type(), func() { r.nodes[n.Type().Name] = n })
}

func (r *Registry) RegisterTrigger(t Trigger) error {
	return r.register(t.Type(), func() { r.triggers[t.Type().Name] = t })
}

func (r *Registry) register(typ NodeType, store func()) error {
	if typ.Name == "" {
		return errors.New("register node type: empty name")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.nodes[typ.Name]; ok {
		return fmt.Errorf("register node type %q: already registered", typ.Name)
	}

	if _, ok := r.triggers[typ.Name]; ok {
		return fmt.Errorf("register node type %q: already registered", typ.Name)
	}

	store()

	return nil
}

func (r *Registry) Node(name string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	n, ok := r.nodes[name]

	return n, ok
}

func (r *Registry) Trigger(name string) (Trigger, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	t, ok := r.triggers[name]

	return t, ok
}

// Type describes any registered node or trigger type.
func (r *Registry) Type(name string) (typ NodeType, trigger bool, ok bool) {
	if n, ok := r.Node(name); ok {
		return n.Type(), false, true
	}

	if t, ok := r.Trigger(name); ok {
		return t.Type(), true, true
	}

	return NodeType{}, false, false
}

// Validate runs the structural checks of domain.Workflow.Validate, then the ones that
// need node types. Every problem matches domain.ErrInvalidWorkflow.
func (r *Registry) Validate(w domain.Workflow) error {
	if err := w.Validate(); err != nil {
		return err
	}

	var problems []error

	problem := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf("%w: "+format, append([]any{domain.ErrInvalidWorkflow}, args...)...))
	}

	types := make(map[domain.NodeID]NodeType, len(w.Nodes))
	triggers := 0

	for _, n := range w.Nodes {
		typ, trigger, ok := r.Type(n.Type)
		if !ok {
			problems = append(problems, fmt.Errorf("%w: node %q: %w %q", domain.ErrInvalidWorkflow, n.ID, ErrUnknownNodeType, n.Type))
			continue
		}

		types[n.ID] = typ

		if trigger {
			triggers++
		}

		for _, spec := range typ.Params {
			if spec.Required && spec.Default == nil && n.Params[spec.Name] == nil {
				problem("node %q: missing required param %q", n.ID, spec.Name)
			}
		}
	}

	if triggers == 0 {
		problem("no trigger node")
	}

	for _, c := range w.Connections {
		from, fromOK := types[c.From]
		to, toOK := types[c.To]

		if fromOK && c.FromPort == ErrorPort {
			if spec, _ := w.Node(c.From); !spec.ContinueOnFail {
				problem("node %q: error port is connected but continueOnFail is off", c.From)
			}
		} else if fromOK && !slices.Contains(from.Outputs, c.FromPort) {
			problem("node %q (%s) has no output port %q", c.From, from.Name, c.FromPort)
		}

		if toOK && !slices.Contains(to.Inputs, c.ToPort) {
			problem("node %q (%s) has no input port %q", c.To, to.Name, c.ToPort)
		}
	}

	return errors.Join(problems...)
}
