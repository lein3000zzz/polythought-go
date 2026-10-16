package codenodes_test

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
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/codenodes"
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
	return ports.CodeResult{Output: json.RawMessage(c.output), Logs: []ports.CodeLog{{Level: "log", Text: "hi"}}}, c.err
}

var started = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func run(t *testing.T, node *codenodes.Node, params map[string]any, items ...map[string]any) engine.Ports {
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

func TestTransform(t *testing.T) {
	t.Parallel()

	c := &caller{output: `[{"n":1},{"n":2.5,"name":"x"}]`}
	out := run(t, codenodes.NewTransform(c, logging.Noop()), map[string]any{"source": "export const run = (items) => items"},
		map[string]any{"q": "A"}, map[string]any{"q": "B"})

	assert.Equal(t, []map[string]any{{"n": json.Number("1")}, {"n": json.Number("2.5"), "name": "x"}}, jsons(out[engine.DefaultPort]),
		"numbers stay exact")

	assert.Equal(t, ports.CodeCall{
		Owner: "alice", Source: "export const run = (items) => items", Export: "run",
		Args:      []any{[]any{map[string]any{"q": "A"}, map[string]any{"q": "B"}}},
		Params:    map[string]any{},
		Execution: ports.CodeExecution{ID: "ex-1", WorkflowID: "wf-1", StartedAt: started},
	}, c.got, "once, with every item, for the owner")

	for name, output := range map[string]string{"not a list": `{"n":1}`, "not objects": `[1,2]`} {
		c := &caller{output: output}
		out := run(t, codenodes.NewTransform(c, logging.Noop()), nil, map[string]any{"q": "A"})
		require.Len(t, out[engine.ErrorPort], 1, name)
		assert.Contains(t, out[engine.ErrorPort][0].Err.Error(), "run ", name)
	}
}

func TestAggregate(t *testing.T) {
	t.Parallel()

	out := run(t, codenodes.NewAggregate(&caller{output: `{"count":2}`}, logging.Noop()), nil, map[string]any{}, map[string]any{})
	require.Len(t, out[engine.DefaultPort], 1)
	assert.Equal(t, map[string]any{"count": json.Number("2")}, out[engine.DefaultPort][0].JSON)
	assert.Len(t, out[engine.DefaultPort][0].Source, 2, "made of every item")

	none := run(t, codenodes.NewAggregate(&caller{output: `null`}, logging.Noop()), nil, map[string]any{})
	assert.Empty(t, none[engine.DefaultPort])
}

func TestFilter(t *testing.T) {
	t.Parallel()

	c := &caller{output: `[{"value":true},{"value":false},{"error":"TypeError: no"},{"value":"yes"}]`}
	out := run(t, codenodes.NewFilter(c, logging.Noop()), nil,
		map[string]any{"i": 0}, map[string]any{"i": 1}, map[string]any{"i": 2}, map[string]any{"i": 3})

	assert.True(t, c.got.Each, "called for each item")
	assert.Equal(t, "keep", c.got.Export)
	assert.Equal(t, []map[string]any{{"i": 0}}, jsons(out[engine.DefaultPort]))

	require.Len(t, out[engine.ErrorPort], 2)
	assert.EqualError(t, out[engine.ErrorPort][0].Err, "TypeError: no")
	assert.EqualError(t, out[engine.ErrorPort][1].Err, `keep must return true or false, not "yes"`)
	assert.Equal(t, 3, out[engine.ErrorPort][1].Source[0].Index)
}

func TestRoute(t *testing.T) {
	t.Parallel()

	c := &caller{output: `[{"value":"tight"},{"value":"wide"},{"value":"sideways"}]`}
	node := codenodes.NewRoute(c, logging.Noop())
	out := run(t, node, nil, map[string]any{"i": 0}, map[string]any{"i": 1}, map[string]any{"i": 2})

	assert.Equal(t, "route", c.got.Export)
	assert.Equal(t, map[string]any{"routes": []string{"wide", "tight"}}, c.got.Params, "its settings, not its code")
	assert.Equal(t, []map[string]any{{"i": 0}}, jsons(out["tight"]))
	assert.Equal(t, []map[string]any{{"i": 1}}, jsons(out["wide"]))
	require.Len(t, out[engine.ErrorPort], 1)
	assert.EqualError(t, out[engine.ErrorPort][0].Err, `route gave "sideways", which is not one of its routes: wide, tight`)

	assert.Equal(t, []string{"wide", "tight"}, node.Type().OutputsOf(nil), "a port per route")
}

func TestCodeFailing(t *testing.T) {
	t.Parallel()

	codeErr := &domain.CodeError{Kind: domain.CodeThrew, Message: "Error: boom"}
	out := run(t, codenodes.NewTransform(&caller{err: codeErr}, logging.Noop()), nil, map[string]any{"i": 0}, map[string]any{"i": 1})

	require.Len(t, out[engine.ErrorPort], 2, "every item it was given, for an error path")
	require.ErrorIs(t, out[engine.ErrorPort][0].Err, codeErr)

	node := codenodes.NewTransform(&caller{err: errors.New("runtime down")}, logging.Noop())
	_, err := node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), nil)},
		engine.Ports{engine.DefaultPort: {{JSON: map[string]any{}}}})
	require.EqualError(t, err, "runtime down", "not the code's failure: the node's")
}

func TestTypes(t *testing.T) {
	t.Parallel()

	for _, n := range []*codenodes.Node{
		codenodes.NewTransform(nil, nil), codenodes.NewFilter(nil, nil), codenodes.NewRoute(nil, nil), codenodes.NewAggregate(nil, nil),
	} {
		typ := n.Type()
		require.NoError(t, engine.NewRegistry().RegisterNode(n), typ.Name)

		switch typ.Name {
		case codenodes.FilterType, codenodes.RouteType:
			assert.Equal(t, shape.Passed(), typ.Items, "%s passes items on", typ.Name)
		default:
			assert.Nil(t, typ.Items, "%s: nobody knows what it makes", typ.Name)
		}
	}
}
