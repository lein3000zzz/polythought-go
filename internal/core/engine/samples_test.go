package engine_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func runOf(t *testing.T, e domain.Execution, id domain.NodeID) domain.NodeRun {
	t.Helper()

	for _, r := range e.Runs {
		if r.NodeID == id {
			return r
		}
	}

	t.Fatalf("no run of node %q", id)

	return domain.NodeRun{}
}

func items(n int, kv ...any) []engine.Item {
	out := make([]engine.Item, n)
	for i := range out {
		out[i] = item(append([]any{"i", i}, kv...)...)
	}

	return out
}

func TestSamples(t *testing.T) {
	failing := domain.Workflow{
		Nodes:       []domain.NodeSpec{trigger(), {ID: "f", Type: "test.fail", ContinueOnFail: true}},
		Connections: []domain.Connection{mainLink("trigger", "f")},
	}

	big := strings.Repeat("x", 7<<10) // three of these do not fit the 16 KiB budget

	tests := []struct {
		name     string
		workflow domain.Workflow
		output   engine.Ports
		opts     []engine.ExecuteOption
		check    func(t *testing.T, e domain.Execution)
	}{
		{
			name:     "not kept unless asked",
			workflow: failing,
			output:   fired(items(2)...),
			check: func(t *testing.T, e domain.Execution) {
				for _, r := range e.Runs {
					assert.Nil(t, r.Samples, "node %s", r.NodeID)
				}
			},
		},
		{
			name:     "the first five items of each port",
			workflow: failing,
			output:   fired(append(items(7), items(3, "bad", true)...)...),
			opts:     []engine.ExecuteOption{engine.KeepSamples()},
			check: func(t *testing.T, e domain.Execution) {
				trig := runOf(t, e, "trigger")
				assert.Len(t, trig.Samples[engine.DefaultPort], 5)
				assert.Equal(t, 10, trig.Output[engine.DefaultPort], "counts stay complete")
				assert.JSONEq(t, `{"i":0}`, string(trig.Samples[engine.DefaultPort][0].JSON))

				f := runOf(t, e, "f")
				assert.Len(t, f.Samples[engine.DefaultPort], 5)
				require.Len(t, f.Samples[engine.ErrorPort], 3)
				assert.JSONEq(t, `{"i":0,"bad":true}`, string(f.Samples[engine.ErrorPort][0].JSON))
				assert.Equal(t, errBoom.Error(), f.Samples[engine.ErrorPort][0].Error)
			},
		},
		{
			name:     "failed items get the budget first",
			workflow: failing,
			output:   fired(append(items(2, "pad", big), items(2, "pad", big, "bad", true)...)...),
			opts:     []engine.ExecuteOption{engine.KeepSamples()},
			check: func(t *testing.T, e domain.Execution) {
				f := runOf(t, e, "f")
				assert.Len(t, f.Samples[engine.ErrorPort], 2)
				assert.Empty(t, f.Samples[engine.DefaultPort])
				assert.Equal(t, 2, f.Output[engine.DefaultPort])

				trig := runOf(t, e, "trigger")
				assert.Len(t, trig.Samples[engine.DefaultPort], 2, "a port stops at the first item that does not fit")
			},
		},
		{
			name: "an item that is not JSON says so",
			workflow: domain.Workflow{
				Nodes:       []domain.NodeSpec{trigger(), spec("p", "test.pass", nil)},
				Connections: []domain.Connection{mainLink("trigger", "p")},
			},
			output: fired(item("v", math.Inf(1))),
			opts:   []engine.ExecuteOption{engine.KeepSamples()},
			check: func(t *testing.T, e domain.Execution) {
				s := runOf(t, e, "trigger").Samples[engine.DefaultPort]
				require.Len(t, s, 1)
				assert.Nil(t, s[0].JSON)
				assert.Contains(t, s[0].Error, "could not be encoded as JSON")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := newExecutor(t, observability.NoopUtility()).
				Execute(context.Background(), tt.workflow, "trigger", tt.output, tt.opts...)
			require.NoError(t, err)

			tt.check(t, res.Execution)
		})
	}
}
