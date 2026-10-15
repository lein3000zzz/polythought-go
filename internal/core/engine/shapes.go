package engine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Shape is what the items at some point of a workflow look like: their fields. Nil means
// nobody knows, as after code whose author did not say.
type Shape []Field

// Received works out, for each node, the shape of the items its main input receives,
// following the items from the triggers: nodes say what they emit, pass on what they
// receive, or pick from it. Where several connections meet, a field only some of them
// carry is optional, and one unknown makes the whole unknown. Nodes nothing reaches, and
// workflows that do not order, are unknown.
func (r *Registry) Received(w domain.Workflow) map[domain.NodeID]Shape {
	order, err := w.TopologicalOrder()
	if err != nil {
		return nil
	}

	received := make(map[domain.NodeID]Shape, len(order))
	emitted := make(map[domain.NodeID]Shape, len(order))

	for _, id := range order {
		spec, _ := w.Node(id)

		var from []Shape

		for _, c := range w.Incoming(id) {
			if c.ToPort != DefaultPort {
				continue
			}

			if c.FromPort == ErrorPort {
				from = append(from, received[c.From]) // failed items are the ones received
			} else {
				from = append(from, emitted[c.From])
			}
		}

		received[id] = merge(from)

		if typ, _, ok := r.Type(spec.Type); ok {
			emitted[id] = emits(typ, spec, received[id])
		}
	}

	return received
}

func emits(typ NodeType, spec domain.NodeSpec, in Shape) Shape {
	switch it := typ.Items; {
	case it == nil:
		return nil
	case it.Passed:
		return in
	case it.Picked != "":
		names, err := Params(spec.Params).Strings(it.Picked)
		if err != nil || in == nil {
			return nil
		}

		return pick(in, names)
	default:
		return append(Shape{}, it.Fields...) // known, even with no fields
	}
}

// pick is the fields at paths, named by them, as transform.pick keeps them.
func pick(in Shape, paths []string) Shape {
	out := make(Shape, 0, len(paths))

	for _, p := range paths {
		f, ok := in.Lookup(p)
		if !ok {
			f = Field{Kind: FieldAny}
		}

		f.Name = p
		// An item without it has nothing to keep.
		f.Optional = !ok || isOptionalAt(in, p)

		out = append(out, f)
	}

	return out
}

// isOptionalAt reports whether some step on the way to path may be missing or null.
func isOptionalAt(in Shape, path string) bool {
	steps := strings.Split(path, ".")

	for i := range steps {
		f, _ := in.Lookup(strings.Join(steps[:i+1], "."))
		if f.Optional || f.Nullable || f.Kind == FieldList {
			return true // a list may be shorter than the index
		}
	}

	return false
}

func merge(shapes []Shape) Shape {
	if len(shapes) == 0 || slices.ContainsFunc(shapes, func(s Shape) bool { return s == nil }) {
		return nil
	}

	out := slices.Clone(shapes[0])

	for _, s := range shapes[1:] {
		for i, f := range out {
			other, ok := s.field(f.Name)
			switch {
			case !ok:
				out[i].Optional = true
			case other.Kind != f.Kind:
				out[i] = Field{Name: f.Name, Kind: FieldAny, Optional: f.Optional || other.Optional}
			default:
				out[i].Optional = f.Optional || other.Optional
				out[i].Nullable = f.Nullable || other.Nullable
			}
		}

		for _, f := range s {
			if _, ok := Shape(out).field(f.Name); !ok {
				f.Optional = true
				out = append(out, f)
			}
		}
	}

	return out
}

func (s Shape) field(name string) (Field, bool) {
	for _, f := range s {
		if f.Name == name {
			return f, true
		}
	}

	return Field{}, false
}

