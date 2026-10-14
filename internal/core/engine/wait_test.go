package engine_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var waitNow = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// pauser waits an hour. Answered, it passes items on with the answer; timed out, it sends
// them to "late"; asked to, it waits again.
type pauser struct{}

func (pauser) Type() engine.NodeType {
	return engine.NodeType{Name: "test.pause", Inputs: mainPorts(), Outputs: mainPorts("main", "late")}
}

func (pauser) Run(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return nil, &engine.Wait{Until: waitNow.Add(time.Hour), State: map[string]any{"count": len(in[engine.DefaultPort])}}
}

func (pauser) Resume(_ context.Context, _ engine.RunContext, in engine.Ports, r engine.Resumption) (engine.Ports, error) {
	if r.Payload["again"] == true {
		return nil, &engine.Wait{Until: waitNow.Add(2 * time.Hour)}
	}

	out := engine.Ports{}

	for i, it := range in[engine.DefaultPort] {
		j := map[string]any{"count": r.State["count"]}
		for k, v := range it.JSON {
			j[k] = v
		}

		port := engine.DefaultPort
		if r.TimedOut {
			port = "late"
		} else {
			j["answer"] = r.Payload["answer"]
		}

		out.Add(port, engine.Item{JSON: j, Source: []engine.ItemRef{{Port: engine.DefaultPort, Index: i}}})
	}

	return out, nil
}

// cannotWait asks to wait without being able to resume.
type cannotWait struct{}

func (cannotWait) Type() engine.NodeType {
	return engine.NodeType{Name: "test.cannot", Inputs: mainPorts(), Outputs: mainPorts()}
}

func (cannotWait) Run(context.Context, engine.RunContext, engine.Ports) (engine.Ports, error) {
	return nil, &engine.Wait{Until: waitNow.Add(time.Minute)}
}

// memWaits keeps waits in memory, like the repository.
type memWaits struct {
	mu   sync.Mutex
	recs map[domain.ExecutionID]ports.WaitRecord
}

func (m *memWaits) Add(_ context.Context, w ports.WaitRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recs[w.ExecutionID] = w

	return nil
}

func (m *memWaits) CountForOwner(_ context.Context, owner domain.UserID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	n := 0

	for _, r := range m.recs {
		if r.OwnerID == owner {
			n++
		}
	}

	return n, nil
}

func (m *memWaits) Take(_ context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.recs[id]
	if !ok || r.OwnerID != owner {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	delete(m.recs, id)

	return r, nil
}

func (m *memWaits) TakeDue(context.Context, time.Time, int) ([]ports.WaitRecord, error) {
	return nil, nil
}

func (m *memWaits) TakeForWorkflow(context.Context, domain.UserID, domain.WorkflowID) ([]ports.WaitRecord, error) {
	return nil, nil
}

// saves records every save of an execution, by ID.
type saves struct {
	mu   sync.Mutex
	last map[domain.ExecutionID]domain.Execution
	n    int
}

func (s *saves) Save(_ context.Context, e domain.Execution) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.last[e.ID] = e
	s.n++

	return nil
}

type waitDeps struct {
	waits    *memWaits
	saves    *saves
	captured *[]engine.Item
	notified *[]domain.ExecutionStatus
}

func waitingExecutor(t *testing.T, limits engine.WaitLimits) (*engine.Executor, waitDeps) {
	t.Helper()

	r := testRegistry(t)
	require.NoError(t, r.RegisterNode(pauser{}))
	require.NoError(t, r.RegisterNode(cannotWait{}))

	var captured []engine.Item

	require.NoError(t, r.RegisterNode(fakeNode{
		typ: engine.NodeType{Name: "test.capture", Inputs: mainPorts(), Outputs: mainPorts()},
		run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
			captured = append(captured, in[engine.DefaultPort]...)
			return engine.Ports{engine.DefaultPort: in[engine.DefaultPort]}, nil
		},
	}))

	d := waitDeps{
		waits: &memWaits{recs: map[domain.ExecutionID]ports.WaitRecord{}}, saves: &saves{last: map[domain.ExecutionID]domain.Execution{}},
		captured: &captured, notified: &[]domain.ExecutionStatus{},
	}

	ex, err := engine.NewExecutor(observability.NoopUtility(), r,
		engine.WithClock(func() time.Time { return waitNow }),
		engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
		engine.WithRecorder(d.saves),
		engine.WithNotifier(notifierFunc(func(_ domain.UserID, e domain.Execution) { *d.notified = append(*d.notified, e.Status) })),
		engine.WithWaits(d.waits, func() engine.WaitLimits { return limits }),
	)
	require.NoError(t, err)

	return ex, d
}

type notifierFunc func(domain.UserID, domain.Execution)

func (f notifierFunc) ExecutionFinished(owner domain.UserID, e domain.Execution) { f(owner, e) }

