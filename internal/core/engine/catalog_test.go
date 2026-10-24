package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// owners is a catalog with custom nodes for alice only.
type owners struct {
	base   *engine.Registry
	custom map[domain.UserID][]engine.Node
	err    error
}

func (o owners) For(_ context.Context, owner domain.UserID) (*engine.Registry, error) {
	if o.err != nil {
		return nil, o.err
	}

	return o.base.With(o.custom[owner]...)
}

func splitter() fakeNode {
	return fakeNode{
		typ: engine.NodeType{Name: "custom.split", Inputs: mainPorts(), Outputs: mainPorts("wide", "tight"), Code: true},
		run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
			return engine.Ports{"wide": in[engine.DefaultPort]}, nil
		},
	}
}

func TestRegistryWith(t *testing.T) {
	base := testRegistry(t)

	with, err := base.With(splitter())
	require.NoError(t, err)

	_, ok := with.Node("custom.split")
	assert.True(t, ok)
	_, ok = with.Node("test.pass")
	assert.True(t, ok, "the built-ins are there too")
	_, ok = with.Trigger("test.trigger")
	assert.True(t, ok)

	_, ok = base.Node("custom.split")
	assert.False(t, ok, "the base is left as it was")

	_, err = base.With(fakeNode{typ: engine.NodeType{Name: "test.pass"}})
	assert.ErrorContains(t, err, `"test.pass": already registered`)
}

func TestExecuteTakesTheOwnersTypes(t *testing.T) {
	cat := owners{base: testRegistry(t), custom: map[domain.UserID][]engine.Node{"alice": {splitter()}}}

	ex, err := engine.NewExecutor(observability.NoopUtility(), cat.base,
		engine.WithClock(tickingClock()), engine.WithCatalog(cat))
	require.NoError(t, err)

	flow := func(owner domain.UserID) domain.Workflow {
		return domain.Workflow{
			ID: "wf", OwnerID: owner,
			Nodes:       []domain.NodeSpec{trigger(), spec("split", "custom.split", nil), spec("a", "test.pass", nil)},
			Connections: []domain.Connection{mainLink("trigger", "split"), link("split", "wide", "a", engine.DefaultPort)},
		}
	}

	res, err := ex.Execute(t.Context(), flow("alice"), "trigger", fired(item("n", 1)))
	require.NoError(t, err)
	assert.Equal(t, domain.ExecutionSucceeded, res.Execution.Status, res.Execution.Error)
	assert.Len(t, res.Outputs["a"][engine.DefaultPort], 1)

	res, err = ex.Execute(t.Context(), flow("bob"), "trigger", fired(item("n", 1)))
	require.NoError(t, err)
	assert.Equal(t, domain.ExecutionFailed, res.Execution.Status, "bob has no such node")
	assert.Contains(t, res.Execution.Error, `unknown node type "custom.split"`)

	failing := errors.New("db down")
	ex, err = engine.NewExecutor(observability.NoopUtility(), cat.base, engine.WithCatalog(owners{err: failing}))
	require.NoError(t, err)

	_, err = ex.Execute(t.Context(), flow("alice"), "trigger", fired(item("n", 1)))
	assert.ErrorIs(t, err, failing, "an execution that could not start")
}

// A waiting execution whose owner's types cannot be read fails, rather than being lost.
func TestContinueWithoutTheOwnersTypes(t *testing.T) {
	ctx := context.Background()
	waits := &memWaits{recs: map[domain.ExecutionID]ports.WaitRecord{}}
	r := testRegistry(t)
	require.NoError(t, r.RegisterNode(pauser{}))

	cat := &switchable{base: r}

	ex, err := engine.NewExecutor(observability.NoopUtility(), r,
		engine.WithClock(func() time.Time { return waitNow }),
		engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
		engine.WithWaits(waits, func() engine.WaitLimits { return engine.WaitLimits{} }),
		engine.WithCatalog(cat),
	)
	require.NoError(t, err)

	flow := domain.Workflow{
		ID: "wf", OwnerID: "alice",
		Nodes:       []domain.NodeSpec{trigger(), spec("pause", "test.pause", nil), spec("a", "test.pass", nil)},
		Connections: []domain.Connection{mainLink("trigger", "pause"), mainLink("pause", "a")},
	}

	res, err := ex.Execute(ctx, flow, "trigger", fired(item("v", 1)))
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

	cat.err = errors.New("db down")

	rec, err := waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)

	res, err = ex.Continue(ctx, rec, engine.Resumption{Payload: map[string]any{"answer": "yes"}})
	require.NoError(t, err)
	assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
	assert.Equal(t, "the node types of the workflow's owner: db down", res.Execution.Error)
}

type switchable struct {
	base *engine.Registry
	err  error
}

func (s *switchable) For(context.Context, domain.UserID) (*engine.Registry, error) {
	return s.base, s.err
}

func TestRunnerTakesTheOwnersTriggers(t *testing.T) {
	base := engine.NewRegistry()
	require.NoError(t, base.RegisterNode(fakeNode{typ: engine.NodeType{Name: "test.pass", Inputs: mainPorts(), Outputs: mainPorts()}}))

	cat := owners{base: base}

	ex, err := engine.NewExecutor(observability.NoopUtility(), base, engine.WithCatalog(cat))
	require.NoError(t, err)

	runner := engine.NewRunner(observability.NoopUtility(), cat, ex)

	err = runner.Run(t.Context(), domain.Workflow{OwnerID: "alice", Nodes: []domain.NodeSpec{spec("a", "test.pass", nil)}}, false)
	require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
	assert.ErrorContains(t, err, "no trigger node")

	err = engine.NewRunner(observability.NoopUtility(), owners{err: errors.New("db down")}, ex).
		Run(t.Context(), domain.Workflow{OwnerID: "alice"}, false)
	assert.ErrorContains(t, err, "the node types of the workflow's owner: db down")
}

// No tasks here.
func (*memWaits) LeaseTask(context.Context, domain.UserID, ports.TaskLease, time.Time) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}

func (*memWaits) ReleaseTask(context.Context, domain.UserID, domain.ExecutionID) error {
	return domain.ErrNotFound
}

func (*memWaits) RenewLeases(context.Context, domain.UserID, string, time.Time) error { return nil }

func (*memWaits) TakeTask(context.Context, domain.UserID, domain.ExecutionID, string) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}
