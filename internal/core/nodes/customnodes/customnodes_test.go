package customnodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// caller answers with what it is given to, and keeps the call.
type caller struct {
	got    ports.CodeCall
	output string
	err    error
}

func (c *caller) Call(_ context.Context, call ports.CodeCall) (ports.CodeResult, error) {
	c.got = call
	return ports.CodeResult{Output: json.RawMessage(c.output)}, c.err
}

var started = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

const source = "export function run(items) { return items }"

// split has a decimal threshold and two outputs, and passes items on.
func split() domain.CustomNode {
	return domain.CustomNode{
		Name: "spread-split", Label: "Spread split", Description: "Wide books one way, tight ones the other.",
		Settings: []domain.CustomSetting{
			{Name: "threshold", Label: "Threshold", Kind: "decimal", Default: "0.02"},
			{Name: "on", Label: "Field", Kind: "string", Field: true, FieldKinds: []string{"decimal"}, Default: "spread"},
		},
		Outputs: []string{"wide", "tight"},
		Items:   domain.CustomItems{Passed: true},
		Source:  source,
	}
}

// summary makes items of its own, with one output.
func summary() domain.CustomNode {
	return domain.CustomNode{
		Name: "summary", Label: "Summary", Outputs: []string{"main"},
		Items: domain.CustomItems{Fields: []domain.CustomField{
			{Name: "count", Kind: "number"},
			{Name: "best", Kind: "object", Nullable: true, Fields: []domain.CustomField{{Name: "price", Kind: "decimal"}}},
			{Name: "tags", Kind: "list", Of: &domain.CustomField{Kind: "text", Options: []string{"a", "b"}}},
		}},
		Source: source,
	}
}

func run(t *testing.T, node *customnodes.Node, params map[string]any, items ...map[string]any) engine.Ports {
	t.Helper()

	in := engine.Ports{}
	for _, it := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: it})
	}

	rc := engine.RunContext{
		ExecutionID: "ex-1", StartedAt: started, WorkflowID: "wf-1", OwnerID: "alice", NodeID: "n",
		Params: engine.LiteralParams(node.Type(), params),
	}

	out, err := node.Run(context.Background(), rc, in)
	require.NoError(t, err)

	return out
}

func jsons(items []engine.Item) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = it.JSON
	}

	return out
}

func TestTypeOf(t *testing.T) {
	t.Parallel()

	typ := customnodes.TypeOf(split())
	require.NoError(t, engine.NewRegistry().RegisterNode(customnodes.New(split(), nil, nil)), "a type the registry takes")

	assert.Equal(t, "custom.spread-split", typ.Name)
	assert.Equal(t, []string{"main"}, typ.Inputs)
	assert.Equal(t, []string{"wide", "tight"}, typ.Outputs)
	assert.True(t, typ.Code, "the code switch covers it")
	assert.Equal(t, shape.Passed(), typ.Items)
	assert.Equal(t, engine.ParamSpec{
		Name: "on", Label: "Field", Kind: engine.ParamString, Default: "spread",
		Field: true, FieldKinds: []engine.FieldKind{engine.FieldDecimal},
	}, typ.Params[1])

	own := customnodes.TypeOf(summary())
	require.NoError(t, engine.NewRegistry().RegisterNode(customnodes.New(summary(), nil, nil)))

	assert.Equal(t, &engine.Items{Fields: []engine.Field{
		{Name: "count", Kind: engine.FieldNumber, Fields: []engine.Field{}},
		{Name: "best", Kind: engine.FieldObject, Nullable: true, Fields: []engine.Field{
			{Name: "price", Kind: engine.FieldDecimal, Fields: []engine.Field{}},
		}},
		{Name: "tags", Kind: engine.FieldList, Fields: []engine.Field{}, Of: &engine.Field{
			Kind: engine.FieldText, Options: []string{"a", "b"}, Fields: []engine.Field{},
		}},
	}}, own.Items)
}