// pausing is trigger → pause → after, with side, also from the trigger, declared after
// pause: its input is still pending when pause waits.
func pausing() domain.Workflow {
	return domain.Workflow{
		ID: "wf-1", OwnerID: "alice", Version: 3,
		Nodes: []domain.NodeSpec{
			trigger(),
			spec("pause", "test.pause", nil),
			spec("after", "test.capture", nil),
			spec("side", "test.pass", map[string]any{"tag": "side"}),
			spec("late", "test.pass", map[string]any{"tag": "late"}),
		},
		Connections: []domain.Connection{
			mainLink("trigger", "pause"), mainLink("pause", "after"), mainLink("trigger", "side"),
			link("pause", "late", "late", engine.DefaultPort),
		},
	}
}

func TestWaitAndContinue(t *testing.T) {
	ctx := context.Background()
	ex, d := waitingExecutor(t, engine.WaitLimits{MaxWaiting: 5, MaxWait: 24 * time.Hour})

	price := decimal.RequireFromString("0.0200")
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)

	res, err := ex.Execute(ctx, pausing(), "trigger", fired(item("v", price, "at", at, "n", 7, "tags", []any{"a"})))
	require.NoError(t, err)

	e := res.Execution
	assert.Equal(t, domain.ExecutionWaiting, e.Status)
	assert.Equal(t, domain.NodeID("pause"), e.WaitingOn)
	assert.Equal(t, waitNow.Add(time.Hour), e.WaitingUntil)
	assert.True(t, e.FinishedAt.IsZero())
	assert.Equal(t, map[domain.NodeID]domain.NodeRunStatus{"trigger": domain.NodeRunSucceeded, "pause": domain.NodeRunWaiting}, runStatuses(e))
	assert.Empty(t, *d.captured, "nothing after the wait ran")

	assert.Equal(t, domain.ExecutionWaiting, d.saves.last["ex-1"].Status, "saved before the wait refers to it")
	assert.Equal(t, []domain.ExecutionStatus{domain.ExecutionWaiting}, *d.notified)

	rec, err := d.waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)
	assert.Equal(t, domain.NodeID("pause"), rec.NodeID)
	assert.Equal(t, domain.WorkflowID("wf-1"), rec.WorkflowID)
	assert.Equal(t, waitNow.Add(time.Hour), rec.Until)

	res, err = ex.Continue(ctx, rec, engine.Resumption{Payload: map[string]any{"answer": "yes"}})
	require.NoError(t, err)

	e = res.Execution
	assert.Equal(t, domain.ExecutionSucceeded, e.Status)
	assert.Equal(t, 3, e.WorkflowVersion, "the version that started it")
	assert.Empty(t, e.WaitingOn)
	assert.Equal(t, map[domain.NodeID]domain.NodeRunStatus{
		"trigger": domain.NodeRunSucceeded, "pause": domain.NodeRunSucceeded, "after": domain.NodeRunSucceeded,
		"side": domain.NodeRunSucceeded, "late": domain.NodeRunSkipped,
	}, runStatuses(e), "the pending branch ran too")

	pauseRuns := slices.DeleteFunc(slices.Clone(e.Runs), func(r domain.NodeRun) bool { return r.NodeID != "pause" })
	require.Len(t, pauseRuns, 1, "the run that waited is finished in place")
	assert.Equal(t, map[string]int{engine.DefaultPort: 1}, pauseRuns[0].Output)

	// Items came through JSON: exact decimals as text, times in RFC 3339.
	require.Len(t, *d.captured, 1)
	got := (*d.captured)[0].JSON
	assert.Equal(t, "0.02", got["v"])
	assert.Equal(t, "2026-10-01T09:30:00Z", got["at"])
	assert.Equal(t, json.Number("7"), got["n"])
	assert.Equal(t, []any{"a"}, got["tags"])
	assert.Equal(t, "yes", got["answer"])
	assert.Equal(t, json.Number("1"), got["count"], "the node's own state came back")

	assert.Equal(t, domain.ExecutionSucceeded, d.saves.last["ex-1"].Status)
	assert.Equal(t, []domain.ExecutionStatus{domain.ExecutionWaiting, domain.ExecutionSucceeded}, *d.notified)
}

func TestWaitTimesOut(t *testing.T) {
	ctx := context.Background()
	ex, d := waitingExecutor(t, engine.WaitLimits{})

	_, err := ex.Execute(ctx, pausing(), "trigger", fired(item("v", 1)))
	require.NoError(t, err)

	rec, err := d.waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)

	res, err := ex.Continue(ctx, rec, engine.Resumption{TimedOut: true})
	require.NoError(t, err)
	assert.Equal(t, domain.NodeRunSucceeded, runStatuses(res.Execution)["late"])
	assert.Equal(t, domain.NodeRunSkipped, runStatuses(res.Execution)["after"])
}

