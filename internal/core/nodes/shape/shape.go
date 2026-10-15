// Package shape has shorthands for describing the items nodes emit.
package shape

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func Text(name, description string) engine.Field {
	return engine.Field{Name: name, Kind: engine.FieldText, Description: description}
}

func Number(name, description string) engine.Field {
	return engine.Field{Name: name, Kind: engine.FieldNumber, Description: description}
}

func Decimal(name, description string) engine.Field {
	return engine.Field{Name: name, Kind: engine.FieldDecimal, Description: description}
}

func Bool(name, description string) engine.Field {
	return engine.Field{Name: name, Kind: engine.FieldBool, Description: description}
}

func Time(name, description string) engine.Field {
	return engine.Field{Name: name, Kind: engine.FieldTime, Description: description}
}

// OneOf is text that is always one of options.
func OneOf(name, description string, options ...string) engine.Field {
	return engine.Field{Name: name, Kind: engine.FieldText, Description: description, Options: options}
}

// ListOf is a list of objects with fields.
func ListOf(name, description string, fields ...engine.Field) engine.Field {
	return engine.Field{
		Name: name, Kind: engine.FieldList, Description: description,
		Of: &engine.Field{Kind: engine.FieldObject, Fields: fields},
	}
}

// Nullable marks f as sometimes null.
func Nullable(f engine.Field) engine.Field {
	f.Nullable = true
	return f
}

// Optional marks f as missing from some items.
func Optional(f engine.Field) engine.Field {
	f.Optional = true
	return f
}

// Own describes items with fields of their own.
func Own(fields ...engine.Field) *engine.Items {
	return &engine.Items{Fields: fields}
}

// Passed describes items sent on as they were received.
func Passed() *engine.Items {
	return &engine.Items{Passed: true}
}

// Conforms reports how item differs from fields, if it does: a field it lacks or does not
// declare, or a value of another kind. Values may be typed, as nodes build them, or as
// JSON reads them back: a decimal or a time as text.
func Conforms(fields []engine.Field, item map[string]any) error {
	return conforms(fields, item, "")
}

func conforms(fields []engine.Field, item map[string]any, path string) error {
	declared := make(map[string]bool, len(fields))

	for _, f := range fields {
		declared[f.Name] = true

		v, ok := item[f.Name]
		if !ok {
			if f.Optional {
				continue
			}

			return fmt.Errorf("field %s%s is missing", path, f.Name)
		}

		if err := value(f, v, path+f.Name); err != nil {
			return err
		}
	}

	for _, name := range slices.Sorted(maps.Keys(item)) {
		if !declared[name] {
			return fmt.Errorf("field %s%s is not declared", path, name)
		}
	}

	return nil
}

func value(f engine.Field, v any, at string) error {
	if v == nil {
		if f.Nullable || f.Kind == engine.FieldAny {
			return nil
		}

		return fmt.Errorf("field %s is null", at)
	}

	ok := true

	switch f.Kind {
	case engine.FieldText:
		s, isText := v.(string)
		ok = isText && (len(f.Options) == 0 || slices.Contains(f.Options, s))
	case engine.FieldNumber:
		switch v.(type) {
		case int, int64, float64, json.Number:
		default:
			ok = false
		}
	case engine.FieldDecimal:
		switch d := v.(type) {
		case decimal.Decimal:
		case string:
			_, err := decimal.NewFromString(d)
			ok = err == nil
		default:
			ok = false
		}
	case engine.FieldBool:
		_, ok = v.(bool)
	case engine.FieldTime:
		switch t := v.(type) {
		case time.Time:
		case string:
			_, err := time.Parse(time.RFC3339Nano, t)
			ok = err == nil
		default:
			ok = false
		}
	case engine.FieldList:
		return list(f, v, at)
	case engine.FieldObject:
		m, isObject := v.(map[string]any)
		if !isObject {
			return fmt.Errorf("field %s is %T, not an object", at, v)
		}

		return conforms(f.Fields, m, at+".")
	}

	if !ok {
		return fmt.Errorf("field %s is %T %v, not %s", at, v, v, f.Kind)
	}

	return nil
}

func list(f engine.Field, v any, at string) error {
	var elems []any

	switch l := v.(type) {
	case []any:
		elems = l
	case []map[string]any:
		for _, m := range l {
			elems = append(elems, m)
		}
	default:
		return fmt.Errorf("field %s is %T, not a list", at, v)
	}

	for i, e := range elems {
		if err := value(*f.Of, e, fmt.Sprintf("%s[%d]", at, i)); err != nil {
			return err
		}
	}

	return nil
}

// Check reports items a node of type typ emitted that do not conform to the fields it
// declares, as they are and through JSON. Items received and passed on, and failed ones,
// are not its own and go unchecked.
func Check(typ engine.NodeType, out engine.Ports) error {
	if typ.Items == nil || typ.Items.Received() {
		return nil
	}

	for port, items := range out {
		if port == engine.ErrorPort {
			continue
		}

		for i, it := range items {
			if err := Conforms(typ.Items.Fields, it.JSON); err != nil {
				return fmt.Errorf("%s: %s item %d: %w", typ.Name, port, i, err)
			}

			raw, err := json.Marshal(it.JSON)
			if err != nil {
				return err
			}

			var back map[string]any
			if err := json.Unmarshal(raw, &back); err != nil {
				return err
			}

			if err := Conforms(typ.Items.Fields, back); err != nil {
				return fmt.Errorf("%s: %s item %d, through JSON: %w", typ.Name, port, i, err)
			}
		}
	}

	return nil
}