// Lookup finds the field at a path like spread or outcomes.0.price, as nodes read them:
// names through objects, indexes through lists. Anything under a field of any kind may be
// there.
func (s Shape) Lookup(path string) (Field, bool) {
	steps := strings.Split(path, ".")
	cur := Field{Kind: FieldObject, Fields: s}

	for _, step := range steps {
		switch cur.Kind {
		case FieldAny:
			return Field{Kind: FieldAny}, true
		case FieldObject:
			f, ok := Shape(cur.Fields).field(step)
			if !ok {
				return Field{}, false
			}

			cur = f
		case FieldList:
			if _, err := strconv.Atoi(step); err != nil || cur.Of == nil {
				return Field{}, false
			}

			cur = *cur.Of
		default:
			return Field{}, false
		}
	}

	return cur, true
}

// Paths lists every path into the shape a field param could name, the way nodes read
// them: nested fields with dots, and a list's elements by index, the first standing for
// all.
func (s Shape) Paths() []string {
	var out []string

	var walk func(fields []Field, prefix string)
	walk = func(fields []Field, prefix string) {
		for _, f := range fields {
			path := prefix + f.Name
			out = append(out, path)

			switch {
			case f.Kind == FieldObject:
				walk(f.Fields, path+".")
			case f.Kind == FieldList && f.Of != nil && f.Of.Kind == FieldObject:
				walk(f.Of.Fields, path+".0.")
			}
		}
	}

	walk(s, "")

	return out
}

// CheckFields reports field params that name a field the items they receive do not have,
// or one of a kind the param does not take. Where nobody knows the items, nothing is
// checked. Saving a workflow asks this; running one does not, so a workflow saved before
// keeps running.
func (r *Registry) CheckFields(w domain.Workflow) error {
	received := r.Received(w)

	var v domain.ValidationError

	for _, n := range w.Nodes {
		typ, _, ok := r.Type(n.Type)
		in := received[n.ID]

		if !ok || in == nil {
			continue
		}

		for _, spec := range typ.Params {
			value := n.Params[spec.Name]
			if value == nil {
				value = spec.Default // what runs when it is not set
			}

			if !spec.Field || value == nil {
				continue
			}

			for _, path := range fieldPaths(spec, value) {
				if msg := checkFieldPath(spec, in, path); msg != "" {
					v.Add(domain.Problem{
						NodeID: n.ID, Param: spec.Name, Err: ErrInvalidParam,
						Message: fmt.Sprintf("node %q: param %q: %s", n.ID, spec.Name, msg),
					})
				}
			}
		}
	}

	return v.Err()
}

func fieldPaths(spec ParamSpec, value any) []string {
	p := Params{spec.Name: value}

	if spec.Kind == ParamList {
		list, _ := p.Strings(spec.Name)
		return list
	}

	s, err := p.String(spec.Name)
	if err != nil || s == "" {
		return nil
	}

	return []string{s}
}

func checkFieldPath(spec ParamSpec, in Shape, path string) string {
	f, ok := in.Lookup(path)
	if !ok {
		msg := fmt.Sprintf("the items it receives have no field %q", path)
		if near := closest(path, in.Paths()); near != "" {
			msg += fmt.Sprintf("; %q?", near)
		}

		return msg
	}

	if len(spec.FieldKinds) > 0 && f.Kind != FieldAny && !slices.Contains(spec.FieldKinds, f.Kind) {
		kinds := make([]string, len(spec.FieldKinds))
		for i, k := range spec.FieldKinds {
			kinds[i] = string(k)
		}

		return fmt.Sprintf("field %q is %s, and this takes %s", path, f.Kind, strings.Join(kinds, " or "))
	}

	return ""
}

// closest is the path most like path, differing in letter case or by a couple of
// letters; empty when none is.
func closest(path string, paths []string) string {
	best, bestDist := "", 3

	for _, p := range paths {
		if strings.EqualFold(p, path) {
			return p
		}

		if d := distance(strings.ToLower(p), strings.ToLower(path)); d < bestDist {
			best, bestDist = p, d
		}
	}

	return best
}

// distance is the Levenshtein distance between a and b, by bytes.
func distance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i

		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}

		prev = cur
	}

	return prev[len(b)]
}
