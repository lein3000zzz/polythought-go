// Package workflowcode prints a workflow as workflow code (D64, D400): the TypeScript of
// the editor's Code view, which builds the same graph back when the server runs it. It
// is a port of the app's printer, web/src/features/workflowcode/print.ts, and the
// fixtures in testdata/print hold the two to the same output, character for character.
//
// The app prints what the API gave it: JSON whose objects' keys Go wrote in sorted order,
// and whose numbers JavaScript reads as doubles. So this prints keys sorted, numbers as
// JavaScript writes them, strings as JSON.stringify quotes them, and measures lines in
// UTF-16 code units, as JavaScript's length does.
package workflowcode

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const (
	width       = 100 // past this, a node's settings and options go one per line
	indent      = "  "
	defaultPort = "main"
)

// Type is what the printer needs of a node type: whether it is a trigger, and its
// settings in the order it lists them.
type Type struct {
	Trigger bool
	Params  []string
}

// Types finds a node type by its name.
type Types func(name string) (Type, bool)

// Print is workflow code that builds the workflow, its node types telling triggers from
// the rest.
func Print(name string, nodes []domain.NodeSpec, connections []domain.Connection, types Types) string {
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = string(n.ID)
	}

	names := variableNames(ids)

	lines := []string{
		`import { workflow } from "@polythought/workflow"`,
		"",
		"export default workflow(" + text(name) + ", (w) => {",
	}

	for _, n := range nodes {
		t, known := types(n.Type)
		lines = append(lines, printNode(n, names[string(n.ID)], t, known)...)
	}

	if len(connections) > 0 && len(nodes) > 0 {
		lines = append(lines, "")
	}

	for _, c := range connections {
		from, ok := names[string(c.From)]
		if !ok {
			from = string(c.From)
		}

		to, ok := names[string(c.To)]
		if !ok {
			to = string(c.To)
		}

		source := from
		if c.FromPort != defaultPort {
			source = from + ".port(" + text(c.FromPort) + ")"
		}

		target := to
		if c.ToPort != defaultPort {
			target = to + ".input(" + text(c.ToPort) + ")"
		}

		lines = append(lines, indent+source+".to("+target+")")
	}

	lines = append(lines, "})", "")

	return strings.Join(lines, "\n")
}

type entry struct {
	key   string
	value any
}

func printNode(n domain.NodeSpec, name string, t Type, known bool) []string {
	call := "node"
	if known && t.Trigger {
		call = "trigger"
	}

	settings := ordered(n.Params, t)

	var options []entry
	if n.Name != "" {
		options = append(options, entry{"name", n.Name})
	}

	if n.Notes != "" {
		options = append(options, entry{"notes", n.Notes})
	}

	if n.ContinueOnFail {
		options = append(options, entry{"continueOnFail", true})
	}

	// Settings are there when options are, if only as {}.
	var objects [][]entry

	switch {
	case len(options) > 0:
		objects = [][]entry{settings, options}
	case len(settings) > 0:
		objects = [][]entry{settings}
	}

	head := indent + "const " + name + " = w." + call + "(" + text(string(n.ID)) + ", " + text(n.Type)

	inline := head
	for _, o := range objects {
		inline += ", " + object(o)
	}

	inline += ")"

	if fits(inline) {
		return []string{inline}
	}

	// The settings one per line, then the options after them, inline if they fit.
	out := head + ", "
	if len(settings) == 0 {
		out += "{}"
	} else {
		out += broken(settings)
	}

	if len(options) > 0 {
		inlined := object(options)
		after := out + ", " + inlined + ")"
		last := after[strings.LastIndex(after, "\n")+1:]

		if fits(last) && !strings.Contains(inlined, "\n") {
			out = after
		} else {
			out += ", " + broken(options) + ")"
		}
	} else {
		out += ")"
	}

	return strings.Split(out, "\n")
}

func fits(line string) bool {
	return utf16Len(line) <= width && !strings.Contains(line, "\n")
}

// utf16Len is a string's length as JavaScript measures it.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r >= 0x10000 {
			n++
		}
	}

	return n
}

// ordered is the settings in the order the type lists them, then any others as they are.
func ordered(params map[string]any, t Type) []entry {
	var out []entry

	for _, k := range t.Params {
		if v, ok := params[k]; ok {
			out = append(out, entry{k, v})
		}
	}

	for _, k := range keys(params) {
		if !slices.Contains(t.Params, k) {
			out = append(out, entry{k, params[k]})
		}
	}

	return out
}

// keys are an object's keys in the order JavaScript gives them once it has read Go's
// JSON of it: array indexes first, by number, then the others as Go sorted them.
func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	var indexes, rest []string

	for _, k := range out {
		if arrayIndex(k) {
			indexes = append(indexes, k)
		} else {
			rest = append(rest, k)
		}
	}

	slices.SortFunc(indexes, func(a, b string) int {
		x, _ := strconv.ParseUint(a, 10, 64)
		y, _ := strconv.ParseUint(b, 10, 64)

		return int(x) - int(y) //nolint:gosec // both below 2³²
	})

	return append(indexes, rest...)
}

// arrayIndex says JavaScript orders the key as an array index: an integer from 0 to
// 2³² − 2, written as JavaScript would.
func arrayIndex(k string) bool {
	if k == "0" {
		return true
	}

	if k == "" || k[0] < '1' || k[0] > '9' {
		return false
	}

	n, err := strconv.ParseUint(k, 10, 64)

	return err == nil && n <= math.MaxUint32-1
}

func entries(m map[string]any) []entry {
	ks := keys(m)

	out := make([]entry, len(ks))
	for i, k := range ks {
		out[i] = entry{k, m[k]}
	}

	return out
}

