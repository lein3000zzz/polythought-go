// Package fields reads values out of items for the nodes that test, sort or describe
// them, by paths like "spread" or "outcomes.0.price".
package fields

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Lookup follows a dot-separated path through maps and lists; list steps are indexes.
func Lookup(item map[string]any, path string) (any, bool) {
	var cur any = item

	for step := range strings.SplitSeq(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[step]
			if !ok {
				return nil, false
			}

			cur = next
		case []any:
			i, err := strconv.Atoi(step)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}

			cur = v[i]
		default:
			return nil, false
		}
	}

	return cur, true
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
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case decimal.Decimal:
		return x.String()
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case map[string]any, []any:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}

		return string(b)
	default:
		return fmt.Sprint(x)
	}
}

var placeholder = regexp.MustCompile(`\{\{\s*([^{}\s]+)\s*\}\}`)

// Render replaces each {{path}} in template with the item's value at that path. Paths
// the item does not have become empty.
func Render(template string, item map[string]any) string {
	return placeholder.ReplaceAllStringFunc(template, func(m string) string {
		v, _ := Lookup(item, placeholder.FindStringSubmatch(m)[1])
		return Format(v)
	})
}
