package logic

import (
	"context"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const FilterType = "logic.filter"

var _ engine.Node = Filter{}

// filterOperators lists the tests in the order an editor offers them. The first six
// compare numbers, like If's.
var filterOperators = []string{"gt", "gte", "lt", "lte", "eq", "neq", "contains", "notContains", "exists", "missing"}

// Filter keeps the items whose field passes a test and drops the rest. An item whose
// field is not a number fails a numeric test onto the error port, as with If.
type Filter struct{}

func (Filter) Type() engine.NodeType {
	return engine.NodeType{
		Name:  FilterType,
		Label: "Filter",
		Description: "Keeps the items whose field passes a test and drops the others: a number " +
			"compared with a value, text that contains a value, or a field being there at all.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Passed(),
		Params: []engine.ParamSpec{
			{
				Name: "field", Label: "Field", Kind: engine.ParamString, Required: true,
				Description: "The item field to test, e.g. spread, or outcomes.0.price for the first outcome's price.",
			},
			{Name: "operator", Label: "Test", Kind: engine.ParamString, Default: "gt", Options: filterOperators},
			{
				Name: "value", Label: "Value", Kind: engine.ParamString,
				Description: "A number for the comparisons, text for contains (any case). Not used by exists and missing.",
			},
		},
	}
}

func (Filter) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		keep, itemErr, err := filterTest(rc, src, item)
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

// filterTest reports whether item passes. The first error is the item's; the second,
// for bad configuration, fails the node.
func filterTest(rc engine.RunContext, src engine.ItemRef, item engine.Item) (bool, error, error) {
	p, err := rc.Params.Resolve(src)
	if err != nil {
		return false, nil, err
	}

	field, err := p.String("field")
	if err != nil {
		return false, nil, err
	}

	op, err := p.String("operator")
	if err != nil {
		return false, nil, err
	}

	value, _ := p.String("value") // optional for exists and missing
	got, present := fields.Lookup(item.JSON, field)
	present = present && got != nil

	switch op {
	case "exists":
		return present, nil, nil
	case "missing":
		return !present, nil, nil
	case "contains", "notContains":
		has := present && strings.Contains(strings.ToLower(fields.Format(got)), strings.ToLower(value))
		return has == (op == "contains"), nil, nil
	}

	satisfies, known := operators[op]
	if !known {
		return false, nil, fmt.Errorf("%w %q: unknown test %q", engine.ErrInvalidParam, "operator", op)
	}

	want, ok := fields.Number(value)
	if !ok {
		return false, nil, fmt.Errorf("%w %q: %q is not a number, which %s compares with", engine.ErrInvalidParam, "value", value, op)
	}

	if !present {
		return false, nil, nil // nothing to compare, like a one-sided book's spread
	}

	n, ok := fields.Number(got)
	if !ok {
		return false, fmt.Errorf("field %q is %q, not a number", field, fields.Format(got)), nil
	}

	return satisfies(n.Cmp(want)), nil, nil
}
