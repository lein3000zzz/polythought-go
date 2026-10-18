package logic

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const FilterType = "logic.filter"

var _ engine.Node = Filter{}

// Filter keeps the items that pass its tests (tests.go) and drops the rest. An item whose
// field one cannot test fails onto the error port, as with If.
type Filter struct{}

func (Filter) Type() engine.NodeType {
	return engine.NodeType{
		Name:  FilterType,
		Label: "Filter",
		Description: "Keeps the items that pass its tests and drops the others: a number compared with a " +
			"value, text that contains a value, or a field being there at all. Several tests count together " +
			"as Match says.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Passed(),
		Params:  testParams(),
	}
}

func (Filter) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		keep, itemErr, err := passes(p, item)
		if err != nil {
			return nil, err
		}

		switch {
		case itemErr != nil:
			out.Fail(item, src, itemErr)
		case keep:
			out.Add(engine.DefaultPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}
