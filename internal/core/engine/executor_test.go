package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var epoch = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// tickingClock advances one second per call, so durations are deterministic.
func tickingClock() func() time.Time {
	now := epoch

	return func() time.Time {
		now = now.Add(time.Second)
		return now
	}
}

func newExecutor(t *testing.T, util *observability.Utility) *engine.Executor {
	t.Helper()

	ex, err := engine.NewExecutor(util, testRegistry(t),
		engine.WithClock(tickingClock()),
		engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
	)
	require.NoError(t, err)

	return ex
}

func trigger() domain.NodeSpec {
	return spec("trigger", "test.trigger", nil)
}

func fired(items ...engine.Item) engine.Ports {
	return engine.Ports{engine.DefaultPort: items}
}

func runStatuses(e domain.Execution) map[domain.NodeID]domain.NodeRunStatus {
	out := make(map[domain.NodeID]domain.NodeRunStatus, len(e.Runs))
	for _, r := range e.Runs {
		out[r.NodeID] = r.Status
	}

	return out
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name     string
		workflow domain.Workflow
		output   engine.Ports
		cancel   bool

		wantStatus domain.ExecutionStatus
		wantRuns   map[domain.NodeID]domain.NodeRunStatus
		wantError  string
		check      func(t *testing.T, res engine.Result)
	}{
		{
			name: "linear chain with configured and default params",
			workflow: domain.Workflow{
				ID:          "wf",
				Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", map[string]any{"tag": "custom"}), spec("b", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "a"), mainLink("a", "b")},
			},
			output:     fired(item("n", 1), item("n", 2)),
			wantStatus: domain.ExecutionSucceeded,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "a": "succeeded", "b": "succeeded"},
			check: func(t *testing.T, res engine.Result) {
				a := res.Outputs["a"][engine.DefaultPort]
				require.Len(t, a, 2)
				assert.Equal(t, "custom", a[0].JSON["tag"])
				assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, a[1].Source)

				b := res.Outputs["b"][engine.DefaultPort]
				require.Len(t, b, 2)
				assert.Equal(t, "default", b[0].JSON["tag"])
				assert.Equal(t, 1, b[0].JSON["n"])
			},
		},
		{
			name: "branches route items and unreached nodes are skipped",
			workflow: domain.Workflow{
				Nodes: []domain.NodeSpec{
					trigger(), spec("if", "test.if", map[string]any{"threshold": "0.5"}),
					spec("high", "test.pass", nil), spec("low", "test.pass", nil),
				},
				Connections: []domain.Connection{
					mainLink("trigger", "if"),
					link("if", "true", "high", engine.DefaultPort),
					link("if", "false", "low", engine.DefaultPort),
				},
			},
			output:     fired(item("v", 0.9), item("v", "0.7")),
			wantStatus: domain.ExecutionSucceeded,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "if": "succeeded", "high": "succeeded", "low": "skipped"},
			check: func(t *testing.T, res engine.Result) {
				assert.Len(t, res.Outputs["high"][engine.DefaultPort], 2)
				assert.Nil(t, res.Outputs["low"])
			},
		},
		{
			name: "merge combines two input ports",
			workflow: domain.Workflow{
				Nodes: []domain.NodeSpec{trigger(), spec("x", "test.pass", nil), spec("y", "test.pass", nil), spec("merge", "test.merge", nil)},
				Connections: []domain.Connection{
					mainLink("trigger", "x"), mainLink("trigger", "y"),
					link("x", engine.DefaultPort, "merge", "a"), link("y", engine.DefaultPort, "merge", "b"),
				},
			},
			output:     fired(item("n", 1)),
			wantStatus: domain.ExecutionSucceeded,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "x": "succeeded", "y": "succeeded", "merge": "succeeded"},
			check: func(t *testing.T, res engine.Result) {
				assert.Len(t, res.Outputs["merge"][engine.DefaultPort], 2)

				run := res.Execution.Runs[len(res.Execution.Runs)-1]
				assert.Equal(t, map[string]int{"a": 1, "b": 1}, run.Input)
			},
		},
		{
			name: "failed items stop the execution without continueOnFail",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("check", "test.fail", nil), spec("after", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "check"), mainLink("check", "after")},
			},
			output:     fired(item("bad", true), item("bad", false)),
			wantStatus: domain.ExecutionFailed,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "check": "failed"},
			wantError:  `node "check": 1 items failed: boom`,
		},
		{
			name: "failed items take the error branch with continueOnFail",
			workflow: domain.Workflow{
				Nodes: []domain.NodeSpec{
					trigger(),
					{ID: "check", Type: "test.fail", ContinueOnFail: true},
					spec("ok", "test.pass", nil), spec("recover", "test.pass", map[string]any{"tag": "recovered"}),
				},
				Connections: []domain.Connection{
					mainLink("trigger", "check"),
					mainLink("check", "ok"),
					link("check", engine.ErrorPort, "recover", engine.DefaultPort),
				},
			},
			output:     fired(item("bad", true), item("bad", false)),
			wantStatus: domain.ExecutionSucceeded,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "check": "succeeded", "ok": "succeeded", "recover": "succeeded"},
			check: func(t *testing.T, res engine.Result) {
				failed := res.Outputs["check"][engine.ErrorPort]
				require.Len(t, failed, 1)
				require.ErrorIs(t, failed[0].Err, errBoom)

				recovered := res.Outputs["recover"][engine.DefaultPort]
				require.Len(t, recovered, 1)
				assert.Equal(t, true, recovered[0].JSON["bad"], "the failed item keeps its data")
			},
		},
		{
			name: "node error fails the execution",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("broken", "test.error", nil), spec("after", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "broken"), mainLink("broken", "after")},
			},
			output:     fired(item()),
			wantStatus: domain.ExecutionFailed,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "broken": "failed"},
			wantError:  `node "broken": boom`,
		},
		{
			name: "node panic is contained",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("bad", "test.panic", nil)},
				Connections: []domain.Connection{mainLink("trigger", "bad")},
			},
			output:     fired(item()),
			wantStatus: domain.ExecutionFailed,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "bad": "failed"},
			wantError:  `node "bad": node panicked: kaboom`,
		},
		{
			name: "cancellation",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("wait", "test.wait", nil), spec("after", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "wait"), mainLink("wait", "after")},
			},
			output:     fired(item()),
			cancel:     true,
			wantStatus: domain.ExecutionCancelled,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "wait": "failed"},
			wantError:  "context canceled",
		},
		{
			name: "empty trigger output skips everything",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "a")},
			},
			output:     engine.Ports{},
			wantStatus: domain.ExecutionSucceeded,
			wantRuns:   map[domain.NodeID]domain.NodeRunStatus{"trigger": "succeeded", "a": "skipped"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if tt.cancel {
				time.AfterFunc(20*time.Millisecond, cancel)
			}

			res, err := newExecutor(t, observability.NoopUtility()).Execute(ctx, tt.workflow, "trigger", tt.output)
			require.NoError(t, err)

			exec := res.Execution
			assert.Equal(t, domain.ExecutionID("ex-1"), exec.ID)
			assert.Equal(t, domain.NodeID("trigger"), exec.TriggeredBy)
			assert.Equal(t, tt.wantStatus, exec.Status)
			assert.Equal(t, tt.wantRuns, runStatuses(exec))
			assert.Positive(t, exec.Duration())

			if tt.wantError == "" {
				assert.Empty(t, exec.Error)
			} else {
				assert.Contains(t, exec.Error, tt.wantError)
			}

			if tt.check != nil {
				tt.check(t, res)
			}
		})
	}
}

