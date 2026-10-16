package engine_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// doubler works out "v * 2" for each item, and remembers how often it was asked.
type doubler struct{ calls int }

func (d *doubler) Evaluate(_ context.Context, e engine.Evaluation) ([][]engine.EvalResult, error) {
	d.calls++

	out := make([][]engine.EvalResult, len(e.Items))
	for i, item := range e.Items {
		out[i] = make([]engine.EvalResult, len(e.Expressions))
		for j := range e.Expressions {
			v, _ := item["v"].(int)
			out[i][j] = engine.EvalResult{Value: v * 2}
		}
	}

	return out, nil
}

func TestExecuteWithExpressions(t *testing.T) {
	ev := &doubler{}

	ex, err := engine.NewExecutor(observability.NoopUtility(), testRegistry(t), engine.WithEvaluator(ev))
	require.NoError(t, err)

	w := domain.Workflow{
		Nodes:       []domain.NodeSpec{trigger(), spec("a", "test.pass", map[string]any{"tag": "{{ v }} and {{ v * 2 }}"})},
		Connections: []domain.Connection{mainLink("trigger", "a")},
	}

	res, err := ex.Execute(context.Background(), w, "trigger", fired(item("v", 1), item("v", 5)))
	require.NoError(t, err)

	var tags []string
	for _, it := range res.Outputs["a"][engine.DefaultPort] {
		tags = append(tags, fmt.Sprint(it.JSON["tag"]))
	}

	assert.Equal(t, []string{"1 and 2", "5 and 10"}, tags, "each item its own")
	assert.Equal(t, 1, ev.calls, "one evaluation for the node's run")
}

// picky doubles v, but throws for v over 3.
type picky struct{}

func (picky) Evaluate(_ context.Context, e engine.Evaluation) ([][]engine.EvalResult, error) {
	out := make([][]engine.EvalResult, len(e.Items))
	for i, item := range e.Items {
		out[i] = make([]engine.EvalResult, len(e.Expressions))
		for j := range e.Expressions {
			if v, _ := item["v"].(int); v > 3 {
				out[i][j] = engine.EvalResult{Err: "RangeError: too big"}
			} else {
				out[i][j] = engine.EvalResult{Value: v * 2}
			}
		}
	}

	return out, nil
}

func TestExpressionErrorPath(t *testing.T) {
	ex, err := engine.NewExecutor(observability.NoopUtility(), testRegistry(t), engine.WithEvaluator(picky{}))
	require.NoError(t, err)

	workflow := func(continueOnFail bool) domain.Workflow {
		a := spec("a", "test.pass", map[string]any{"tag": "{{ v * 2 }}"})
		a.ContinueOnFail = continueOnFail

		return domain.Workflow{
			Nodes:       []domain.NodeSpec{trigger(), a, spec("caught", "test.pass", nil)},
			Connections: []domain.Connection{mainLink("trigger", "a"), link("a", engine.ErrorPort, "caught", engine.DefaultPort)},
		}
	}

	in := fired(item("v", 1), item("v", 5), item("v", 2))

	t.Run("the run fails, as for any failed item", func(t *testing.T) {
		w := workflow(false)
		w.Connections = w.Connections[:1]

		res, err := ex.Execute(context.Background(), w, "trigger", in)
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
		assert.Equal(t, `node "a": 1 items failed: param "tag": {{ v * 2 }}: RangeError: too big`, res.Execution.Error)
	})

	t.Run("with continue on failure, the item takes the error path", func(t *testing.T) {
		res, err := ex.Execute(context.Background(), workflow(true), "trigger", in)
		require.NoError(t, err)

		out := res.Outputs["a"]

		var tags []any
		var sources []int
		for _, it := range out[engine.DefaultPort] {
			tags = append(tags, it.JSON["tag"])
			sources = append(sources, it.Source[0].Index)
		}

		assert.Equal(t, []any{"2", "4"}, tags, "the others ran")
		assert.Equal(t, []int{0, 2}, sources, "pointing at their place in the input")

		require.Len(t, out[engine.ErrorPort], 1)
		failed := out[engine.ErrorPort][0]
		assert.Equal(t, 5, failed.JSON["v"])
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, failed.Source)
		require.ErrorContains(t, failed.Err, "RangeError: too big")

		assert.Len(t, res.Outputs["caught"][engine.DefaultPort], 1, "and on to what is connected there")
	})
}
