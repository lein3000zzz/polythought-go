package engine_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// emitter fires its "times" param worth of executions, then fails or waits for cancellation.
type emitter struct {
	fail error
}

func (emitter) Type() engine.NodeType {
	return engine.NodeType{
		Name:    "test.emitter",
		Outputs: mainPorts(),
		Params:  []engine.ParamSpec{{Name: "times", Kind: engine.ParamNumber, Default: 1}},
	}
}

func (e emitter) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	times, err := tc.Params.Int("times")
	if err != nil {
		return err
	}

	for range times {
		if err := emit(ctx, fired(item("from", string(tc.NodeID)))); err != nil {
			return err
		}
	}

	if e.fail != nil {
		return e.fail
	}

	<-ctx.Done()

	return nil
}

func TestRunner(t *testing.T) {
	tests := []struct {
		name     string
		trigger  emitter
		workflow domain.Workflow
		// wantRuns is how many times the counting node runs before the test cancels.
		wantRuns int64
		wantErr  error
		errText  string
	}{
		{
			name: "every emit becomes an execution",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{spec("src", "test.emitter", map[string]any{"times": 3}), spec("count", "test.count", nil)},
				Connections: []domain.Connection{mainLink("src", "count")},
			},
			wantRuns: 3,
		},
		{
			name: "trigger defaults apply",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{spec("src", "test.emitter", nil), spec("count", "test.count", nil)},
				Connections: []domain.Connection{mainLink("src", "count")},
			},
			wantRuns: 1,
		},
		{
			name: "all triggers start",
			workflow: domain.Workflow{
				Nodes: []domain.NodeSpec{
					spec("a", "test.emitter", map[string]any{"times": 2}),
					spec("b", "test.emitter", map[string]any{"times": 3}),
					spec("count", "test.count", nil),
				},
				Connections: []domain.Connection{mainLink("a", "count"), mainLink("b", "count")},
			},
			wantRuns: 5,
		},
		{
			name:    "a failing trigger is reported",
			trigger: emitter{fail: errBoom},
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{spec("src", "test.emitter", map[string]any{"times": 1}), spec("count", "test.count", nil)},
				Connections: []domain.Connection{mainLink("src", "count")},
			},
			wantRuns: 1,
			wantErr:  errBoom,
			errText:  `trigger "src"`,
		},
		{
			name:     "an invalid workflow does not start",
			workflow: domain.Workflow{Nodes: []domain.NodeSpec{spec("count", "test.count", nil)}},
			wantErr:  domain.ErrInvalidWorkflow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runs atomic.Int64

			registry := testRegistry(t)
			require.NoError(t, registry.RegisterTrigger(tt.trigger))
			require.NoError(t, registry.RegisterNode(fakeNode{
				typ: engine.NodeType{Name: "test.count", Inputs: mainPorts(), Outputs: mainPorts()},
				run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
					runs.Add(1)
					return in, nil
				},
			}))

			executor, err := engine.NewExecutor(observability.NoopUtility(), registry)
			require.NoError(t, err)

			runner := engine.NewRunner(observability.NoopUtility(), registry, executor)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- runner.Run(ctx, tt.workflow) }()

			require.Eventually(t, func() bool { return runs.Load() >= tt.wantRuns }, waitFor, time.Millisecond)

			if tt.wantErr == nil {
				cancel()
			}

			select {
			case err := <-done:
				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
					assert.Contains(t, err.Error(), tt.errText)
				} else {
					require.NoError(t, err)
				}
			case <-time.After(waitFor):
				require.FailNow(t, "runner did not return")
			}

			assert.Equal(t, tt.wantRuns, runs.Load())
		})
	}
}
