package engine_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func TestRetryWait(t *testing.T) {
	var waits []time.Duration
	for n := 1; n <= 7; n++ {
		waits = append(waits, engine.RetryWait(n))
	}

	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}, waits,
		"twice the last, from a second, at most 30 s")
}

func TestRetries(t *testing.T) {
	two := 2
	none := 0
	reader := engine.NodeType{Name: "r", Reads: true}
	getter := engine.NodeType{
		Name: "g", Reads: true, ReadsWhen: &engine.Choice{Param: "method", Value: "GET"},
		Params: []engine.ParamSpec{{Name: "method", Kind: engine.ParamString, Default: "GET"}},
	}

	for _, tc := range []struct {
		name string
		typ  engine.NodeType
		spec domain.NodeSpec
		want int
	}{
		{name: "a node that only reads, 3 to begin with", typ: reader, want: engine.DefaultRetries},
		{name: "as set", typ: reader, spec: domain.NodeSpec{Retry: &two}, want: 2},
		{name: "off", typ: reader, spec: domain.NodeSpec{Retry: &none}, want: 0},
		{name: "a node that acts, none to begin with", typ: engine.NodeType{Name: "a"}, want: 0},
		{name: "but as set", typ: engine.NodeType{Name: "a"}, spec: domain.NodeSpec{Retry: &two}, want: 2},
		{name: "a GET by default", typ: getter, want: engine.DefaultRetries},
		{name: "a GET as set", typ: getter, spec: domain.NodeSpec{Params: map[string]any{"method": "get"}}, want: engine.DefaultRetries},
		{name: "a POST", typ: getter, spec: domain.NodeSpec{Params: map[string]any{"method": "POST"}}, want: 0},
		{name: "a method from an expression", typ: getter, spec: domain.NodeSpec{Params: map[string]any{"method": "{{ m }}"}}, want: 0},
		{name: "a node that trades, never", typ: engine.NodeType{Name: "t", NoRetry: true}, spec: domain.NodeSpec{Retry: &two}, want: 0},
		{name: "a plugin's that trades, never", typ: engine.NodeType{Name: "p", Trades: true, Reads: true}, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, engine.Retries(tc.typ, tc.spec))
		})
	}
}

// flaky fails each item, or the whole run, until its n-th try.
type flaky struct {
	mu        sync.Mutex
	tries     map[string]int
	runs      int
	wholeTill int            // fails as a whole the first this many runs
	itemTill  map[string]int // an item fails its first this many tries
}

func (f *flaky) run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runs++
	if f.runs <= f.wholeTill {
		return nil, errBoom
	}

	out := engine.Ports{}

	for i, it := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		name, _ := it.JSON["name"].(string)
		f.tries[name]++

		if f.tries[name] <= f.itemTill[name] {
			out.Fail(it, src, fmt.Errorf("%s: busy", name))
			continue
		}

		tag, _ := p.String("tag")
		out.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{"name": name, "tag": tag, "try": f.tries[name]}, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

func retrying(t *testing.T, f *flaky, typ engine.NodeType) *engine.Executor {
	t.Helper()

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{Name: "test.trigger", Outputs: mainPorts()}}))

	typ.Name, typ.Inputs, typ.Outputs = "test.flaky", mainPorts(), mainPorts()
	typ.Params = append(typ.Params, engine.ParamSpec{Name: "tag", Kind: engine.ParamString})
	require.NoError(t, r.RegisterNode(fakeNode{typ: typ, run: f.run}))

	ex, err := engine.NewExecutor(observability.NoopUtility(), r, engine.WithRetryWaits(func(int) time.Duration { return 0 }))
	require.NoError(t, err)

	return ex
}

func flow(n domain.NodeSpec) domain.Workflow {
	n.ID, n.Type = "flaky", "test.flaky"

	return domain.Workflow{
		ID:          "wf",
		Nodes:       []domain.NodeSpec{trigger(), n},
		Connections: []domain.Connection{mainLink("trigger", "flaky")},
	}
}

