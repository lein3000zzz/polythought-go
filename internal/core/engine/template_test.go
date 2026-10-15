package engine

import (
	"html"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTemplate(t *testing.T) {
	t.Parallel()

	text := func(s string) part { return part{text: s} }
	expr := func(s string) part { return part{expr: s, isExpr: true} }

	tests := []struct {
		in   string
		want []part
	}{
		{"no expressions", nil},
		{"{{question}}", []part{expr("question")}},
		{"Spread: {{ spread * 100 }}%", []part{text("Spread: "), expr("spread * 100"), text("%")}},
		{"{{ a }}{{b}}", []part{expr("a"), expr("b")}},
		{"{{ {x: 1}.x }} and {{ [1, {y: 2}][1].y }}", []part{expr("{x: 1}.x"), text(" and "), expr("[1, {y: 2}][1].y")}},
		{"{{ unclosed", nil},
		{"{{}} and {{ a }}", []part{text("{{}} and "), expr("a")}},
		{"{{  }}", nil},
		{"a {{ b }} c {{ d", []part{text("a "), expr("b"), text(" c {{ d")}},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, parseTemplate(tt.in), tt.in)
	}
}

func TestWholeExpr(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"{{ params.max }}":   "params.max",
		"  {{ limit * 2 }} ": "limit * 2",
	} {
		got, ok := wholeExpr(parseTemplate(in))
		assert.True(t, ok, in)
		assert.Equal(t, want, got)
	}

	for _, in := range []string{"x {{ a }}", "{{ a }}{{ b }}", "{{ a }} %"} {
		_, ok := wholeExpr(parseTemplate(in))
		assert.False(t, ok, in)
	}
}

func TestPlain(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{"question", "outcomes.0.price", "item.spread", "params.max", "execution.id", "$x", "_a.b_c"} {
		assert.True(t, plain(expr), expr)
	}

	for _, expr := range []string{"spread * 100", "outcomes[0].price", "true", "null.x", "f()", "a .b", "1"} {
		assert.False(t, plain(expr), expr)
	}
}

func TestLiteralTemplate(t *testing.T) {
	t.Parallel()

	item := map[string]any{
		"question": "A <b> & C",
		"price":    decimal.RequireFromString("0.42"),
		"outcomes": []any{map[string]any{"label": "Yes"}},
	}

	tmpl, err := Params{"m": "<b>{{question}}</b> at {{ price }}, {{outcomes.0.label}}, {{missing}}, {{ price * 100 }}"}.Template("m")
	require.NoError(t, err)

	assert.Equal(t, "<b>A &lt;b&gt; &amp; C</b> at 0.42, Yes, , {{ price * 100 }}", tmpl.Render(item, html.EscapeString),
		"the template's tags stay, values are escaped, and only plain fields are looked up without the executor")

	plainText, err := Params{"m": "just text"}.Template("m")
	require.NoError(t, err)
	assert.Equal(t, "just text", plainText.Render(item, Same))

	_, err = Params{"m": 3}.Template("m")
	require.ErrorIs(t, err, ErrInvalidParam)
}