func TestExecuteRejects(t *testing.T) {
	valid := domain.Workflow{
		Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", nil)},
		Connections: []domain.Connection{mainLink("trigger", "a")},
	}

	tests := []struct {
		name     string
		workflow domain.Workflow
		trigger  domain.NodeID
		wantErr  error
	}{
		{name: "an empty workflow", workflow: domain.Workflow{}, trigger: "trigger", wantErr: engine.ErrNotTrigger},
		{name: "start from a regular node", workflow: valid, trigger: "a", wantErr: engine.ErrNotTrigger},
		{name: "start from an unknown node", workflow: valid, trigger: "ghost", wantErr: engine.ErrNotTrigger},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newExecutor(t, observability.NoopUtility()).Execute(context.Background(), tt.workflow, tt.trigger, fired(item()))

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

// A saved workflow that no longer holds, as when a custom node it uses changed, fails
// where its owner sees it, before any node runs.
func TestExecuteFailsWhatNoLongerHolds(t *testing.T) {
	tests := []struct {
		name      string
		workflow  domain.Workflow
		wantError string
	}{
		{
			name: "a param of the wrong type",
			workflow: domain.Workflow{
				ID:          "wf",
				Nodes:       []domain.NodeSpec{trigger(), spec("if", "test.if", map[string]any{"threshold": true})},
				Connections: []domain.Connection{mainLink("trigger", "if")},
			},
			wantError: `node "if": param "threshold"`,
		},
		{
			name: "a node type gone",
			workflow: domain.Workflow{
				ID:          "wf",
				Nodes:       []domain.NodeSpec{trigger(), spec("split", "custom.split", nil)},
				Connections: []domain.Connection{mainLink("trigger", "split")},
			},
			wantError: `node "split": unknown node type "custom.split"`,
		},
		{
			name: "a port gone",
			workflow: domain.Workflow{
				ID:          "wf",
				Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", nil), spec("b", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "a"), link("a", "wide", "b", engine.DefaultPort)},
			},
			wantError: `node "a" (test.pass) has no output port "wide"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			saved := &saves{last: map[domain.ExecutionID]domain.Execution{}}

			ex, err := engine.NewExecutor(observability.NoopUtility(), testRegistry(t),
				engine.WithClock(tickingClock()),
				engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
				engine.WithRecorder(saved),
			)
			require.NoError(t, err)

			res, err := ex.Execute(context.Background(), tt.workflow, "trigger", fired(item()))
			require.NoError(t, err)

			assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
			assert.Contains(t, res.Execution.Error, tt.wantError)
			assert.Empty(t, res.Execution.Runs, "no node ran, the trigger's included")
			assert.Equal(t, domain.ExecutionFailed, saved.last["ex-1"].Status, "recorded")
		})
	}
}

func TestExecuteMetrics(t *testing.T) {
	tests := []struct {
		name           string
		nodeType       string
		wantLabel      string // the node type's operation, when not its name
		wantExecStatus string
		wantNodeStatus string
	}{
		{name: "success", nodeType: "test.pass", wantExecStatus: metrics.StatusSuccess, wantNodeStatus: metrics.StatusSuccess},
		{name: "node failure", nodeType: "test.error", wantExecStatus: metrics.StatusError, wantNodeStatus: metrics.StatusError},
		{
			name: "custom nodes count together", nodeType: "custom.split", wantLabel: "custom",
			wantExecStatus: metrics.StatusSuccess, wantNodeStatus: metrics.StatusSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execTotal := metricsmocks.NewMockCounter(t)
			execTotal.EXPECT().Inc(metrics.Labels{"operation": "execute_workflow", "status": tt.wantExecStatus}).Return(nil).Once()

			label := tt.nodeType
			if tt.wantLabel != "" {
				label = tt.wantLabel
			}

			nodeTotal := metricsmocks.NewMockCounter(t)
			nodeTotal.EXPECT().Inc(metrics.Labels{"operation": label, "status": tt.wantNodeStatus}).Return(nil).Once()

			util := newMockedUtil(t, map[string]*metricsmocks.MockCounter{
				"engine_executions/operations_total": execTotal,
				"engine_nodes/operations_total":      nodeTotal,
			})

			wf := domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("n", tt.nodeType, nil)},
				Connections: []domain.Connection{mainLink("trigger", "n")},
			}

			r := testRegistry(t)
			require.NoError(t, r.RegisterNode(splitter()))

			ex, err := engine.NewExecutor(util, r)
			require.NoError(t, err)

			_, err = ex.Execute(context.Background(), wf, "trigger", fired(item()))
			require.NoError(t, err)
		})
	}
}

func TestExecuteNotifies(t *testing.T) {
	var order []string

	recorder := portsmocks.NewMockExecutionRecorder(t)
	recorder.EXPECT().Save(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, domain.Execution) error {
			order = append(order, "recorded")
			return nil
		}).Once()

	notifier := portsmocks.NewMockExecutionNotifier(t)
	notifier.EXPECT().ExecutionFinished(domain.UserID("alice"), mock.MatchedBy(func(e domain.Execution) bool {
		return e.ID == "ex-1" && e.Status == domain.ExecutionSucceeded
	})).Run(func(domain.UserID, domain.Execution) { order = append(order, "notified") }).Once()

	another := portsmocks.NewMockExecutionNotifier(t)
	another.EXPECT().ExecutionFinished(domain.UserID("alice"), mock.Anything).
		Run(func(domain.UserID, domain.Execution) { order = append(order, "notified again") }).Once()

	ex, err := engine.NewExecutor(observability.NoopUtility(), testRegistry(t),
		engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
		engine.WithRecorder(recorder),
		engine.WithNotifier(notifier),
		engine.WithNotifier(another),
	)
	require.NoError(t, err)

	wf := domain.Workflow{ID: "wf-1", OwnerID: "alice", Nodes: []domain.NodeSpec{trigger()}}
	_, err = ex.Execute(context.Background(), wf, "trigger", fired(item("v", 1)))
	require.NoError(t, err)

	assert.Equal(t, []string{"recorded", "notified", "notified again"}, order, "listeners can load what they hear about")
}

// A run names the agent or token behind it (D407): its workflow's, unless it says another,
// the user's zero one included.
func TestExecuteBy(t *testing.T) {
	ctx := context.Background()
	ex := newExecutor(t, observability.NoopUtility())

	switchedOn := domain.Actor{GrantID: "g-1", Name: "Claude Code", How: domain.ActorSwitchedOn}
	w := domain.Workflow{ID: "wf", Nodes: []domain.NodeSpec{trigger()}, ActedBy: switchedOn}

	res, err := ex.Execute(ctx, w, "trigger", fired(item("n", 1)))
	require.NoError(t, err)
	assert.Equal(t, switchedOn, res.Execution.By, "its trigger's run, the workflow's")

	ran := domain.Actor{TokenID: "t-1", Name: "deploy", How: domain.ActorRan}
	res, err = ex.Execute(ctx, w, "trigger", fired(item("n", 1)), engine.By(ran))
	require.NoError(t, err)
	assert.Equal(t, ran, res.Execution.By)

	res, err = ex.Execute(ctx, w, "trigger", fired(item("n", 1)), engine.By(domain.Actor{}))
	require.NoError(t, err)
	assert.True(t, res.Execution.By.IsUser(), "run by the user by hand")
}