func itemNames(items []engine.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.JSON["name"].(string))
	}

	return out
}

func TestExecuteRetries(t *testing.T) {
	ctx := context.Background()
	three := []engine.Item{item("name", "a"), item("name", "b"), item("name", "c")}

	t.Run("only the items it failed, each with its own settings and source, till they pass", func(t *testing.T) {
		f := &flaky{tries: map[string]int{}, itemTill: map[string]int{"b": 2}}

		res, err := retrying(t, f, engine.NodeType{Reads: true}).Execute(ctx, flow(domain.NodeSpec{Params: map[string]any{"tag": "{{ name }}"}}), "trigger", fired(three...))
		require.NoError(t, err)
		require.Equal(t, domain.ExecutionSucceeded, res.Execution.Status, res.Execution.Error)

		out := res.Outputs["flaky"][engine.DefaultPort]
		assert.Equal(t, []string{"a", "c", "b"}, itemNames(out), "passed on a retry, after the others")
		assert.Equal(t, map[string]any{"name": "b", "tag": "b", "try": 3}, out[2].JSON)
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, out[2].Source, "b was the second item it got")
		assert.Equal(t, map[string]int{"a": 1, "b": 3, "c": 1}, f.tries, "the others not again")

		run := res.Execution.Runs[1]
		assert.Equal(t, 3, run.Tries)
		assert.Equal(t, map[string]int{engine.DefaultPort: 3}, run.Output)
	})

	t.Run("the whole run, when it failed as a whole", func(t *testing.T) {
		f := &flaky{tries: map[string]int{}, wholeTill: 2}

		res, err := retrying(t, f, engine.NodeType{Reads: true}).Execute(ctx, flow(domain.NodeSpec{}), "trigger", fired(three...))
		require.NoError(t, err)
		require.Equal(t, domain.ExecutionSucceeded, res.Execution.Status, res.Execution.Error)
		assert.Equal(t, []string{"a", "b", "c"}, itemNames(res.Outputs["flaky"][engine.DefaultPort]))
		assert.Equal(t, 3, res.Execution.Runs[1].Tries)
	})

	t.Run("no more than it may: what still fails, fails", func(t *testing.T) {
		one := 1
		f := &flaky{tries: map[string]int{}, itemTill: map[string]int{"c": 5}}

		res, err := retrying(t, f, engine.NodeType{Reads: true}).Execute(ctx, flow(domain.NodeSpec{Retry: &one}), "trigger", fired(three...))
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
		assert.Contains(t, res.Execution.Error, "c: busy")
		assert.Equal(t, 2, f.tries["c"])
		assert.Equal(t, 2, res.Execution.Runs[1].Tries)
	})

	t.Run("a node that acts is not retried unless asked", func(t *testing.T) {
		f := &flaky{tries: map[string]int{}, itemTill: map[string]int{"a": 1}}

		res, err := retrying(t, f, engine.NodeType{}).Execute(ctx, flow(domain.NodeSpec{}), "trigger", fired(three...))
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
		assert.Equal(t, 1, f.tries["a"])
		assert.Zero(t, res.Execution.Runs[1].Tries, "tried once")
	})

	t.Run("continuing on failure, what still fails goes out the error port", func(t *testing.T) {
		one := 1
		f := &flaky{tries: map[string]int{}, itemTill: map[string]int{"a": 9}}

		n := domain.NodeSpec{Retry: &one, ContinueOnFail: true}
		res, err := retrying(t, f, engine.NodeType{}).Execute(ctx, flow(n), "trigger", fired(three...))
		require.NoError(t, err)
		require.Equal(t, domain.ExecutionSucceeded, res.Execution.Status, res.Execution.Error)

		failed := res.Outputs["flaky"][engine.ErrorPort]
		require.Len(t, failed, 1)
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 0}}, failed[0].Source)
		assert.Equal(t, []string{"b", "c"}, itemNames(res.Outputs["flaky"][engine.DefaultPort]))
	})

	t.Run("cancelled while it waits, it tries no more", func(t *testing.T) {
		f := &flaky{tries: map[string]int{}, itemTill: map[string]int{"a": 9}}

		r := engine.NewRegistry()
		require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{Name: "test.trigger", Outputs: mainPorts()}}))
		require.NoError(t, r.RegisterNode(fakeNode{
			typ: engine.NodeType{Name: "test.flaky", Inputs: mainPorts(), Outputs: mainPorts(), Reads: true},
			run: f.run,
		}))

		ex, err := engine.NewExecutor(observability.NoopUtility(), r, engine.WithRetryWaits(func(int) time.Duration { return time.Hour }))
		require.NoError(t, err)

		cctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()

		res, err := ex.Execute(cctx, flow(domain.NodeSpec{}), "trigger", fired(item("name", "a")))
		require.NoError(t, err)
		assert.NotEqual(t, domain.ExecutionSucceeded, res.Execution.Status)
		assert.Equal(t, 1, f.tries["a"])
	})
}

