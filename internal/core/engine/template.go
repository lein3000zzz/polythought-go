package engine

import (
	"strings"
)

// A setting may hold {{ ... }}: text around expressions, worked out for each item. See
// expr.go for what an expression sees and how it is worked out.

// part is a piece of a setting's text: text as written, or an expression from {{ }}.
type part struct {
	text   string
	expr   string // trimmed
	isExpr bool
}

// parseTemplate splits s into its text and {{ }} parts; nil when it has no expression.
// An expression ends at the first }} after which its own braces balance, so an object
// literal works inside. {{ with no end, and {{}}, are text.
func parseTemplate(s string) []part {
	if !strings.Contains(s, "{{") {
		return nil
	}

	var parts []part

	text := func(t string) {
		if t == "" {
			return
		}

		if n := len(parts); n > 0 && !parts[n-1].isExpr {
			parts[n-1].text += t
			return
		}

		parts = append(parts, part{text: t})
	}

	rest := s

	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			break
		}

		end := closing(rest[start+2:])
		if end < 0 {
			break
		}

		text(rest[:start])

		if expr := strings.TrimSpace(rest[start+2 : start+2+end]); expr != "" {
			parts = append(parts, part{expr: expr, isExpr: true})
		} else {
			text(rest[start : start+4+end])
		}

		rest = rest[start+4+end:]
	}

	text(rest)

	for _, p := range parts {
		if p.isExpr {
			return parts
		}
	}

	return nil
}

// closing finds the }} that ends an expression: the first where its { and } balance.
func closing(s string) int {
	depth := 0

	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			if depth == 0 && i+1 < len(s) && s[i+1] == '}' {
				return i
			}

			if depth > 0 {
				depth--
			}
		}
	}

	return -1
}

// wholeExpr is the expression when the parts are exactly one {{ }}, spaces around it
// aside: a setting whose value is the expression's, of its own kind.
func wholeExpr(parts []part) (string, bool) {
	expr, found := "", false

	for _, p := range parts {
		switch {
		case p.isExpr && !found:
			expr, found = p.expr, true
		case p.isExpr || strings.TrimSpace(p.text) != "":
			return "", false
		}
	}

	return expr, found
}

// HasExpressions reports whether a setting's value holds {{ }}, itself or in a list.
func HasExpressions(v any) bool {
	switch x := v.(type) {
	case string:
		return parseTemplate(x) != nil
	case []any:
		for _, e := range x {
			if HasExpressions(e) {
				return true
			}
		}
	}

	return false
}

// Template is a text setting the node fills in itself, per item, so it can escape the
// values for where the text goes, such as HTML. The executor works its expressions out
// for the item; a template read without it, as in tests, looks plain fields up in the
// item it renders and leaves other expressions as written.
type Template struct {
	parts []templatePart
}

type templatePart struct {
	text   string
	path   string // a plain field to look up in the item rendered
	value  any    // an expression's value, worked out for the item
	filled bool   // value is set
}

// literalTemplate reads text as a template, without working anything out.
func literalTemplate(s string) Template {
	parts := parseTemplate(s)
	if parts == nil {
		return Template{parts: []templatePart{{text: s}}}
	}

	out := make([]templatePart, len(parts))

	for i, p := range parts {
		switch {
		case !p.isExpr:
			out[i] = templatePart{text: p.text}
		case plain(p.expr):
			out[i] = templatePart{path: p.expr}
		default:
			out[i] = templatePart{text: "{{ " + p.expr + " }}"}
		}
	}

	return Template{parts: out}
}

// Render fills the template in for item, each value passed through escape.
func (t Template) Render(item map[string]any, escape func(string) string) string {
	var b strings.Builder

	for _, p := range t.parts {
		switch {
		case p.filled:
			b.WriteString(escape(Format(p.value)))
		case p.path != "":
			b.WriteString(escape(Format(scope{item: item}.lookup(p.path))))
		default:
			b.WriteString(p.text)
		}
	}

	return b.String()
}

// Template reads a text setting the node fills in itself: what the executor worked out
// for the item, or the text as it is.
func (p Params) Template(name string) (Template, error) {
	v, err := p.get(name)
	if err != nil {
		return Template{}, err
	}

	switch x := v.(type) {
	case Template:
		return x, nil
	case string:
		return literalTemplate(x), nil
	default:
		return Template{}, invalid(name, v, "text")
	}
}

// RenderEach fills in the template setting name for each item, with the settings worked
// out for that item, each value passed through escape.
func RenderEach(rc RunContext, name string, items []Item, refs []ItemRef, escape func(string) string) ([]string, error) {
	out := make([]string, len(items))

	for i, it := range items {
		p, err := rc.Params.Resolve(refs[i])
		if err != nil {
			return nil, err
		}

		t, err := p.Template(name)
		if err != nil {
			return nil, err
		}

		out[i] = t.Render(it.JSON, escape)
	}

	return out, nil
}

// Same is an escape that changes nothing.
func Same(s string) string { return s }
