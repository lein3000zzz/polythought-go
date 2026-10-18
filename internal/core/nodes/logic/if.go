// Package logic holds nodes that route items.
package logic

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const (
	IfType = "logic.if"

	PortTrue  = "true"
	PortFalse = "false"
)

var _ engine.Node = If{}

// If tests each item and routes it to "true" or "false", by the tests Filter has
// (tests.go). A field one cannot test fails the item.
type If struct{}

func (If) Type() engine.NodeType {
	return engine.NodeType{
		Name:  IfType,
		Label: "If",
		Description: "Tests each item and routes it to true or false: a number compared with a value, text " +
			"that contains a value, or a field being there at all. Several tests count together as Match says.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{PortTrue, PortFalse},
		Items:   shape.Passed(),
		Params:  testParams(),
	}
}

func (If) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		ok, itemErr, err := passes(p, item)
		if err != nil {
			return nil, err
		}

		port := PortFalse
		if ok {
			port = PortTrue
		}

		if itemErr != nil {
			out.Fail(item, src, itemErr)
			continue
		}

		out.Add(port, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
	}

	return out, nil
}
