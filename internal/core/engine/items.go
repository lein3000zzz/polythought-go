package engine

import (
	"errors"
	"fmt"
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
	Fields []Field

	// Passed: the node sends on the items it receives, as they are.
	Passed bool

	// Picked names a list param: the node sends on the items it receives with only the
	// fields that param names.
	Picked string
}

// Received reports whether the items are the ones the node receives, whole or picked.
func (it Items) Received() bool {
	return it.Passed || it.Picked != ""
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

	switch {
	case it.Passed && it.Picked != "":
		return fail("passed whole or picked, not both")
	case it.Received() && len(it.Fields) > 0:
		return fail("the items received, or fields of its own, not both")
	case it.Received() && len(typ.Inputs) == 0:
		return fail("a node without inputs receives no items")
	}

	if it.Picked != "" {
		if !hasParam(typ, it.Picked, ParamList) {
			return fail("picked by %q, which is not a list param", it.Picked)
		}
	}

	if err := checkFields(it.Fields, ""); err != nil {
		return fail("%v", err)
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