func TestWaitAgain(t *testing.T) {
	ctx := context.Background()
	ex, d := waitingExecutor(t, engine.WaitLimits{})

	_, err := ex.Execute(ctx, pausing(), "trigger", fired(item("v", 1)))
	require.NoError(t, err)

	rec, err := d.waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)

	res, err := ex.Continue(ctx, rec, engine.Resumption{Payload: map[string]any{"again": true}})
	require.NoError(t, err)
	assert.Equal(t, domain.ExecutionWaiting, res.Execution.Status)
	assert.Equal(t, waitNow.Add(2*time.Hour), res.Execution.WaitingUntil)

	rec, err = d.waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)

	res, err = ex.Continue(ctx, rec, engine.Resumption{Payload: map[string]any{"answer": "now"}})
	require.NoError(t, err)
	assert.Equal(t, domain.ExecutionSucceeded, res.Execution.Status)
	assert.Equal(t, domain.NodeRunSucceeded, runStatuses(res.Execution)["side"], "what was pending is kept across waits")
}

func TestWaitRefused(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name    string
		limits  engine.WaitLimits
		node    string
		before  int // waits the owner has already
		wantErr string
	}{
		{name: "too far ahead", limits: engine.WaitLimits{MaxWait: 30 * time.Minute}, node: "test.pause", wantErr: "wait refused: waits end within 30m0s"},
		{name: "too many waiting", limits: engine.WaitLimits{MaxWaiting: 2}, node: "test.pause", before: 2, wantErr: "wait refused: 2 of your executions are waiting already"},
		{name: "a node that cannot resume", node: "test.cannot", wantErr: "cannot wait here: it cannot wait"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex, d := waitingExecutor(t, tc.limits)
			for i := range tc.before {
				d.waits.recs[domain.ExecutionID(rune('a'+i))] = ports.WaitRecord{OwnerID: "alice"}
			}

			w := domain.Workflow{
				ID: "wf-1", OwnerID: "alice",
				Nodes:       []domain.NodeSpec{trigger(), spec("pause", tc.node, nil)},
				Connections: []domain.Connection{mainLink("trigger", "pause")},
			}

			res, err := ex.Execute(ctx, w, "trigger", fired(item("v", 1)))
			require.NoError(t, err)

			assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
			assert.Contains(t, res.Execution.Error, tc.wantErr)
			assert.Equal(t, domain.NodeRunFailed, runStatuses(res.Execution)["pause"])
			assert.Len(t, d.waits.recs, tc.before, "nothing was kept")
			assert.Equal(t, domain.ExecutionFailed, d.saves.last["ex-1"].Status)
		})
	}

	t.Run("without waits", func(t *testing.T) {
		ex, err := engine.NewExecutor(observability.NoopUtility(), func() *engine.Registry {
			r := testRegistry(t)
			require.NoError(t, r.RegisterNode(pauser{}))

			return r
		}())
		require.NoError(t, err)

		res, err := ex.Execute(ctx, domain.Workflow{
			Nodes:       []domain.NodeSpec{trigger(), spec("pause", "test.pause", nil)},
			Connections: []domain.Connection{mainLink("trigger", "pause")},
		}, "trigger", fired(item("v", 1)))
		require.NoError(t, err)
		assert.Contains(t, res.Execution.Error, "waiting is not available")
	})

	t.Run("too much in flight", func(t *testing.T) {
		ex, _ := waitingExecutor(t, engine.WaitLimits{})

		res, err := ex.Execute(ctx, pausing(), "trigger", fired(item("blob", strings.Repeat("x", 5<<20))))
		require.NoError(t, err)
		assert.Contains(t, res.Execution.Error, "more than the 4096 KiB a wait keeps")
	})
}

func TestCancelWaiting(t *testing.T) {
	ctx := context.Background()
	ex, d := waitingExecutor(t, engine.WaitLimits{})

	_, err := ex.Execute(ctx, pausing(), "trigger", fired(item("v", 1)))
	require.NoError(t, err)

	rec, err := d.waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)

	e, err := ex.Cancel(ctx, rec, "cancelled by you")
	require.NoError(t, err)

	assert.Equal(t, domain.ExecutionCancelled, e.Status)
	assert.Equal(t, "cancelled by you", e.Error)
	assert.Equal(t, waitNow, e.FinishedAt)
	assert.Empty(t, e.WaitingOn)
	assert.Equal(t, domain.NodeRunCancelled, runStatuses(e)["pause"])
	assert.Equal(t, domain.ExecutionCancelled, d.saves.last["ex-1"].Status)
	assert.Equal(t, domain.ExecutionCancelled, (*d.notified)[len(*d.notified)-1])
}
