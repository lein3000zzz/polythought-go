package engine

import (
	"errors"
	"fmt"
	"slices"
)

// FieldKind is what a field of an item holds, as JSON.
type FieldKind string

const (
	FieldText    FieldKind = "text"
	FieldNumber  FieldKind = "number"  // a JSON number
	FieldDecimal FieldKind = "decimal" // exact, as text like "0.0200"
	FieldBool    FieldKind = "bool"
	FieldTime    FieldKind = "time"   // RFC 3339 text
	FieldList    FieldKind = "list"   // its elements are Of
	FieldObject  FieldKind = "object" // its fields are Fields
	FieldAny     FieldKind = "any"
)

var fieldKinds = []FieldKind{FieldText, FieldNumber, FieldDecimal, FieldBool, FieldTime, FieldList, FieldObject, FieldAny}

// Field describes a field of the items a node emits.
type Field struct {
	Name        string // empty for a list's elements
	Kind        FieldKind
	Description string
	Optional    bool     // some items lack it
	Nullable    bool     // it may be null
	Options     []string // the only values text may have, when it is one of a few
	Of          *Field   // a list's elements
	Fields      []Field  // an object's fields
}

// Items describes the items a node emits, on every output port: fields of its own, or
// the items it received. The error port always carries the items received.
type Items struct {
	// Fields are the items' own; with Passed, those it adds to the items it sends on.
	Fields []Field

	// Passed: the node sends on the items it receives, as they are, but for Fields.
	Passed bool

	// Picked names a list param: the node sends on the items it receives with only the
	// fields that param names.
	Picked string

	// Split names a field param, a list: the node sends an item for each of its entries,
	// with the other fields of the item it came from (D132). An object's fields go at the
	// top, winning a clash; anything else takes the list's place.
	Split string

	// Merged names the text param that says how the node combines the items of its two
	// inputs, one of the Merge ways (D119); JoinedAs, the one naming the field that holds
	// the second input's item in a join.
	Merged   string
	JoinedAs string

	// Named names a text param holding the name of Fields' one field: the node puts what
	// it adds where its settings say, an answer, say (D256).
	Named string

	// Alone is a setting's value under which a node passing items sends one item of its
	// Fields alone instead, for all it received: taking them together, say (D253).
	Alone Choice
}

// Choice is one value of a setting.
type Choice struct {
	Param, Value string
}

// The ways a node with Items.Merged combines its two inputs, A and B, matched on a key:
// A's items with a match in B, A's and B's once per key, A's without a match, the items
// of one with no match in the other, or A's with a match, B's item added (D119).
const (
	MergeBoth    = "both"
	MergeEither  = "either"
	MergeOnlyA   = "onlyA"
	MergeOnlyOne = "onlyOne"
	MergeJoin    = "join"
)

// MergeWays lists them in the order an editor offers them.
var MergeWays = []string{MergeBoth, MergeEither, MergeOnlyA, MergeOnlyOne, MergeJoin}

// Received reports whether the items are the ones the node receives: whole, picked,
// split or merged.
func (it Items) Received() bool {
	return it.Passed || it.Picked != "" || it.Split != "" || it.Merged != ""
}

var errItems = errors.New("items")

