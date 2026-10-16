package engine

import (
	"context"
	"errors"
	"html"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// fakeEvaluator works out a few expressions in Go, and counts how often it is asked.
type fakeEvaluator struct {
	calls int
	got   Evaluation
	err   error
}

func (f *fakeEvaluator) Evaluate(_ context.Context, e Evaluation) ([][]EvalResult, error) {
	f.calls++
	f.got = e

	if f.err != nil {
		return nil, f.err
	}

	out := make([][]EvalResult, len(e.Items))

	for i, item := range e.Items {
		out[i] = make([]EvalResult, len(e.Expressions))

		for j, expr := range e.Expressions {
			switch expr {
			case "spread * 100":
				d, _ := decimal.NewFromString(Format(item["spread"]))
				out[i][j] = EvalResult{Value: d.Mul(decimal.NewFromInt(100)).String()}
			case "n + 1":
				n, _ := item["n"].(int)
				out[i][j] = EvalResult{Value: float64(n + 1)}
			case "n > 1":
				n, _ := item["n"].(int)
				out[i][j] = EvalResult{Value: n > 1}
			case `["a", "b"]`:
				out[i][j] = EvalResult{Value: []any{"a", "b"}}
			case "nothing":
				out[i][j] = EvalResult{}
			default:
				out[i][j] = EvalResult{Err: "ReferenceError: " + expr + " is not defined"}
			}
		}
	}

	return out, nil
}

var exprType = NodeType{
	Name: "t.expr", Inputs: []string{DefaultPort}, Outputs: []string{DefaultPort}, OutputsParam: "ports",
	Params: []ParamSpec{
		{Name: "text", Kind: ParamString},
		{Name: "message", Kind: ParamString, Template: true},
		{Name: "limit", Kind: ParamNumber, Default: 10},
		{Name: "least", Kind: ParamDecimal},
		{Name: "on", Kind: ParamBool},
		{Name: "every", Kind: ParamDuration},
		{Name: "tags", Kind: ParamList},
		{Name: "mode", Kind: ParamString, Options: []string{"fast", "slow"}},
		{Name: "wallet", Kind: ParamWallet},
		{Name: "field", Kind: ParamString, Field: true},
		{Name: "ports", Kind: ParamList},
	},
}

func resolver(t *testing.T, ev Evaluator, params map[string]any, items ...map[string]any) ParamResolver {
	t.Helper()

	in := Ports{}
	for _, it := range items {
		in.Add(DefaultPort, Item{JSON: it})
	}

	e := &Executor{evaluator: ev}

	return e.resolverFor(context.Background(), exprType, params, in, map[string]any{
		"id": "ex-1", "workflowId": "wf-1", "startedAt": time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
	})
}

func at(i int) ItemRef { return ItemRef{Port: DefaultPort, Index: i} }

func TestResolveLiteral(t *testing.T) {
	t.Parallel()

	r := resolver(t, nil, map[string]any{"text": "no expressions here", "wallet": "{{ w }}"})

	_, isLiteral := r.(literalResolver)
	assert.True(t, isLiteral, "nothing to work out: the literal resolver, as before")
}

func TestResolvePlainFields(t *testing.T) {
	t.Parallel()

	ev := &fakeEvaluator{}
	r := resolver(t, ev, map[string]any{
		"text":  "{{question}} by {{ item.who }} in {{ execution.id }} up to {{ params.limit }}, {{ outcomes.0.label }}{{missing}}",
		"limit": "{{ n }}",
		"least": "{{ price }}",
		"on":    "{{ open }}",
	}, map[string]any{
		"question": "BTC?", "who": "alice", "n": 3, "price": decimal.RequireFromString("0.42"), "open": true,
		"outcomes": []any{map[string]any{"label": "Yes"}},
	})

	p, err := r.Resolve(at(0))
	require.NoError(t, err)

	assert.Equal(t, "BTC? by alice in ex-1 up to {{ n }}, Yes", p["text"], "params are as configured")
	assert.Equal(t, 3, p["limit"])
	assert.Equal(t, "0.42", p["least"], "decimals keep their text")
	assert.Equal(t, true, p["on"])
	assert.Zero(t, ev.calls, "plain fields never reach the sandbox")
}

func TestResolveSandbox(t *testing.T) {
	t.Parallel()

	ev := &fakeEvaluator{}
	r := resolver(t, ev, map[string]any{
		"text":  "Spread: {{ spread * 100 }}%",
		"limit": "{{ n + 1 }}",
		"on":    "{{ n > 1 }}",
		"tags":  `{{ ["a", "b"] }}`,
	}, map[string]any{"spread": "0.012", "n": 1}, map[string]any{"spread": "0.03", "n": 5})

	first, err := r.Resolve(at(0))
	require.NoError(t, err)

	second, err := r.Resolve(at(1))
	require.NoError(t, err)

	assert.Equal(t, "Spread: 1.2%", first["text"])
	assert.Equal(t, "Spread: 3%", second["text"])
	assert.Equal(t, 2, mustInt(t, first, "limit"))
	assert.Equal(t, false, first["on"])
	assert.Equal(t, true, second["on"])
	assert.Equal(t, []any{"a", "b"}, first["tags"])

	assert.Equal(t, 1, ev.calls, "once a run, for every item")
	assert.Len(t, ev.got.Items, 3, "the items, and none, for settings asked without one")
	assert.Equal(t, "ex-1", ev.got.Execution["id"])
}

func mustInt(t *testing.T, p Params, name string) int {
	t.Helper()

	n, err := p.Int(name)
	require.NoError(t, err)

	return n
}

func TestResolveTemplate(t *testing.T) {
	t.Parallel()

	r := resolver(t, &fakeEvaluator{}, map[string]any{"message": "<b>{{question}}</b> moved {{ spread * 100 }}%"},
		map[string]any{"question": "A < B", "spread": "0.5"})

	p, err := r.Resolve(at(0))
	require.NoError(t, err)

	tmpl, err := p.Template("message")
	require.NoError(t, err)
	assert.Equal(t, "<b>A &lt; B</b> moved 50%", tmpl.Render(nil, html.EscapeString), "the node escapes the values")
}

func TestResolveProblems(t *testing.T) {
	t.Parallel()

	item := map[string]any{"spread": "0.1", "n": 1, "word": "lots"}

	tests := []struct {
		name   string
		params map[string]any
		ev     Evaluator
		want   string
	}{
		{"a throw", map[string]any{"text": "{{ spred * 100 }}"}, &fakeEvaluator{}, `param "text": {{ spred * 100 }}: ReferenceError: spred * 100 is not defined`},
		{"the wrong kind", map[string]any{"limit": "{{ word }}"}, &fakeEvaluator{}, `param "limit": {{ word }} gave "lots", which must be a whole number`},
		{"not an option", map[string]any{"mode": "{{ word }}"}, &fakeEvaluator{}, `{{ word }} gave "lots", which "lots" is not one of [fast slow]`},
		{"text that is not a number", map[string]any{"limit": "{{ n }}0x"}, &fakeEvaluator{}, `param "limit": the text "10x", which must be a whole number`},
		{"no runtime", map[string]any{"text": "{{ spread * 100 }}"}, nil, "need the code runtime"},
		{"the runtime failing", map[string]any{"text": "{{ spread * 100 }}"}, &fakeEvaluator{err: errors.New("out of time")}, "expressions: out of time"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := resolver(t, tt.ev, tt.params, item).Resolve(at(0))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestResolveLeaves(t *testing.T) {
	t.Parallel()

	r := resolver(t, &fakeEvaluator{}, map[string]any{
		"text":   "{{ spread * 100 }}",
		"limit":  "{{ nothing }}",
		"wallet": "{{ w }}", "field": "{{ f }}", "ports": []any{"{{ p }}"},
		"every": "{{ wait }}s",
	}, map[string]any{"spread": "1", "wait": 30})

	p, err := r.Resolve(at(0))
	require.NoError(t, err)

	assert.Equal(t, 10, p["limit"], "an expression giving nothing leaves the default")
	assert.Equal(t, "{{ w }}", p["wallet"], "wallets, fields and ports take no expressions")
	assert.Equal(t, "{{ f }}", p["field"])
	assert.Equal(t, []any{"{{ p }}"}, p["ports"])
	assert.Equal(t, "30s", p["every"])

	none, err := resolver(t, &fakeEvaluator{}, map[string]any{"text": "{{ spread * 100 }}"}, map[string]any{"spread": "1"}).
		Resolve(ItemRef{Port: DefaultPort, Index: 9})
	require.NoError(t, err)
	assert.Equal(t, "0", none["text"], "asked without an item, the item is empty")
}

type stubNode struct{ typ NodeType }

func (n stubNode) Type() NodeType { return n.typ }

func (stubNode) Run(context.Context, RunContext, Ports) (Ports, error) { return nil, nil }

type stubTrigger struct{ typ NodeType }

func (t stubTrigger) Type() NodeType { return t.typ }

func (stubTrigger) Start(context.Context, TriggerContext, Emit) error { return nil }

func TestCheckExpressions(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	require.NoError(t, r.RegisterNode(stubNode{typ: exprType}))
	require.NoError(t, r.RegisterTrigger(stubTrigger{typ: NodeType{
		Name: "t.trigger", Outputs: []string{DefaultPort}, Params: []ParamSpec{{Name: "startsWith", Kind: ParamString}},
	}}))

	var compiled []string
	compile := func(expr string) error {
		compiled = append(compiled, expr)
		if strings.HasSuffix(expr, "*") {
			return errors.New("line 1, column 8: Unexpected end of the expression")
		}

		return nil
	}

	w := domain.Workflow{Nodes: []domain.NodeSpec{
		{ID: "go", Type: "t.trigger", Params: map[string]any{"startsWith": "{{ x }}"}},
		{ID: "n", Type: "t.expr", Params: map[string]any{
			"text":    "{{ question }} {{ spread * 100 }} {{ spread * 100 }}",
			"message": "{{ price * }}",
			"limit":   "{{ params.max + 1 }}",
			"wallet":  "{{ w }}",
			"field":   "{{ f }}",
			"ports":   []any{"{{ p }}"},
		}},
	}}

	err := r.CheckExpressions(w, compile)
	require.ErrorIs(t, err, domain.ErrInvalidWorkflow)

	var v *domain.ValidationError
	require.ErrorAs(t, err, &v)

	got := map[string]string{}
	for _, p := range v.Problems {
		got[string(p.NodeID)+"."+p.Param] = p.Message
	}

	assert.Equal(t, map[string]string{
		"go.startsWith": `node "go": param "startsWith": triggers work out no expressions: {{ }} would stay as written`,
		"n.message":     `node "n": param "message": {{ price * }}: line 1, column 8: Unexpected end of the expression`,
		"n.wallet":      `node "n": param "wallet": this setting takes no expressions: {{ }} would stay as written`,
		"n.field":       `node "n": param "field": this setting takes no expressions: {{ }} would stay as written`,
		"n.ports":       `node "n": param "ports": this setting takes no expressions: {{ }} would stay as written`,
	}, got)

	assert.ElementsMatch(t, []string{"spread * 100", "price *", "params.max + 1"}, compiled,
		"each once; plain fields need no compiling")

	require.NoError(t, r.CheckExpressions(domain.Workflow{Nodes: []domain.NodeSpec{
		{ID: "n", Type: "t.expr", Params: map[string]any{"text": "no {{ expressions }} to fail"}},
	}}, compile))
}
