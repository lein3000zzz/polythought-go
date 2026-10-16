// Package codenodes holds the scaffold nodes: steps whose work is TypeScript the user
// writes, called as each says (D42), with the SDK and its data requests for the
// workflow's owner, in the sandbox, one run per node run.
package codenodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	TransformType = "code.transform"
	FilterType    = "code.filter"
	RouteType     = "code.route"
	AggregateType = "code.aggregate"

	sourceParam = "source"
	routesParam = "routes"
)

type kind int

const (
	transform kind = iota
	filter
	route
	aggregate
)

// Node is a scaffold node of one kind.
type Node struct {
	kind kind
	code ports.CodeCaller
	log  logging.Logger
}

var _ engine.Node = (*Node)(nil)

func NewTransform(code ports.CodeCaller, log logging.Logger) *Node {
	return &Node{transform, code, log}
}
func NewFilter(code ports.CodeCaller, log logging.Logger) *Node { return &Node{filter, code, log} }
func NewRoute(code ports.CodeCaller, log logging.Logger) *Node  { return &Node{route, code, log} }
func NewAggregate(code ports.CodeCaller, log logging.Logger) *Node {
	return &Node{aggregate, code, log}
}

func (n *Node) Type() engine.NodeType {
	t := n.typ()
	t.Code = true

	return t
}

func (n *Node) typ() engine.NodeType {
	in := []string{engine.DefaultPort}

	switch n.kind {
	case filter:
		return engine.NodeType{
			Name: FilterType, Label: "Code: filter",
			Description: "Keeps the items TypeScript says to keep: keep(item, ctx) is called for each item and returns true to keep it.",
			Inputs:      in, Outputs: in, Items: shape.Passed(),
			Params: []engine.ParamSpec{source(filterStarter)},
		}
	case route:
		return engine.NodeType{
			Name: RouteType, Label: "Code: route",
			Description: "Sends each item down the route TypeScript picks: route(item, ctx) is called for each item and returns one of the Routes.",
			Inputs:      in, OutputsParam: routesParam, Items: shape.Passed(),
			Params: []engine.ParamSpec{
				source(routeStarter),
				{
					Name: routesParam, Label: "Routes", Kind: engine.ParamList, Default: []string{"wide", "tight"},
					Description: "One per line, up to 10. Each route is an output port of the same name.",
				},
			},
		}
	case aggregate:
		return engine.NodeType{
			Name: AggregateType, Label: "Code: aggregate",
			Description: "Sums the items up with TypeScript: run(items, ctx) is called once with all of them and returns one item.",
			Inputs:      in, Outputs: in,
			Params: []engine.ParamSpec{source(aggregateStarter)},
		}
	default:
		return engine.NodeType{
			Name: TransformType, Label: "Code: transform",
			Description: "Turns the items into others with TypeScript: run(items, ctx) is called once with all of them and returns the items to send on.",
			Inputs:      in, Outputs: in,
			Params: []engine.ParamSpec{source(transformStarter)},
		}
	}
}

func source(starter string) engine.ParamSpec {
	return engine.ParamSpec{
		Name: sourceParam, Label: "Code", Kind: engine.ParamCode, Default: starter,
		Description: "TypeScript, importing only from @polythought/sdk: dec for exact prices, data requests, and the types of every node's items.",
	}
}

func (n *Node) export() string {
	if n.kind == filter {
		return "keep"
	}

	if n.kind == route {
		return "route"
	}

	return "run"
}

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

	src, err := p.String(sourceParam)
	if err != nil {
		return nil, err
	}

	res, err := n.code.Call(ctx, ports.CodeCall{
		Owner: rc.OwnerID, Source: src, Export: n.export(), Args: []any{args}, Each: n.kind == filter || n.kind == route,
		Params:    settings(p),
		Execution: ports.CodeExecution{ID: string(rc.ExecutionID), WorkflowID: string(rc.WorkflowID), StartedAt: rc.StartedAt},
	})

	n.logLines(ctx, rc, res.Logs)

	var codeErr *domain.CodeError

	if errors.As(err, &codeErr) {
		// No one item to blame: every item it was given failed, for an error path to take.
		for i, it := range items {
			out.Fail(it, refs[i], err)
		}

		return out, nil
	}

	if err != nil {
		return nil, err
	}

	switch n.kind {
	case transform:
		return out, n.transformed(res.Output, items, refs, out)
	case aggregate:
		return out, n.aggregated(res.Output, items, refs, out)
	default:
		routes, err := p.Strings(routesParam)
		if n.kind == route && err != nil {
			return nil, err
		}

		return out, n.each(res.Output, routes, items, refs, out)
	}
}