func broken(es []entry) string {
	inner := indent + indent
	lines := make([]string, len(es))

	for i, e := range es {
		lines[i] = inner + key(e.key) + ": " + brokenValue(e.value, inner) + ","
	}

	return "{\n" + strings.Join(lines, "\n") + "\n" + indent + "}"
}

// brokenValue puts a table, a list of rows, a row per line; anything else as it is.
func brokenValue(v any, at string) string {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return value(v)
	}

	for _, r := range list {
		if _, row := r.(map[string]any); !row {
			return value(v)
		}
	}

	lines := make([]string, len(list))
	for i, r := range list {
		lines[i] = at + indent + value(r) + ","
	}

	return "[\n" + strings.Join(lines, "\n") + "\n" + at + "]"
}

func object(es []entry) string {
	if len(es) == 0 {
		return "{}"
	}

	parts := make([]string, len(es))
	for i, e := range es {
		parts[i] = key(e.key) + ": " + value(e.value)
	}

	return "{ " + strings.Join(parts, ", ") + " }"
}

func value(v any) string {
	switch x := v.(type) {
	case string:
		return text(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = value(e)
		}

		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		return object(entries(x))
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return number(x)
	case float32:
		return number(float64(x))
	case int:
		return number(float64(x))
	case int64:
		return number(float64(x))
	case interface{ Float64() (float64, error) }: // json.Number
		f, err := x.Float64()
		if err != nil {
			return "null"
		}

		return number(f)
	}

	return "undefined" // as JSON.stringify leaves what it cannot write
}

// number is a number as JavaScript writes it: the shortest digits that read back the
// same, in plain notation from 10⁻⁶ up to 10²¹, in exponent notation beyond.
func number(f float64) string {
	switch {
	case f == 0:
		return "0" // -0 too
	case math.IsNaN(f), math.IsInf(f, 0):
		return "null"
	}

	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}

	mantissa, exp, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exp)
	k, n := len(digits), e+1

	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}

	out := digits[:1]
	if k > 1 {
		out += "." + digits[1:]
	}

	if n-1 >= 0 {
		return sign + out + "e+" + strconv.Itoa(n-1)
	}

	return sign + out + "e-" + strconv.Itoa(1-n)
}

func key(k string) string {
	// As a plain key, __proto__ would set the object's prototype instead.
	if k == "__proto__" {
		return `["__proto__"]`
	}

	if identifier(k) {
		return k
	}

	return quote(k)
}

// text is a string literal: in double quotes, or for text of several lines, like code, a
// template literal that keeps its lines as they are.
func text(s string) string {
	if !strings.Contains(s, "\n") {
		return quote(s)
	}

	// A carriage return in a template is read as a line feed: escaped, it stays one.
	return "`" + templateEscapes.Replace(s) + "`"
}

var templateEscapes = strings.NewReplacer(`\`, `\\`, "`", "\\`", "${", `\${`, "\r", `\r`)

// quote is a string as JSON.stringify writes it.
func quote(s string) string {
	var b strings.Builder

	b.WriteByte('"')

	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u00`)
				b.WriteString(strconv.FormatInt(int64(r)>>4, 16))
				b.WriteString(strconv.FormatInt(int64(r)&0xf, 16))
			} else {
				b.WriteRune(r)
			}
		}
	}

	b.WriteByte('"')

	return b.String()
}

// identifier says the key or name is a JavaScript identifier, as the app's
// /^[\p{ID_Start}$_][\p{ID_Continue}$‌‍]*$/u has it.
func identifier(s string) bool {
	if s == "" {
		return false
	}

	for i, r := range s {
		switch {
		case r == '$':
		case i == 0 && (r == '_' || idStart(r)):
		case i > 0 && (r == '‌' || r == '‍' || idContinue(r)):
		default:
			return false
		}
	}

	return true
}

func idStart(r rune) bool {
	return unicode.In(r, unicode.L, unicode.Nl, unicode.Other_ID_Start) && !pattern(r)
}

func idContinue(r rune) bool {
	return idStart(r) || unicode.In(r, unicode.Mn, unicode.Mc, unicode.Nd, unicode.Pc, unicode.Other_ID_Continue) && !pattern(r)
}

func pattern(r rune) bool {
	return unicode.In(r, unicode.Pattern_Syntax, unicode.Pattern_White_Space)
}

// Names a variable cannot have, or should not: the words of JavaScript, and the names the
// code itself uses.
var reserved = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`break case catch class const continue debugger default delete do else enum export
		extends false finally for function if import in instanceof new null return super
		switch this throw true try typeof var void while with yield let static implements
		interface package private protected public await arguments eval undefined NaN
		Infinity w workflow`) {
		reserved[w] = true
	}
}

// variableNames gives each node a variable, named after its ID: order-books is
// orderBooks.
func variableNames(ids []string) map[string]string {
	taken := map[string]bool{}
	out := make(map[string]string, len(ids))

	for _, id := range ids {
		words := strings.FieldsFunc(id, func(r rune) bool { return r != '$' && !idContinue(r) })

		base := ""
		for i, w := range words {
			if i == 0 {
				base = w
			} else {
				base += upperFirst(w)
			}
		}

		if !identifier(base) {
			base = "node" + upperFirst(base)
		}

		name := base
		for i := 2; taken[name] || reserved[name]; i++ {
			name = base + strconv.Itoa(i)
		}

		taken[name] = true
		out[id] = name
	}

	return out
}

var upper = cases.Upper(language.Und)

// upperFirst is s with its first character upper case, as JavaScript's
// s[0].toUpperCase() + s.slice(1) makes it: a full mapping (ß is SS), and none for a
// character beyond the Basic Multilingual Plane, whose first half alone s[0] is.
func upperFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if s == "" || r >= 0x10000 {
		return s
	}

	return upper.String(string(r)) + s[size:]
}