func TestRunByPort(t *testing.T) {
	t.Parallel()

	c := &caller{output: `{"wide":[{"spread":"0.05"}],"tight":[{"spread":"0.01"},{"spread":"0.02"}]}`}
	out := run(t, customnodes.New(split(), c, logging.Noop()), map[string]any{"threshold": "0.03"},
		map[string]any{"spread": "0.05"}, map[string]any{"spread": "0.01"}, map[string]any{"spread": "0.02"})

	assert.Equal(t, []map[string]any{{"spread": "0.05"}}, jsons(out["wide"]))
	assert.Equal(t, []map[string]any{{"spread": "0.01"}, {"spread": "0.02"}}, jsons(out["tight"]))

	assert.Equal(t, ports.CodeCall{
		Owner: "alice", Source: source, Export: "run",
		Args:      []any{[]any{map[string]any{"spread": "0.05"}, map[string]any{"spread": "0.01"}, map[string]any{"spread": "0.02"}}},
		Params:    map[string]any{"threshold": "0.03", "on": "spread"},
		Execution: ports.CodeExecution{ID: "ex-1", WorkflowID: "wf-1", StartedAt: started},
	}, c.got, "once, with every item and all its settings, for the owner")

	c.output = `{"wide":[{"spread":"0.05"}]}`
	out = run(t, customnodes.New(split(), c, logging.Noop()), nil, map[string]any{"spread": "0.05"})
	assert.Len(t, out["wide"], 1)
	assert.Empty(t, out["tight"], "a port left out sends none")
}

func TestRunOneOutput(t *testing.T) {
	t.Parallel()

	c := &caller{output: `[{"count":2,"best":{"price":"0.40"},"tags":["a"],"extra":true},{"count":0,"best":null,"tags":[]}]`}
	out := run(t, customnodes.New(summary(), c, logging.Noop()), nil, map[string]any{"q": "A"})

	assert.Equal(t, []map[string]any{
		{"count": json.Number("2"), "best": map[string]any{"price": "0.40"}, "tags": []any{"a"}, "extra": true},
		{"count": json.Number("0"), "best": nil, "tags": []any{}},
	}, jsons(out[engine.DefaultPort]), "numbers stay exact; fields beyond the declared ones are fine")
	assert.Empty(t, out[engine.ErrorPort])
}

// What run returns that the node cannot use fails every item it got, saying what is
// wrong, so the error port keeps carrying what came in (D53, D55).
func TestRunFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		def    domain.CustomNode
		output string
		err    error
		want   string
	}{
		{name: "not a list", def: summary(), output: `{"count":1}`, want: `what run returned must be a list of items, not {"count":1}`},
		{name: "not objects", def: summary(), output: `[1]`, want: "what run returned item 0 is 1, not an object"},
		{
			name: "a declared field missing", def: summary(), output: `[{"count":1,"best":null,"tags":[]},{"count":1,"tags":[]}]`,
			want: "what run returned item 1 does not match the node's fields: field best is missing",
		},
		{
			name: "one of the wrong kind, nested", def: summary(), output: `[{"count":1,"best":{"price":"lots"},"tags":["c"]}]`,
			want: "what run returned item 0 does not match the node's fields: field best.price is string lots, not decimal",
		},
		{name: "not by port", def: split(), output: `[{"spread":"0.05"}]`, want: "run must return the items of each output by name, like { wide: [...] }"},
		{name: "a port it does not have", def: split(), output: `{"wide":[],"medium":[]}`, want: `run returned items for "medium", which is not one of its outputs: wide, tight`},
		{name: "a port's items not a list", def: split(), output: `{"wide":{}}`, want: "what run returned for wide must be a list of items, not {}"},
		{
			name: "the code throwing", def: split(),
			err: &domain.CodeError{Kind: domain.CodeThrew, Message: "TypeError: boom"}, want: "TypeError: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &caller{output: tt.output, err: tt.err}
			out := run(t, customnodes.New(tt.def, c, logging.Noop()), nil, map[string]any{"q": "A"}, map[string]any{"q": "B"})

			failed := out[engine.ErrorPort]
			require.Len(t, failed, 2, "every item it got")
			assert.Equal(t, []map[string]any{{"q": "A"}, {"q": "B"}}, jsons(failed), "as they came in")
			assert.Contains(t, failed[0].Err.Error(), tt.want)

			for _, port := range customnodes.TypeOf(tt.def).Outputs {
				assert.Empty(t, out[port])
			}
		})
	}
}

func TestRunWithoutItems(t *testing.T) {
	t.Parallel()

	c := &caller{err: errors.New("not called")}
	out := run(t, customnodes.New(split(), c, logging.Noop()), nil)

	assert.Empty(t, out)
	assert.Empty(t, c.got.Source, "no call")
}
