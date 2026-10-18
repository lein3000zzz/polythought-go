package transform

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
)

const SplitOutType = "transform.splitOut"

var _ engine.Node = SplitOut{}

// SplitOut makes an item of each entry of a list field, with the other fields of the
// item it came from (D132): an object's fields at the top, winning a clash; anything
// else in the list's place. An item with an empty list, or none, gives no items.
type SplitOut struct{}

func (SplitOut) Type() engine.NodeType {
	return engine.NodeType{
		Name:  SplitOutType,
		Label: "Split out",
		Description: "Makes an item of each entry of a list field, like a market's outcomes, " +
			"with the other fields of the item it came from.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   &engine.Items{Split: "field"},
		Params: []engine.ParamSpec{{
			Name: "field", Label: "List", Kind: engine.ParamString, Required: true,
			Field: true, FieldKinds: []engine.FieldKind{engine.FieldList},
			Description: "The list to split, e.g. outcomes. An entry's own fields go at the top, beside the item's others.",
		}},
	}
}

func (SplitOut) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	// A field setting takes no expressions: the same for every item.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	path, err := p.String("field")
	if err != nil {
		return nil, err
	}

	steps := strings.Split(path, ".")

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		v, ok := fields.Lookup(item.JSON, path)
		if !ok || v == nil {
			continue // nothing to split
		}

		entries, ok := listOf(v)
		if !ok {
			out.Fail(item, src, fmt.Errorf("field %q is %q, not a list", path, fields.Format(v)))
			continue
		}

		for _, e := range entries {
			out.Add(engine.DefaultPort, engine.Item{JSON: entryItem(item.JSON, steps, e), Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

func listOf(v any) ([]any, bool) {
	switch l := v.(type) {
	case []any:
		return l, true
	case []string:
		out := make([]any, len(l))
		for i, s := range l {
			out[i] = s
		}

		return out, true
	case []map[string]any:
		out := make([]any, len(l))
		for i, m := range l {
			out[i] = m
		}

		return out, true
	}

	return nil, false
}

// entryItem is the item made of one entry of the list at steps.
func entryItem(item map[string]any, steps []string, entry any) map[string]any {
	obj, ok := entry.(map[string]any)
	if !ok {
		return replaced(item, steps, entry, true)
	}

	out := replaced(item, steps, nil, false)
	maps.Copy(out, obj)

	return out
}

// replaced is a copy of m with the value at steps, through objects, set to v, or left
// out when not set. Through a list, the list stays as it is.
func replaced(m map[string]any, steps []string, v any, set bool) map[string]any {
	out := maps.Clone(m)

	if len(steps) == 1 {
		if set {
			out[steps[0]] = v
		} else {
			delete(out, steps[0])
		}

		return out
	}

	if next, ok := m[steps[0]].(map[string]any); ok {
		out[steps[0]] = replaced(next, steps[1:], v, set)
	}

	return out
}
