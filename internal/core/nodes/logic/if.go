// Package logic holds nodes that route items.
package logic

import (
	"context"
	"fmt"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

const (
	IfType = "logic.if"

	PortTrue  = "true"
	PortFalse = "false"
)

var _ engine.Node = If{}

// operators maps an operator name to the comparison results that satisfy it.
var operators = map[string]func(cmp int) bool{
	"gt":  func(c int) bool { return c > 0 },
	"gte": func(c int) bool { return c >= 0 },
	"lt":  func(c int) bool { return c < 0 },
	"lte": func(c int) bool { return c <= 0 },
	"eq":  func(c int) bool { return c == 0 },
	"neq": func(c int) bool { return c != 0 },
}

// If compares a numeric field of each item with a value and routes it to "true" or
// "false". A missing or null field is false; a non-numeric one fails the item.
type If struct{}

func (If) Type() engine.NodeType {
	return engine.NodeType{
		Name:    IfType,
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{PortTrue, PortFalse},
		Params: []engine.ParamSpec{
			{Name: "field", Kind: engine.ParamString, Required: true},
			{Name: "operator", Kind: engine.ParamString, Default: "gt"},
			{Name: "value", Kind: engine.ParamDecimal, Required: true},
		},
	}
}

func (If) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		matched, err := evaluate(rc, src, item)
		if err != nil {
			return nil, err
		}

		switch {
		case matched.err != nil:
			out.Fail(item, src, matched.err)
		case matched.ok:
			out.Add(PortTrue, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
		default:
			out.Add(PortFalse, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

type outcome struct {
	ok  bool
	err error // the item's own problem, routed to the error port
}

// evaluate returns an error only for bad configuration, which fails the node.
func evaluate(rc engine.RunContext, src engine.ItemRef, item engine.Item) (outcome, error) {
	p, err := rc.Params.Resolve(src)
	if err != nil {
		return outcome{}, err
	}

	field, err := p.String("field")
	if err != nil {
		return outcome{}, err
	}

	op, err := p.String("operator")
	if err != nil {
		return outcome{}, err
	}

	satisfies, known := operators[op]
	if !known {
		return outcome{}, fmt.Errorf("%w %q: unknown operator %q", engine.ErrInvalidParam, "operator", op)
	}

	value, err := p.Decimal("value")
	if err != nil {
		return outcome{}, err
	}

	if item.JSON[field] == nil {
		return outcome{}, nil
	}

	got, err := engine.Params(item.JSON).Decimal(field)
	if err != nil {
		return outcome{err: fmt.Errorf("field %q: %w", field, err)}, nil
	}

	return outcome{ok: satisfies(got.Cmp(value))}, nil
}
