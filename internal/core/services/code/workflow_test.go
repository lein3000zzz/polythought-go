package code_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
)

func TestWorkflow(t *testing.T) {
	t.Parallel()

	r := &runner{output: `{
		"name": "Spread watch",
		"nodes": [
			{"id": "manual", "type": "trigger.manual"},
			{"id": "tight", "type": "logic.if", "name": "Tight spread", "notes": "Under 2 cents",
			 "params": {"field": "spread", "operator": "lt", "value": "0.02"}, "continueOnFail": true}
		],
		"connections": [{"from": "manual", "fromPort": "main", "to": "tight", "toPort": "main"}]
	}`}

	w, err := code.New(compiler{}, r, code.Data{}).Workflow(context.Background(), "export default workflow(...)")
	require.NoError(t, err)

	assert.Equal(t, domain.Workflow{
		Name: "Spread watch",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"},
			{
				ID: "tight", Type: "logic.if", Name: "Tight spread", Notes: "Under 2 cents",
				Params: map[string]any{"field": "spread", "operator": "lt", "value": "0.02"}, ContinueOnFail: true,
			},
		},
		Connections: []domain.Connection{{From: "manual", FromPort: "main", To: "tight", ToPort: "main"}},
	}, w)

	assert.Equal(t, "workflow: export default workflow(...)", r.job.Source, "compiled as workflow code")
	assert.Equal(t, "__workflow", r.job.Entry)
	assert.Nil(t, r.job.Host, "no data requests")
	assert.Zero(t, r.job.Limits, "the configured limits")
}

func TestWorkflowRefuses(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		output string
		want   string
	}{
		"no default export":     {`null`, "the code exports no workflow"},
		"something else":        {`{"name": "w"}`, "is not a workflow"},
		"something like it":     {`{"name": "w", "nodes": [], "connections": [], "extra": 1}`, "is not a workflow"},
		"the wrong kind inside": {`{"name": "w", "nodes": [{"id": 1}], "connections": []}`, "is not a workflow"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := code.New(compiler{}, &runner{output: tc.output}, code.Data{}).Workflow(context.Background(), "s")

			var ce *domain.CodeError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, domain.CodeNoEntry, ce.Kind)
			assert.Contains(t, ce.Message, tc.want)
		})
	}

	t.Run("code that does not compile does not run", func(t *testing.T) {
		t.Parallel()

		r := &runner{}
		compileErr := &domain.CodeError{Kind: domain.CodeDidNotCompile, Message: "line 1, column 1: nope"}

		_, err := code.New(compiler{err: compileErr}, r, code.Data{}).Workflow(context.Background(), "?")
		require.ErrorIs(t, err, compileErr)
		assert.Empty(t, r.job.Source)
	})
}
