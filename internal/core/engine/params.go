package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"time"

	"github.com/shopspring/decimal"
)

var (
	ErrMissingParam = errors.New("missing param")
	ErrInvalidParam = errors.New("invalid param")
)

// literalResolver returns the node's configured params with type defaults filled in.
// It ignores the item: expressions come later.
type literalResolver struct {
	params Params
}

func newLiteralResolver(typ NodeType, configured map[string]any) literalResolver {
	params := make(Params, len(typ.Params)+len(configured))

	for _, spec := range typ.Params {
		if spec.Default != nil {
			params[spec.Name] = spec.Default
		}
	}

	maps.Copy(params, configured)

	return literalResolver{params: params}
}

// LiteralParams resolves a node's configured params, with type defaults, the same way the
// executor does. Useful to run a node outside an execution, such as in tests.
func LiteralParams(typ NodeType, configured map[string]any) ParamResolver {
	return newLiteralResolver(typ, configured)
}

// Resolve returns a copy, so a node changing its params cannot affect the next item.
func (r literalResolver) Resolve(ItemRef) (Params, error) {
	return maps.Clone(r.params), nil
}

// Table reads a table param (D80): its rows, each a Params of its cells, with the
// columns' defaults where a cell is empty. A table not set has no rows.
func (p Params) Table(name string, columns []ParamSpec) ([]Params, error) {
	v, ok := p[name]
	if !ok || v == nil {
		return nil, nil
	}

	var rows []map[string]any

	switch x := v.(type) {
	case []any:
		for _, r := range x {
			row, ok := r.(map[string]any)
			if !ok {
				return nil, invalid(name, v, "a table")
			}

			rows = append(rows, row)
		}
	case []map[string]any:
		rows = x
	case []Params:
		for _, r := range x {
			rows = append(rows, r)
		}
	default:
		return nil, invalid(name, v, "a table")
	}

	out := make([]Params, len(rows))

	for i, row := range rows {
		cells := make(Params, len(columns)+len(row))

		for _, col := range columns {
			if col.Default != nil {
				cells[col.Name] = col.Default
			}
		}

		for k, cell := range row {
			if cell != nil {
				cells[k] = cell
			}
		}

		out[i] = cells
	}

	return out, nil
}

func (p Params) get(name string) (any, error) {
	v, ok := p[name]
	if !ok || v == nil {
		return nil, fmt.Errorf("%w %q", ErrMissingParam, name)
	}

	return v, nil
}

func invalid(name string, v any, want string) error {
	return fmt.Errorf("%w %q: %v (%T) is not %s", ErrInvalidParam, name, v, v, want)
}

func (p Params) String(name string) (string, error) {
	v, err := p.get(name)
	if err != nil {
		return "", err
	}

	s, ok := v.(string)
	if !ok {
		return "", invalid(name, v, "a string")
	}

	return s, nil
}

func (p Params) Bool(name string) (bool, error) {
	v, err := p.get(name)
	if err != nil {
		return false, err
	}

	b, ok := v.(bool)
	if !ok {
		return false, invalid(name, v, "a bool")
	}

	return b, nil
}

// Decimal accepts decimals, numeric strings and every JSON number shape.
func (p Params) Decimal(name string) (decimal.Decimal, error) {
	v, err := p.get(name)
	if err != nil {
		return decimal.Zero, err
	}

	switch n := v.(type) {
	case decimal.Decimal:
		return n, nil
	case string:
		d, err := decimal.NewFromString(n)
		if err != nil {
			return decimal.Zero, invalid(name, v, "a decimal")
		}

		return d, nil
	case json.Number:
		d, err := decimal.NewFromString(n.String())
		if err != nil {
			return decimal.Zero, invalid(name, v, "a decimal")
		}

		return d, nil
	case float64:
		return decimal.NewFromFloat(n), nil
	case int:
		return decimal.NewFromInt(int64(n)), nil
	case int64:
		return decimal.NewFromInt(n), nil
	default:
		return decimal.Zero, invalid(name, v, "a decimal")
	}
}

// Duration reads a Go duration string like "30s" or "1h30m"; it must be positive.
func (p Params) Duration(name string) (time.Duration, error) {
	s, err := p.String(name)
	if err != nil {
		return 0, err
	}

	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, invalid(name, s, "a positive duration like 30s")
	}

	return d, nil
}

// Int accepts integers and whole JSON numbers.
func (p Params) Int(name string) (int, error) {
	v, err := p.get(name)
	if err != nil {
		return 0, err
	}

	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		if n != math.Trunc(n) {
			return 0, invalid(name, v, "a whole number")
		}

		return int(n), nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return 0, invalid(name, v, "a whole number")
		}

		return int(i), nil
	default:
		return 0, invalid(name, v, "a whole number")
	}
}

// Strings accepts a []string or a JSON array of strings.
func (p Params) Strings(name string) ([]string, error) {
	v, err := p.get(name)
	if err != nil {
		return nil, err
	}

	switch list := v.(type) {
	case []string:
		return list, nil
	case []any:
		out := make([]string, len(list))
		for i, e := range list {
			s, ok := e.(string)
			if !ok {
				return nil, invalid(name, v, "a list of strings")
			}

			out[i] = s
		}

		return out, nil
	default:
		return nil, invalid(name, v, "a list of strings")
	}
}
