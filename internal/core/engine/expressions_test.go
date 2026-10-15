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
