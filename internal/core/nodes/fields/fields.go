// Package fields reads values out of items for the nodes that test, sort or describe
// them, by paths like "spread" or "outcomes.0.price".
package fields

import (
	"encoding/json"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// Lookup follows a dot-separated path through maps and lists; list steps are indexes.
func Lookup(item map[string]any, path string) (any, bool) {
	return engine.Lookup(item, path)
}

// Number reads a value as a decimal: decimals, JSON numbers, Go numbers and numeric
// strings all count.
func Number(v any) (decimal.Decimal, bool) {
	switch n := v.(type) {
	case decimal.Decimal:
		return n, true
	case json.Number:
		d, err := decimal.NewFromString(n.String())
		return d, err == nil
	case string:
		d, err := decimal.NewFromString(strings.TrimSpace(n))
		return d, err == nil
	case float64:
		return decimal.NewFromFloat(n), true
	case int:
		return decimal.NewFromInt(int64(n)), true
	case int64:
		return decimal.NewFromInt(n), true
	default:
		return decimal.Zero, false
	}
}

// Format writes a value the way a person would read it in a message.
func Format(v any) string {
	return engine.Format(v)
}

// Render replaces each {{path}} in template with the item's value at that path. Paths
// the item does not have become empty.
func Render(template string, item map[string]any) string {
	return RenderEscaped(template, item, engine.Same)
}

// RenderEscaped is Render with every value passed through escape, so values cannot
// change the markup the template is written in, such as HTML.
func RenderEscaped(template string, item map[string]any, escape func(string) string) string {
	t, _ := engine.Params{"t": template}.Template("t")
	return t.Render(item, escape)
}
