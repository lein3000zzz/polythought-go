package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Expressions (D38 to D40). Inside {{ }}, bare names are the item's fields, and item,
// params and execution are the item, the node's settings as configured and the run. A
// plain path, like question or outcomes.0.price, is looked up here, a field the item
// lacks being nothing. Anything else is TypeScript, worked out in the sandbox by an
// Evaluator for all of a node run's items at once, the first time an item needs one.
//
// A setting whose value is exactly one {{ }} takes the expression's value, checked as the
// setting's kind; text around expressions makes text. Settings a node renders itself
// (ParamSpec.Template) come as a Template, filled in by the node so it can escape them.
// Triggers, wallets, credentials, field settings and the setting that gives ports take
// none: they hold what they say.

// Evaluator works expressions out in the sandbox, every expression against every item.
type Evaluator interface {
	Evaluate(ctx context.Context, e Evaluation) ([][]EvalResult, error)
}

// Evaluation is what an Evaluator works out: result [i][j] is expression j for item i.
type Evaluation struct {
	Expressions []string
	Items       []map[string]any
	Params      map[string]any
	Execution   map[string]any // id, workflowId, startedAt
}

// EvalResult is one expression's value for one item, or what it threw.
type EvalResult struct {
	Value any
	Err   string
}

// WithEvaluator lets settings hold expressions beyond plain fields. Without it, those
// fail the node.
func WithEvaluator(ev Evaluator) ExecutorOption {
	return func(e *Executor) { e.evaluator = ev }
}

var errNoEvaluator = errors.New("expressions beyond plain fields need the code runtime, which is not running")

