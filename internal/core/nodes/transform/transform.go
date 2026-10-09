// Package transform holds nodes that reshape the list of items: sort it, cut it, or
// trim what each item carries.
package transform

import (
	"context"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
)

const (
	TopType  = "transform.top"
	PickType = "transform.pick"
)

var (
	_ engine.Node = Top{}
	_ engine.Node = Pick{}
)

// Top sorts the items by a numeric field and keeps the first few. Items without that
// number go last, in their original order.
type Top struct{}

func (Top) Type() engine.NodeType {
	return engine.NodeType{
		Name:        TopType,
		Label:       "Top items",
		Description: "Sorts the items by a number and keeps the first few, e.g. the five markets with the most liquidity.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{
				Name: "field", Label: "Sort by", Kind: engine.ParamString, Required: true,
				Description: "A numeric field, e.g. liquidity, volume24h or absChangePct.",
			},
			{Name: "order", Label: "Order", Kind: engine.ParamString, Default: "desc", Options: []string{"desc", "asc"}},
			{Name: "limit", Label: "Keep", Kind: engine.ParamNumber, Default: 10, Description: "How many items to keep."},
		},
	}
}

func (Top) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	// The settings cannot depend on an item: the node looks at all of them at once.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	field, err := p.String("field")
	if err != nil {
		return nil, err
	}

	order, err := p.String("order")
	if err != nil {
		return nil, err
	}

	limit, err := p.Int("limit")
	if err != nil {
		return nil, err
	}

	if limit < 1 {
		return nil, fmt.Errorf("%w %q: keep at least one item", engine.ErrInvalidParam, "limit")
	}

	type ranked struct {
		item  engine.Item
		src   engine.ItemRef
		value decimal.Decimal
		has   bool
	}

	items := make([]ranked, len(in[engine.DefaultPort]))
	for i, item := range in[engine.DefaultPort] {
		v, _ := fields.Lookup(item.JSON, field)
		n, ok := fields.Number(v)
		items[i] = ranked{item: item, src: engine.ItemRef{Port: engine.DefaultPort, Index: i}, value: n, has: ok}
	}

	slices.SortStableFunc(items, func(a, b ranked) int {
		switch {
		case a.has != b.has:
			if a.has {
				return -1
			}

			return 1
		case order == "asc":
			return a.value.Cmp(b.value)
		default:
			return b.value.Cmp(a.value)
		}
	})

	out := engine.Ports{}
	for _, r := range items[:min(limit, len(items))] {
		out.Add(engine.DefaultPort, engine.Item{JSON: r.item.JSON, Source: []engine.ItemRef{r.src}})
	}

	return out, nil
}

// Pick keeps only the named fields of each item, to make messages and outputs easier
// to read. A path like outcomes.0.price becomes a field of that name.
type Pick struct{}

func (Pick) Type() engine.NodeType {
	return engine.NodeType{
		Name:        PickType,
		Label:       "Keep fields",
		Description: "Keeps only the fields you name on each item and drops the rest, so outputs and messages stay short.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{
				Name: "fields", Label: "Fields", Kind: engine.ParamList, Required: true,
				Description: "One per line, e.g. question and spread. Paths like outcomes.0.price work too.",
			},
		},
	}
}

func (Pick) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		names, err := p.Strings("fields")
		if err != nil {
			return nil, err
		}

		picked := make(map[string]any, len(names))
		for _, name := range names {
			if v, ok := fields.Lookup(item.JSON, name); ok {
				picked[name] = v
			}
		}

		out.Add(engine.DefaultPort, engine.Item{JSON: picked, Source: []engine.ItemRef{src}})
	}

	return out, nil
}
