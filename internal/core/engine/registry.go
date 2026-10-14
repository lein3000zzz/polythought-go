package engine

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

var ErrUnknownNodeType = errors.New("unknown node type")

// Registry holds the node and trigger implementations by type name, and the credential
// types their params may ask for.
type Registry struct {
	mu          sync.RWMutex
	nodes       map[string]Node
	triggers    map[string]Trigger
	credentials map[string]CredentialType
}

func NewRegistry() *Registry {
	return &Registry{
		nodes:       make(map[string]Node),
		triggers:    make(map[string]Trigger),
		credentials: make(map[string]CredentialType),
	}
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

	if err := r.checkCredentialParams(typ); err != nil {
		return err
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
// need node types. Problems come back together as a *domain.ValidationError, which
// matches domain.ErrInvalidWorkflow.
func (r *Registry) Validate(w domain.Workflow) error {
	if err := w.Validate(); err != nil {
		return err
	}

	var v domain.ValidationError

	types := make(map[domain.NodeID]NodeType, len(w.Nodes))
	triggers := 0

	for _, n := range w.Nodes {
		typ, trigger, ok := r.Type(n.Type)
		if !ok {
			v.Add(domain.Problem{
				NodeID:  n.ID,
				Message: fmt.Sprintf("node %q: %s %q", n.ID, ErrUnknownNodeType, n.Type),
				Err:     ErrUnknownNodeType,
			})

			continue
		}

		types[n.ID] = typ

		if trigger {
			triggers++
		}

		checkParams(&v, n, typ)
	}

	if triggers == 0 {
		v.Add(domain.Problem{Message: "no trigger node"})
	}

	for _, c := range w.Connections {
		from, fromOK := types[c.From]
		to, toOK := types[c.To]

		if fromOK && c.FromPort == ErrorPort {
			if spec, _ := w.Node(c.From); !spec.ContinueOnFail {
				v.Add(domain.Problem{NodeID: c.From, Message: fmt.Sprintf("node %q: error port is connected but continueOnFail is off", c.From)})
			}
		} else if fromOK && !slices.Contains(from.Outputs, c.FromPort) {
			v.Add(domain.Problem{NodeID: c.From, Message: fmt.Sprintf("node %q (%s) has no output port %q", c.From, from.Name, c.FromPort)})
		}

		if toOK && !slices.Contains(to.Inputs, c.ToPort) {
			v.Add(domain.Problem{NodeID: c.To, Message: fmt.Sprintf("node %q (%s) has no input port %q", c.To, to.Name, c.ToPort)})
		}
	}

	return v.Err()
}

// checkParams reports missing required params, unknown params and values that do not
// fit their spec.
func checkParams(v *domain.ValidationError, n domain.NodeSpec, typ NodeType) {
	declared := make(map[string]bool, len(typ.Params))

	for _, spec := range typ.Params {
		declared[spec.Name] = true

		value := n.Params[spec.Name]
		if value == nil {
			if spec.Required && spec.Default == nil {
				v.Add(domain.Problem{NodeID: n.ID, Param: spec.Name, Message: fmt.Sprintf("node %q: missing required param %q", n.ID, spec.Name)})
			}

			continue
		}

		if msg := checkParam(spec, value); msg != "" {
			v.Add(domain.Problem{
				NodeID:  n.ID,
				Param:   spec.Name,
				Message: fmt.Sprintf("node %q: param %q %s", n.ID, spec.Name, msg),
				Err:     ErrInvalidParam,
			})
		}
	}

	for _, name := range slices.Sorted(maps.Keys(n.Params)) {
		if !declared[name] {
			v.Add(domain.Problem{NodeID: n.ID, Param: name, Message: fmt.Sprintf("node %q: unknown param %q", n.ID, name)})
		}
	}
}

// TypeInfo describes a registered type for clients such as the editor.
type TypeInfo struct {
	NodeType

	Trigger bool
}

// Types lists every registered node and trigger type, sorted by name.
func (r *Registry) Types() []TypeInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]TypeInfo, 0, len(r.nodes)+len(r.triggers))
	for _, n := range r.nodes {
		out = append(out, TypeInfo{NodeType: n.Type()})
	}

	for _, t := range r.triggers {
		out = append(out, TypeInfo{NodeType: t.Type(), Trigger: true})
	}

	slices.SortFunc(out, func(a, b TypeInfo) int { return strings.Compare(a.Name, b.Name) })

	return out
}
