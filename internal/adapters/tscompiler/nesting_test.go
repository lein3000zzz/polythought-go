package tscompiler

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTooNested(t *testing.T) {
	nest := func(open, close string, n int) string {
		return "export const x = " + strings.Repeat(open, n) + "1" + strings.Repeat(close, n)
	}

	tests := []struct {
		name, src string
		arrows    int
		want      string
	}{
		{"a run of !, the one that overflows esbuild's stack", nest("!", "", 257), maxArrows, "more than 256 of ! ~ + - in a row"},
		{"the most of them", nest("!", "", 256), maxArrows, ""},
		{"spaced out, they still run", nest("- ", "", 300), maxArrows, "in a row"},
		{"operators between operands do not run", "export const x = " + strings.Repeat("1 + ", 5000) + "1", maxArrows, ""},
		{"brackets", nest("(", ")", 257), maxArrows, "nests more than 256 levels deep"},
		{"the deepest allowed", nest("[", "]", 256), maxArrows, ""},
		{"nested functions", nest("function f(){", "}", 300), maxArrows, "nests more than"},
		{"template ${ inside each other", nest("`${", "}`", 300), maxArrows, "nests more than"},
		{"what strings, comments and template text hold does not count",
			"const s = '" + strings.Repeat("(", 400) + "'; // " + strings.Repeat("!", 400) + "\n/* " + strings.Repeat("{", 400) + " */ const t = `" +
				strings.Repeat("[", 400) + "${1}`", maxArrows, ""},
		{"chained arrows", "export const x = " + strings.Repeat("a=>", 101) + "1", maxExprArrs, "more than 100 arrow functions"},
		{"an arrow too many in code", "export const x = " + strings.Repeat("a=>", 1001) + "1", maxArrows, "more than 1000 arrow functions"},
		{"a stray closer is no trouble", "}`${`}}}", maxArrows, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tooNested(tt.src, tt.arrows)
			if tt.want == "" {
				assert.Empty(t, got)
			} else {
				assert.Contains(t, got, tt.want)
			}
		})
	}
}