func TestValidateRetry(t *testing.T) {
	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{Name: "test.trigger", Outputs: mainPorts()}}))
	require.NoError(t, r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.read", Inputs: mainPorts(), Outputs: mainPorts(), Reads: true}}))
	require.NoError(t, r.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.order", Inputs: mainPorts(), Outputs: mainPorts(), NoRetry: true}}))

	check := func(typ string, retry int, onTrigger bool) error {
		w := domain.Workflow{Nodes: []domain.NodeSpec{trigger(), {ID: "n", Type: typ}}, Connections: []domain.Connection{mainLink("trigger", "n")}}
		if onTrigger {
			w.Nodes[0].Retry = &retry
		} else {
			w.Nodes[1].Retry = &retry
		}

		return r.Validate(w)
	}

	require.NoError(t, check("test.read", 5, false))
	require.NoError(t, check("test.read", 0, false))
	require.NoError(t, check("test.order", 0, false), "off is off")

	for _, tc := range []struct {
		name      string
		typ       string
		retry     int
		onTrigger bool
		want      string
	}{
		{name: "too many", typ: "test.read", retry: 6, want: "from 0 to 5"},
		{name: "fewer than none", typ: "test.read", retry: -1, want: "from 0 to 5"},
		{name: "a node that trades", typ: "test.order", retry: 1, want: "could order twice"},
		{name: "a trigger", typ: "test.read", retry: 1, onTrigger: true, want: "triggers are not retried"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := check(tc.typ, tc.retry, tc.onTrigger)
			require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
			assert.ErrorContains(t, err, tc.want)
			assert.True(t, errors.Is(err, engine.ErrInvalidParam))
		})
	}
}

// What no wait mends fails at once: marked final, or not found, or a setting that is wrong.
func TestRetryLeavesWhatIsFinal(t *testing.T) {
	for _, cause := range []error{
		engine.Final(errors.New("answered 404")),
		fmt.Errorf("no event %q: %w", "x", domain.ErrNotFound),
		fmt.Errorf("%w %q", engine.ErrInvalidParam, "url"),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			runs := 0

			r := engine.NewRegistry()
			require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{Name: "test.trigger", Outputs: mainPorts()}}))
			require.NoError(t, r.RegisterNode(fakeNode{
				typ: engine.NodeType{Name: "test.flaky", Inputs: mainPorts(), Outputs: mainPorts(), Reads: true},
				run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
					runs++
					out := engine.Ports{}
					out.Fail(in[engine.DefaultPort][0], engine.ItemRef{Port: engine.DefaultPort}, cause)

					return out, nil
				},
			}))

			ex, err := engine.NewExecutor(observability.NoopUtility(), r, engine.WithRetryWaits(func(int) time.Duration { return 0 }))
			require.NoError(t, err)

			res, err := ex.Execute(context.Background(), flow(domain.NodeSpec{}), "trigger", fired(item("name", "a")))
			require.NoError(t, err)
			assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
			assert.Equal(t, 1, runs, "tried once")
			assert.Contains(t, res.Execution.Error, cause.Error(), "the error as it was")
		})
	}
}