// settings are the node's, for ctx.params: all but its code.
func settings(p engine.Params) map[string]any {
	out := make(map[string]any, len(p))

	for k, v := range p {
		if k != sourceParam {
			out[k] = v
		}
	}

	return out
}

// transformed sends on the items run returned. A run that did not return a list of
// objects fails every item, as code that throws does.
func (n *Node) transformed(raw json.RawMessage, items []engine.Item, refs []engine.ItemRef, out engine.Ports) error {
	var list []any
	if err := decode(raw, &list); err != nil || list == nil {
		failAll(out, items, refs, fmt.Errorf("run must return a list of items, not %s", describe(raw)))
		return nil
	}

	made := make([]engine.Item, 0, len(list))

	for i, v := range list {
		obj, ok := v.(map[string]any)
		if !ok {
			failAll(out, items, refs, fmt.Errorf("run returned %s as item %d, which is not an object", describe(mustJSON(v)), i))
			return nil
		}

		made = append(made, engine.Item{JSON: obj})
	}

	out.Add(engine.DefaultPort, made...)

	return nil
}

// aggregated sends on the one item run returned, made of all the items; null sends none.
func (n *Node) aggregated(raw json.RawMessage, items []engine.Item, refs []engine.ItemRef, out engine.Ports) error {
	var v any
	if err := decode(raw, &v); err != nil {
		return err
	}

	if v == nil {
		return nil
	}

	obj, ok := v.(map[string]any)
	if !ok {
		failAll(out, items, refs, fmt.Errorf("run must return one item, an object, not %s", describe(raw)))
		return nil
	}

	out.Add(engine.DefaultPort, engine.Item{JSON: obj, Source: slices.Clone(refs)})

	return nil
}

// each takes what keep or route gave for each item.
func (n *Node) each(raw json.RawMessage, routes []string, items []engine.Item, refs []engine.ItemRef, out engine.Ports) error {
	var outcomes []ports.CodeOutcome
	if err := json.Unmarshal(raw, &outcomes); err != nil || len(outcomes) != len(items) {
		return fmt.Errorf("the code gave %d answers for %d items", len(outcomes), len(items))
	}

	for i, it := range items {
		o := outcomes[i]
		passed := engine.Item{JSON: it.JSON, Source: []engine.ItemRef{refs[i]}}

		if o.Error != nil {
			out.Fail(it, refs[i], errors.New(*o.Error))
			continue
		}

		if n.kind == filter {
			var keep bool
			if err := json.Unmarshal(o.Value, &keep); err != nil {
				out.Fail(it, refs[i], fmt.Errorf("keep must return true or false, not %s", describe(o.Value)))
				continue
			}

			if keep {
				out.Add(engine.DefaultPort, passed)
			}

			continue
		}

		var name string
		if err := json.Unmarshal(o.Value, &name); err != nil || !slices.Contains(routes, name) {
			out.Fail(it, refs[i], fmt.Errorf("route gave %s, which is not one of its routes: %s", describe(o.Value), strings.Join(routes, ", ")))
			continue
		}

		out.Add(name, passed)
	}

	return nil
}

func (n *Node) logLines(ctx context.Context, rc engine.RunContext, logs []ports.CodeLog) {
	for _, l := range logs {
		n.log.Info(ctx, l.Text, "workflow_id", rc.WorkflowID, "execution_id", rc.ExecutionID, "node_id", rc.NodeID, "level", l.Level)
	}
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

const transformStarter = `import type { Context, Item } from "@polythought/sdk"

// Called once with every item; returns the items to send on.
export function run(items: Item[], ctx: Context): Item[] {
  return items.map((item) => ({ ...item, seenAt: ctx.execution.startedAt }))
}
`

const filterStarter = `import type { Context, Item } from "@polythought/sdk"

// Called for each item: true keeps it.
export function keep(item: Item, ctx: Context): boolean {
  return item.question !== undefined
}
`

const routeStarter = `import { dec } from "@polythought/sdk"
import type { Context, Item } from "@polythought/sdk"

// Called for each item: returns the route it goes down, one of Routes.
export function route(item: Item, ctx: Context): string {
  return dec(String(item.spread ?? 0)).gt("0.02") ? "wide" : "tight"
}
`

const aggregateStarter = `import type { Context, Item } from "@polythought/sdk"

// Called once with every item; returns one item that sums them up.
export function run(items: Item[], ctx: Context): Item {
  return { count: items.length }
}
`
