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

// gate answers as told and remembers who it was asked about.
type gate struct {
	allowed bool
	err     error
	asked   []domain.UserID
}

func (g *gate) CodeAllowed(_ context.Context, owner domain.UserID) (bool, error) {
	g.asked = append(g.asked, owner)
	return g.allowed, g.err
}

// codeRegistry has a code node, which counts its runs, and one that waits.
func codeRegistry(t *testing.T, ran *int) *engine.Registry {
	t.Helper()

	r := testRegistry(t)
	require.NoError(t, r.RegisterNode(pauser{}))
	require.NoError(t, r.RegisterNode(fakeNode{
		typ: engine.NodeType{Name: "test.code", Inputs: mainPorts(), Outputs: mainPorts(), Code: true},
		run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
			*ran++
			return in, nil
		},
	}))

	return r
}

func withCode(nodes ...domain.NodeSpec) domain.Workflow {
	w := domain.Workflow{ID: "wf-1", OwnerID: "alice", Nodes: append([]domain.NodeSpec{trigger()}, nodes...)}

	from := domain.NodeID("trigger")
	for _, n := range nodes {
		w.Connections = append(w.Connections, mainLink(from, n.ID))
		from = n.ID
	}

	return w
}

func TestCheckCodeAccess(t *testing.T) {
	failing := errors.New("db down")

	tests := []struct {
		name     string
		workflow domain.Workflow
		gate     gate
		asks     bool
		want     []domain.NodeID // the nodes refused
		wantErr  error
	}{
		{
			name:     "no code nodes: nobody is asked",
			workflow: withCode(spec("a", "test.pass", nil)),
		},
		{
			name:     "an owner who may write code saves it",
			workflow: withCode(spec("c1", "test.code", nil)),
			gate:     gate{allowed: true}, asks: true,
		},
		{
			name:     "one who may not is told about each code node",
			workflow: withCode(spec("c1", "test.code", nil), spec("a", "test.pass", nil), spec("c2", "test.code", nil)),
			asks:     true, want: []domain.NodeID{"c1", "c2"},
		},
		{
			name:     "the gate failing is its own error",
			workflow: withCode(spec("c1", "test.code", nil)),
			gate:     gate{err: failing}, asks: true, wantErr: failing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran int

			r := codeRegistry(t, &ran)

			err := r.CheckCodeAccess(t.Context(), tt.workflow, &tt.gate)

			if tt.asks {
				assert.Equal(t, []domain.UserID{"alice"}, tt.gate.asked)
			} else {
				assert.Empty(t, tt.gate.asked)
			}

			switch {
			case tt.wantErr != nil:
				assert.ErrorIs(t, err, tt.wantErr)

				var v *domain.ValidationError
				assert.NotErrorAs(t, err, &v)
			case tt.want == nil:
				assert.NoError(t, err)
			default:
				var v *domain.ValidationError
				require.ErrorAs(t, err, &v)
				assert.ErrorIs(t, err, domain.ErrCodeOff)

				var refused []domain.NodeID
				for _, p := range v.Problems {
					refused = append(refused, p.NodeID)
				}

				assert.Equal(t, tt.want, refused)
				assert.Equal(t, `node "c1": an admin has turned code off for you`, v.Problems[0].Message)
			}
		})
	}
}

func TestExecuteChecksCodeAccess(t *testing.T) {
	tests := []struct {
		name       string
		workflow   domain.Workflow
		gate       gate
		wantStatus domain.ExecutionStatus
		wantError  string
		wantRan    int
		unasked    bool
	}{
		{
			name:     "an owner who may run code runs it",
			workflow: withCode(spec("c1", "test.code", nil)),
			gate:     gate{allowed: true}, wantStatus: domain.ExecutionSucceeded, wantRan: 1,
		},
		{
			name:     "one who may not fails at once, with no node run",
			workflow: withCode(spec("a", "test.pass", nil), spec("c1", "test.code", nil), spec("c2", "test.code", nil)),
			// The trigger's items never reach a, though a is not code.
			wantStatus: domain.ExecutionFailed,
			wantError:  "an admin has turned code off for you, so the workflow does not run: it has code nodes (c1, c2)",
		},
		{
			name:       "without code nodes it runs, and nobody is asked",
			workflow:   withCode(spec("a", "test.pass", nil)),
			wantStatus: domain.ExecutionSucceeded, unasked: true,
		},
		{
			name:       "the gate failing fails the execution",
			workflow:   withCode(spec("c1", "test.code", nil)),
			gate:       gate{err: errors.New("db down")},
			wantStatus: domain.ExecutionFailed,
			wantError:  "could not tell whether the owner may run code: db down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ran int

			saved := &saves{last: map[domain.ExecutionID]domain.Execution{}}

			ex, err := engine.NewExecutor(observability.NoopUtility(), codeRegistry(t, &ran),
				engine.WithClock(tickingClock()),
				engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
				engine.WithRecorder(saved),
				engine.WithCodeGate(&tt.gate),
			)
			require.NoError(t, err)

			res, err := ex.Execute(t.Context(), tt.workflow, "trigger", fired(item("n", 1)))
			require.NoError(t, err)

			exec := res.Execution
			assert.Equal(t, tt.wantStatus, exec.Status, exec.Error)
			assert.Equal(t, tt.wantRan, ran)
			assert.Equal(t, tt.wantStatus, saved.last["ex-1"].Status, "recorded")

			if tt.wantError != "" {
				assert.Equal(t, tt.wantError, exec.Error)
				assert.Empty(t, exec.Runs, "the trigger's run is not recorded either")
			}

			if tt.unasked {
				assert.Empty(t, tt.gate.asked)
			} else {
				assert.Equal(t, []domain.UserID{"alice"}, tt.gate.asked)
			}
		})
	}
}

// A workflow that waited while its owner could run code does not carry on once they cannot.
func TestContinueChecksCodeAccess(t *testing.T) {
	ctx := context.Background()

	var ran int

	g := &gate{allowed: true}
	waits := &memWaits{recs: map[domain.ExecutionID]ports.WaitRecord{}}

	ex, err := engine.NewExecutor(observability.NoopUtility(), codeRegistry(t, &ran),
		engine.WithClock(func() time.Time { return waitNow }),
		engine.WithExecutionIDs(func() domain.ExecutionID { return "ex-1" }),
		engine.WithWaits(waits, func() engine.WaitLimits { return engine.WaitLimits{} }),
		engine.WithCodeGate(g),
	)
	require.NoError(t, err)

	res, err := ex.Execute(ctx, withCode(spec("pause", "test.pause", nil), spec("c1", "test.code", nil)), "trigger", fired(item("n", 1)))
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

	g.allowed = false

	rec, err := waits.Take(ctx, "alice", "ex-1")
	require.NoError(t, err)

	res, err = ex.Continue(ctx, rec, engine.Resumption{Payload: map[string]any{"answer": "yes"}})
	require.NoError(t, err)

	assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
	assert.Contains(t, res.Execution.Error, "an admin has turned code off for you")
	assert.Zero(t, ran)
	assert.Equal(t, []domain.UserID{"alice", "alice"}, g.asked, "asked when it started and again when it carried on")
}