var plainPath = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z0-9_$]+)*$`)

// notFields are names a plain path cannot start with: they mean something in JavaScript.
var notFields = map[string]bool{"true": true, "false": true, "null": true, "undefined": true, "NaN": true, "Infinity": true, "this": true}

// plain reports whether expr is a path looked up here rather than in the sandbox.
func plain(expr string) bool {
	first, _, _ := strings.Cut(expr, ".")
	return plainPath.MatchString(expr) && !notFields[first]
}

// scope is what an expression sees.
type scope struct {
	item, params, execution map[string]any
}

func (s scope) lookup(path string) any {
	first, rest, nested := strings.Cut(path, ".")

	var base map[string]any

	switch first {
	case "item":
		base = s.item
	case "params":
		base = s.params
	case "execution":
		base = s.execution
	default:
		v, _ := Lookup(s.item, path)
		return v
	}

	if !nested {
		return base
	}

	v, _ := Lookup(base, rest)

	return v
}

// TakesExpressions reports whether a setting of a node, not a trigger, may hold
// expressions: all but wallets, credentials, field settings and the one that gives ports.
func TakesExpressions(typ NodeType, spec ParamSpec) bool {
	return takesExpressions(typ, spec)
}

func takesExpressions(typ NodeType, spec ParamSpec) bool {
	switch spec.Kind {
	case ParamWallet, ParamCredential, ParamCode: // code's own {{ }} is code
		return false
	default:
		return !spec.Field && spec.Name != typ.OutputsParam
	}
}

// exprResolver works out a node's settings for each item.
type exprResolver struct {
	ctx       context.Context
	typ       NodeType
	params    Params // as configured, with defaults
	in        Ports
	execution map[string]any
	eval      Evaluator

	sandbox []string // the expressions it needs, by column
	columns map[string]int

	once    sync.Once
	rows    map[ItemRef][]EvalResult
	none    []EvalResult // for a setting asked without an item
	evalErr error
}

// resolverFor is a node run's resolver: the literal one when no setting holds {{ }}.
func (e *Executor) resolverFor(ctx context.Context, typ NodeType, configured map[string]any, in Ports, execution map[string]any) ParamResolver {
	lit := newLiteralResolver(typ, configured)
	r := &exprResolver{ctx: ctx, typ: typ, params: lit.params, in: in, execution: execution, eval: e.evaluator, columns: map[string]int{}}
	found := false

	for _, spec := range typ.Params {
		if !takesExpressions(typ, spec) || !HasExpressions(lit.params[spec.Name]) {
			continue
		}

		found = true

		for _, expr := range expressions(lit.params[spec.Name]) {
			if _, ok := r.columns[expr]; !ok && !plain(expr) {
				r.columns[expr] = len(r.sandbox)
				r.sandbox = append(r.sandbox, expr)
			}
		}
	}

	if !found {
		return lit
	}

	return r
}

func expressions(v any) []string {
	var out []string

	switch x := v.(type) {
	case string:
		for _, p := range parseTemplate(x) {
			if p.isExpr {
				out = append(out, p.expr)
			}
		}
	case []any:
		for _, e := range x {
			out = append(out, expressions(e)...)
		}
	}

	return out
}

func (r *exprResolver) Resolve(src ItemRef) (Params, error) {
	out := maps.Clone(r.params)

	item := map[string]any{}
	if items := r.in[src.Port]; src.Index >= 0 && src.Index < len(items) && items[src.Index].JSON != nil {
		item = items[src.Index].JSON
	}

	sc := scope{item: item, params: r.params, execution: r.execution}

	for _, spec := range r.typ.Params {
		v := out[spec.Name]
		if !takesExpressions(r.typ, spec) || !HasExpressions(v) {
			continue
		}

		resolved, err := r.resolve(spec, v, src, sc)
		if err != nil {
			return nil, fmt.Errorf("param %q: %w", spec.Name, err)
		}

		switch {
		case resolved != nil:
			out[spec.Name] = resolved
		case spec.Default != nil:
			out[spec.Name] = spec.Default // the expression gave nothing
		default:
			delete(out, spec.Name)
		}
	}

	return out, nil
}

func (r *exprResolver) resolve(spec ParamSpec, v any, src ItemRef, sc scope) (any, error) {
	switch x := v.(type) {
	case string:
		parts := parseTemplate(x)

		if spec.Template {
			return r.template(parts, src, sc)
		}

		if expr, ok := wholeExpr(parts); ok {
			val, err := r.value(expr, src, sc)
			if err != nil || val == nil {
				return nil, err
			}

			return typed(spec, val, "{{ "+expr+" }} gave")
		}

		text, err := r.render(parts, src, sc)
		if err != nil {
			return nil, err
		}

		return typed(spec, text, "the text")
	case []any:
		out := make([]any, len(x))

		for i, e := range x {
			s, ok := e.(string)
			if !ok || !HasExpressions(s) {
				out[i] = e
				continue
			}

			text, err := r.render(parseTemplate(s), src, sc)
			if err != nil {
				return nil, err
			}

			out[i] = text
		}

		return out, nil
	default:
		return v, nil
	}
}

func (r *exprResolver) template(parts []part, src ItemRef, sc scope) (Template, error) {
	out := make([]templatePart, len(parts))

	for i, p := range parts {
		if !p.isExpr {
			out[i] = templatePart{text: p.text}
			continue
		}

		val, err := r.value(p.expr, src, sc)
		if err != nil {
			return Template{}, err
		}

		out[i] = templatePart{value: val, filled: true}
	}

	return Template{parts: out}, nil
}

func (r *exprResolver) render(parts []part, src ItemRef, sc scope) (string, error) {
	var b strings.Builder

	for _, p := range parts {
		if !p.isExpr {
			b.WriteString(p.text)
			continue
		}

		val, err := r.value(p.expr, src, sc)
		if err != nil {
			return "", err
		}

		b.WriteString(Format(val))
	}

	return b.String(), nil
}

// value works one expression out for the item at src.
func (r *exprResolver) value(expr string, src ItemRef, sc scope) (any, error) {
	if plain(expr) {
		return sc.lookup(expr), nil
	}

	r.once.Do(r.evaluate)

	if r.evalErr != nil {
		return nil, r.evalErr
	}

	row, ok := r.rows[src]
	if !ok {
		row = r.none
	}

	res := row[r.columns[expr]]
	if res.Err != "" {
		return nil, fmt.Errorf("{{ %s }}: %s", expr, res.Err)
	}

	return res.Value, nil
}

// evaluate works the sandbox's expressions out for every item the node received, and
// for none, in one go.
func (r *exprResolver) evaluate() {
	if r.eval == nil {
		r.evalErr = errNoEvaluator
		return
	}

	var (
		refs  []ItemRef
		items []map[string]any
	)

	for _, port := range slices.Sorted(maps.Keys(r.in)) {
		for i, it := range r.in[port] {
			refs = append(refs, ItemRef{Port: port, Index: i})

			j := it.JSON
			if j == nil {
				j = map[string]any{}
			}

			items = append(items, j)
		}
	}

	items = append(items, map[string]any{})

	results, err := r.eval.Evaluate(r.ctx, Evaluation{
		Expressions: r.sandbox, Items: items, Params: r.params, Execution: r.execution,
	})
	if err != nil {
		r.evalErr = fmt.Errorf("expressions: %w", err)
		return
	}

	if len(results) != len(items) {
		r.evalErr = fmt.Errorf("expressions: %d results for %d items", len(results), len(items))
		return
	}

	r.rows = make(map[ItemRef][]EvalResult, len(refs))
	for i, ref := range refs {
		r.rows[ref] = results[i]
	}

	r.none = results[len(refs)]
}

// typed checks an expression's value as the setting's kind, reading text as numbers and
// true or false where those are wanted.
func typed(spec ParamSpec, v any, what string) (any, error) {
	if s, ok := v.(string); ok {
		v = fromText(spec.Kind, s)
	}

	switch spec.Kind {
	case ParamString, ParamDuration:
		if _, ok := v.(string); !ok {
			v = Format(v)
		}
	case ParamDecimal:
		if d, ok := v.(decimal.Decimal); ok {
			v = d.String()
		}
	}

	if msg := checkParam(spec, v); msg != "" {
		return nil, fmt.Errorf("%s %s, which %s", what, describe(v), msg)
	}

	return v, nil
}

func fromText(kind ParamKind, s string) any {
	t := strings.TrimSpace(s)

	switch kind {
	case ParamNumber:
		if n, err := strconv.Atoi(t); err == nil {
			return n
		}
	case ParamBool:
		if b, err := strconv.ParseBool(t); err == nil {
			return b
		}
	}

	return s
}

func describe(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	default:
		return Format(x)
	}
}

// CheckExpressions reports expressions a saved workflow would keep but could not use:
// those that do not compile, as compile says, and any in a setting that takes none,
// where {{ }} would stay as written. Plain paths need no compiling. Saving asks this.
func (r *Registry) CheckExpressions(w domain.Workflow, compile func(expr string) error) error {
	var v domain.ValidationError

	for _, n := range w.Nodes {
		typ, trigger, ok := r.Type(n.Type)
		if !ok {
			continue
		}

		for _, spec := range typ.Params {
			value := n.Params[spec.Name]
			if !HasExpressions(value) {
				continue
			}

			problem := func(msg string) {
				v.Add(domain.Problem{
					NodeID: n.ID, Param: spec.Name, Err: ErrInvalidParam,
					Message: fmt.Sprintf("node %q: param %q: %s", n.ID, spec.Name, msg),
				})
			}

			switch {
			case trigger:
				problem("triggers work out no expressions: {{ }} would stay as written")
				continue
			case spec.Kind == ParamCode:
				continue // {{ }} in code is the code's
			case !takesExpressions(typ, spec):
				problem("this setting takes no expressions: {{ }} would stay as written")
				continue
			}

			seen := map[string]bool{}

			for _, expr := range expressions(value) {
				if seen[expr] || plain(expr) {
					continue
				}

				seen[expr] = true

				if err := compile(expr); err != nil {
					problem(fmt.Sprintf("{{ %s }}: %v", expr, err))
				}
			}
		}
	}

	return v.Err()
}

// screened is what screen took out of a node's input: the items whose settings failed,
// and, for each port, the index in the input of each item the node got.
type screened struct {
	failed []Item
	kept   map[string][]int
}

// screen works each item's settings out before a node runs (D41). An item whose
// expressions fail is kept back, to go to the error port like an item the node failed;
// the node gets the others, and rc a resolver that reads its indexes as theirs.
func screen(rc *RunContext, in Ports) (Ports, *screened) {
	r, ok := rc.Params.(*exprResolver)
	if !ok {
		return in, nil
	}

	given := make(Ports, len(in))
	s := &screened{kept: make(map[string][]int, len(in))}

	for _, port := range slices.Sorted(maps.Keys(in)) {
		for i, it := range in[port] {
			src := ItemRef{Port: port, Index: i}

			if _, err := r.Resolve(src); err != nil {
				s.failed = append(s.failed, Item{JSON: it.JSON, Source: []ItemRef{src}, Err: err})
				continue
			}

			given[port] = append(given[port], it)
			s.kept[port] = append(s.kept[port], i)
		}
	}

	if len(s.failed) == 0 {
		return in, nil
	}

	rc.Params = remapped{inner: r, kept: s.kept}

	return given, s
}

// restore turns the sources of what the node sent back into the indexes of its input,
// and adds the items it never got to the error port.
func (s *screened) restore(out Ports) Ports {
	if s == nil {
		return out
	}

	if out == nil {
		out = Ports{}
	}

	for _, items := range out {
		for i, it := range items {
			if len(it.Source) == 0 {
				continue
			}

			src := make([]ItemRef, len(it.Source))
			for j, ref := range it.Source {
				src[j] = s.original(ref)
			}

			items[i].Source = src
		}
	}

	out.Add(ErrorPort, s.failed...)

	return out
}

func (s *screened) original(ref ItemRef) ItemRef {
	if kept := s.kept[ref.Port]; ref.Index >= 0 && ref.Index < len(kept) {
		ref.Index = kept[ref.Index]
	}

	return ref
}

// remapped resolves the node's own indexes as those of its input.
type remapped struct {
	inner ParamResolver
	kept  map[string][]int
}

func (m remapped) Resolve(src ItemRef) (Params, error) {
	if kept := m.kept[src.Port]; src.Index >= 0 && src.Index < len(kept) {
		src.Index = kept[src.Index]
	} else {
		src.Index = -1 // asked without an item
	}

	return m.inner.Resolve(src)
}

// CheckCode reports code settings whose code does not compile, as compile says, where it
// does not. Saving asks this.
func (r *Registry) CheckCode(w domain.Workflow, compile func(source string) error) error {
	var v domain.ValidationError

	for _, n := range w.Nodes {
		typ, _, ok := r.Type(n.Type)
		if !ok {
			continue
		}

		for _, spec := range typ.Params {
			source, isText := n.Params[spec.Name].(string)
			if spec.Kind != ParamCode || !isText {
				continue
			}

			if err := compile(source); err != nil {
				v.Add(domain.Problem{
					NodeID: n.ID, Param: spec.Name, Err: ErrInvalidParam,
					Message: fmt.Sprintf("node %q: param %q: %v", n.ID, spec.Name, err),
				})
			}
		}
	}

	return v.Err()
}
