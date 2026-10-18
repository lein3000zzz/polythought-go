package logic

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/chance"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const (
	SampleType = "logic.sample"

	// PortRest takes the items Sample does not (D142).
	PortRest = "rest"
)

var _ engine.Node = Sample{}

// Sample takes about a share of the items (D122): each by a hash of a field with the
// workflow and node, so the same item gets the same answer every time (D140).
type Sample struct{}

func (Sample) Type() engine.NodeType {
	return engine.NodeType{
		Name:  SampleType,
		Label: "Sample",
		Description: "Takes about a share of the items, each the same way every time: at 30, about 3 in 10 go " +
			"on, and the rest leave from rest.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, PortRest},
		Items:   shape.Passed(),
		Params: []engine.ParamSpec{
			{
				Name: "chance", Label: "Chance %", Kind: engine.ParamDecimal, Required: true,
				Description: "The share to take, 0 to 100: 30 is about 3 in 10.",
			},
			{
				Name: "field", Label: "Decide by", Kind: engine.ParamString, Default: "txHash", Field: true,
				Description: "The field that tells items apart, e.g. txHash for trades or id: an item with the " +
					"same value gets the same answer, on a re-run too.",
			},
		},
	}
}

var hundred = decimal.NewFromInt(100)

func (Sample) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		percent, err := p.Decimal("chance")
		if err != nil {
			return nil, err
		}

		if percent.IsNegative() || percent.GreaterThan(hundred) {
			return nil, fmt.Errorf("%w %q: %s is not a share from 0 to 100", engine.ErrInvalidParam, "chance", percent)
		}

		field, err := p.String("field")
		if err != nil {
			return nil, err
		}

		v, ok := fields.Lookup(item.JSON, field)
		if !ok || v == nil {
			out.Fail(item, src, fmt.Errorf("field %q is missing, so the item cannot be told apart", field))
			continue
		}

		port := PortRest
		if chance.Taken(rc.WorkflowID, rc.NodeID, fields.Format(v), percent) {
			port = engine.DefaultPort
		}

		out.Add(port, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
	}

	return out, nil
}
