package engine_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// lifeTrigger reports when it starts and stops, and which owner it ran for.
type lifeTrigger struct {
	started chan domain.UserID
	stopped chan struct{}
}

func (lifeTrigger) Type() engine.NodeType {
	return engine.NodeType{Name: "test.life", Outputs: mainPorts()}
}

func (l lifeTrigger) Start(ctx context.Context, tc engine.TriggerContext, _ engine.Emit) error {
	l.started <- tc.OwnerID
	<-ctx.Done()
	l.stopped <- struct{}{}

	return nil
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(waitFor):
		require.FailNow(t, "timed out waiting for "+what)

		var zero T

		return zero
	}
}

func TestSupervisor(t *testing.T) {
	lifeWorkflow := func(id domain.WorkflowID, owner domain.UserID) domain.Workflow {
		return domain.Workflow{ID: id, OwnerID: owner, Nodes: []domain.NodeSpec{spec("t", "test.life", nil)}}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, s *engine.Supervisor, life lifeTrigger)
	}{
		{"start runs triggers for the owner", func(t *testing.T, s *engine.Supervisor, life lifeTrigger) {
			s.Start(lifeWorkflow("wf", "alice"))

			assert.Equal(t, domain.UserID("alice"), receive(t, life.started, "start"))
			assert.True(t, s.Running("wf"))
		}},
		{"starting again replaces the old run first", func(t *testing.T, s *engine.Supervisor, life lifeTrigger) {
			s.Start(lifeWorkflow("wf", "alice"))
			receive(t, life.started, "first start")

			s.Start(lifeWorkflow("wf", "alice"))

			select {
			case <-life.stopped:
			default:
				require.FailNow(t, "the old run must be stopped before Start returns")
			}

			receive(t, life.started, "second start")
		}},
		{"stop ends the run", func(t *testing.T, s *engine.Supervisor, life lifeTrigger) {
			s.Start(lifeWorkflow("wf", "alice"))
			receive(t, life.started, "start")

			s.Stop("wf")
			receive(t, life.stopped, "stop")
			assert.False(t, s.Running("wf"))

			s.Stop("wf") // stopping twice is harmless
		}},
		{"an invalid workflow is not running", func(t *testing.T, s *engine.Supervisor, _ lifeTrigger) {
			s.Start(domain.Workflow{ID: "broken"})

			assert.Eventually(t, func() bool { return !s.Running("broken") }, waitFor, time.Millisecond)
		}},
		{"shutdown stops everything", func(t *testing.T, s *engine.Supervisor, life lifeTrigger) {
			s.Start(lifeWorkflow("a", "alice"))
			s.Start(lifeWorkflow("b", "bob"))
			receive(t, life.started, "a")
			receive(t, life.started, "b")

			ctx, cancel := context.WithTimeout(context.Background(), waitFor)
			defer cancel()

			require.NoError(t, s.Shutdown(ctx))
			assert.False(t, s.Running("a"))
			assert.False(t, s.Running("b"))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			life := lifeTrigger{started: make(chan domain.UserID, 4), stopped: make(chan struct{}, 4)}

			registry := testRegistry(t)
			require.NoError(t, registry.RegisterTrigger(life))

			util := observability.NoopUtility()
			executor, err := engine.NewExecutor(util, registry)
			require.NoError(t, err)

			s := engine.NewSupervisor(context.Background(), engine.NewRunner(util, registry, executor), util.Logger)
			t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

			tt.run(t, s, life)
		})
	}
}

func TestExecutorRecordsAndPassesOwner(t *testing.T) {
	var seenOwner atomic.Value

	registry := testRegistry(t)
	require.NoError(t, registry.RegisterNode(fakeNode{
		typ: engine.NodeType{Name: "test.owner", Inputs: mainPorts(), Outputs: mainPorts()},
		run: func(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
			seenOwner.Store(rc.OwnerID)
			return in, nil
		},
	}))

	wf := domain.Workflow{
		ID: "wf", OwnerID: "alice", Version: 4,
		Nodes:       []domain.NodeSpec{trigger(), spec("n", "test.owner", nil)},
		Connections: []domain.Connection{mainLink("trigger", "n")},
	}

	tests := []struct {
		name       string
		cancelled  bool
		saveErr    error
		wantStatus domain.ExecutionStatus
	}{
		{name: "finished executions are recorded", wantStatus: domain.ExecutionSucceeded},
		{name: "a cancelled execution is still recorded", cancelled: true, wantStatus: domain.ExecutionCancelled},
		{name: "a failed save does not fail the execution", saveErr: errBoom, wantStatus: domain.ExecutionSucceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				saved []domain.Execution
			)

			recorder := portsmocks.NewMockExecutionRecorder(t)
			recorder.EXPECT().Save(mock.Anything, mock.Anything).
				RunAndReturn(func(ctx context.Context, e domain.Execution) error {
					require.NoError(t, ctx.Err(), "saving must not inherit the cancellation")

					mu.Lock()
					saved = append(saved, e)
					mu.Unlock()

					return tt.saveErr
				}).Once()

			executor, err := engine.NewExecutor(observability.NoopUtility(), registry, engine.WithRecorder(recorder))
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			if tt.cancelled {
				cancel()
			}
			defer cancel()

			res, err := executor.Execute(ctx, wf, "trigger", fired(item()))
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, res.Execution.Status)

			require.Len(t, saved, 1)
			assert.Equal(t, res.Execution, saved[0])
			assert.Equal(t, 4, saved[0].WorkflowVersion)

			if !tt.cancelled {
				assert.Equal(t, domain.UserID("alice"), seenOwner.Load())
			}
		})
	}
}

func TestRegistryTypes(t *testing.T) {
	types := testRegistry(t).Types()

	names := make([]string, len(types))
	for i, ti := range types {
		names[i] = ti.Name
	}

	assert.IsIncreasing(t, names)
	assert.Contains(t, names, "test.trigger")

	for _, ti := range types {
		assert.Equal(t, strings.HasPrefix(ti.Name, "test.trigger"), ti.Trigger, ti.Name)
	}
}