// checkItems keeps a type's item description sound: either fields or the items received,
// and fields that describe themselves.
func checkItems(typ NodeType) error {
	it := typ.Items
	if it == nil {
		return nil
	}

	fail := func(format string, args ...any) error {
		return fmt.Errorf("register node type %q: %w: %s", typ.Name, errItems, fmt.Sprintf(format, args...))
	}

	ways := 0
	for _, way := range []bool{it.Passed, it.Picked != "", it.Split != "", it.Merged != ""} {
		if way {
			ways++
		}
	}

	switch {
	case ways > 1:
		return fail("passed whole, picked, split or merged: one of them")
	case (it.Picked != "" || it.Split != "" || it.Merged != "") && len(it.Fields) > 0:
		return fail("picked, split or merged, or fields of its own, not both")
	case it.Received() && len(typ.Inputs) == 0:
		return fail("a node without inputs receives no items")
	}

	if it.Picked != "" {
		if !hasParam(typ, it.Picked, ParamList) {
			return fail("picked by %q, which is not a list param", it.Picked)
		}
	}

	if it.Split != "" {
		if !slices.ContainsFunc(typ.Params, func(p ParamSpec) bool { return p.Name == it.Split && p.Field && p.Kind == ParamString }) {
			return fail("split by %q, which is not a field param", it.Split)
		}
	}

	if it.Merged != "" {
		switch {
		case len(typ.Inputs) != 2:
			return fail("merged from %d inputs, not two", len(typ.Inputs))
		case !hasParam(typ, it.Merged, ParamString) || !hasParam(typ, it.JoinedAs, ParamString):
			return fail("merged by %q and joined as %q, which are not both text params", it.Merged, it.JoinedAs)
		}
	}

	if it.Named != "" {
		switch {
		case len(it.Fields) != 1:
			return fail("named by %q, with %d fields, not one", it.Named, len(it.Fields))
		case !hasParam(typ, it.Named, ParamString):
			return fail("named by %q, which is not a text param", it.Named)
		}
	}

	if it.Alone.Param != "" {
		i := slices.IndexFunc(typ.Params, func(p ParamSpec) bool { return p.Name == it.Alone.Param })

		switch {
		case !it.Passed:
			return fail("alone under %q, but not passing the items it receives", it.Alone.Param)
		case i < 0 || !slices.Contains(typ.Params[i].Options, it.Alone.Value):
			return fail("alone when %q is %q, which is not one of its options", it.Alone.Param, it.Alone.Value)
		}
	}

	if err := checkFields(it.Fields, ""); err != nil {
		return fail("%v", err)
	}

	return nil
}

// checkFieldParams keeps field params to text and lists of text, of real kinds.
func checkFieldParams(typ NodeType) error {
	for _, p := range typ.Params {
		switch {
		case !p.Field && len(p.FieldKinds) > 0:
			return fmt.Errorf("register node type %q: param %q has field kinds but names no field", typ.Name, p.Name)
		case p.Field && p.Kind != ParamString && p.Kind != ParamList:
			return fmt.Errorf("register node type %q: param %q names a field, so it is text or a list, not %s", typ.Name, p.Name, p.Kind)
		case p.Input != "" && (!p.Field || !slices.Contains(typ.Inputs, p.Input)):
			return fmt.Errorf("register node type %q: param %q names a field of input %q, which it does not have", typ.Name, p.Name, p.Input)
		}

		for _, k := range p.FieldKinds {
			if !slices.Contains(fieldKinds, k) {
				return fmt.Errorf("register node type %q: param %q: no field kind %q", typ.Name, p.Name, k)
			}
		}
	}

	return nil
}

func hasParam(typ NodeType, name string, kind ParamKind) bool {
	for _, p := range typ.Params {
		if p.Name == name && p.Kind == kind {
			return true
		}
	}

	return false
}

// CheckFields says what is wrong with declared fields, as registering a type checks them.
func CheckFields(fields []Field) error {
	return checkFields(fields, "")
}

func checkFields(fields []Field, path string) error {
	seen := make(map[string]bool, len(fields))

	for _, f := range fields {
		at := path + f.Name

		switch {
		case f.Name == "":
			return fmt.Errorf("a field in %q has no name", path)
		case seen[f.Name]:
			return fmt.Errorf("field %q twice", at)
		}

		seen[f.Name] = true

		if err := checkField(f, at); err != nil {
			return err
		}
	}

	return nil
}

func checkField(f Field, at string) error {
	switch f.Kind {
	case FieldText, FieldNumber, FieldDecimal, FieldBool, FieldTime, FieldAny:
		if f.Of != nil || len(f.Fields) > 0 {
			return fmt.Errorf("field %q is %s, with no elements or fields", at, f.Kind)
		}
	case FieldList:
		if f.Of == nil || f.Of.Name != "" || len(f.Fields) > 0 {
			return fmt.Errorf("list %q needs its elements, unnamed, and no fields", at)
		}

		return checkField(*f.Of, at+"[]")
	case FieldObject:
		if f.Of != nil || len(f.Fields) == 0 {
			return fmt.Errorf("object %q needs fields, and no elements", at)
		}

		return checkFields(f.Fields, at+".")
	default:
		return fmt.Errorf("field %q has no kind %q", at, f.Kind)
	}

	if len(f.Options) > 0 && f.Kind != FieldText {
		return fmt.Errorf("field %q has options but is not text", at)
	}

	return nil
}
