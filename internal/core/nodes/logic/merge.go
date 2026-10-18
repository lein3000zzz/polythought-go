package logic

import (
	"context"
	"fmt"
	"maps"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
)

const (
	MergeType = "logic.merge"

	PortA = "a"
	PortB = "b"
)

var _ engine.Node = Merge{}

// Merge combines the items of two branches, A and B, matched on a field of each (D119,
// D133): A's items with a match in B, A's and B's once per key, A's without a match, the
// items of either with no match in the other, or A's with a match and B's first such item
// under a field of their own (D134 to D136). An item without the field matches nothing.
type Merge struct{}

func (Merge) Type() engine.NodeType {
	return engine.NodeType{
		Name:  MergeType,
		Label: "Merge",
		Description: "Combines the items of two branches, A and B, matched on a field: those in both, in either, " +
			"only in A or in only one, or A's with B's item added.",
		Inputs:  []string{PortA, PortB},
		Outputs: []string{engine.DefaultPort},
		Items:   &engine.Items{Merged: "mode", JoinedAs: "joinedAs"},
		Params: []engine.ParamSpec{
			{
				Name: "mode", Label: "Keep", Kind: engine.ParamString, Default: engine.MergeBoth, Options: engine.MergeWays,
				Description: "both: A's items with a match in B (AND). either: A's and B's, once per key (OR). " +
					"onlyA: A's without a match (NOT). onlyOne: those of A or B with no match in the other (XOR). " +
					"join: A's with a match, B's item added.",
			},
			{
				Name: "field", Label: "A's field", Kind: engine.ParamString, Required: true, Field: true, Input: PortA,
				Description: "The field that matches A's items with B's, e.g. tokenId.",
			},
			{
				Name: "fieldB", Label: "B's field", Kind: engine.ParamString, Field: true, Input: PortB,
				Description: "B's field to match with A's, when it is named otherwise. Empty: the same as A's.",
			},
			{
				Name: "joinedAs", Label: "B's item as", Kind: engine.ParamString, Default: "b",
				Description: "For join: the field of A's item that holds B's matching item.",
			},
		},
	}
}

func (Merge) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	// The settings cannot depend on an item: the node looks at all of them at once.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: PortA})
	if err != nil {
		return nil, err
	}

	mode, err := p.String("mode")
	if err != nil {
		return nil, err
	}

	fieldA, err := p.String("field")
	if err != nil {
		return nil, err
	}

	fieldB, _ := p.String("fieldB")
	if fieldB == "" {
		fieldB = fieldA
	}

	type keyed struct {
		item engine.Item
		src  engine.ItemRef
		key  string
		has  bool
	}

	side := func(port, field string) ([]keyed, map[string]int) {
		items := make([]keyed, len(in[port]))
		first := map[string]int{}

		for i, item := range in[port] {
			k := keyed{item: item, src: engine.ItemRef{Port: port, Index: i}}

			if v, ok := fields.Lookup(item.JSON, field); ok && v != nil {
				k.key, k.has = fields.Format(v), true

				if _, seen := first[k.key]; !seen {
					first[k.key] = i
				}
			}

			items[i] = k
		}

		return items, first
	}

	a, inA := side(PortA, fieldA)
	b, inB := side(PortB, fieldB)

	out := engine.Ports{}
	send := func(k keyed) {
		out.Add(engine.DefaultPort, engine.Item{JSON: k.item.JSON, Source: []engine.ItemRef{k.src}})
	}
	matched := func(k keyed, other map[string]int) bool {
		_, ok := other[k.key]
		return k.has && ok
	}

	switch mode {
	case engine.MergeBoth:
		for _, k := range a {
			if matched(k, inB) {
				send(k)
			}
		}
	case engine.MergeOnlyA:
		for _, k := range a {
			if !matched(k, inB) {
				send(k)
			}
		}
	case engine.MergeOnlyOne:
		for _, k := range a {
			if !matched(k, inB) {
				send(k)
			}
		}

		for _, k := range b {
			if !matched(k, inA) {
				send(k)
			}
		}
	case engine.MergeEither:
		seen := map[string]bool{}

		for _, side := range [][]keyed{a, b} {
			for _, k := range side {
				if k.has {
					if seen[k.key] {
						continue
					}

					seen[k.key] = true
				}

				send(k)
			}
		}
	case engine.MergeJoin:
		as, err := p.String("joinedAs")
		if err != nil || as == "" {
			return nil, fmt.Errorf("%w %q: name the field that holds B's item", engine.ErrInvalidParam, "joinedAs")
		}

		for _, k := range a {
			if !matched(k, inB) {
				continue
			}

			match := b[inB[k.key]]
			joined := maps.Clone(k.item.JSON)
			joined[as] = match.item.JSON

			out.Add(engine.DefaultPort, engine.Item{JSON: joined, Source: []engine.ItemRef{k.src, match.src}})
		}
	default:
		return nil, fmt.Errorf("%w %q: unknown way to merge %q", engine.ErrInvalidParam, "mode", mode)
	}

	return out, nil
}
