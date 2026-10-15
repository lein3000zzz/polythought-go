package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Lookup follows a dot-separated path through an item's maps and lists, like spread or
// outcomes.0.price; list steps are indexes.
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

// Format writes a value the way a person would read it in a message: nothing for null,
// decimals as written, times in RFC 3339, lists and objects as JSON.
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
