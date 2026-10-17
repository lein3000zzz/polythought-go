package tscompiler_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
)

// workflowOf runs workflow code as Apply does: compiled with the builder, run in the
// sandbox with the SDK.
func workflowOf(t *testing.T, source string) (domain.Workflow, error) {
	t.Helper()

	return code.New(compiler(t), withSDKRunner(t), code.Data{}).Workflow(context.Background(), source)
}

func TestWorkflowCode(t *testing.T) {
	t.Parallel()

	w, err := workflowOf(t, `
		import { workflow } from "@polythought/workflow"
		import { dec } from "@polythought/sdk"
		import type { PolymarketOrderbookItem } from "@polythought/sdk"

		const threshold: string = dec("0.01").times(2).toString()

		export default workflow("Spread watch", (w) => {
			const manual = w.trigger("manual", "trigger.manual")
			const tight = w.node("tight", "logic.if", { field: "spread", operator: "lt", value: threshold }, { name: "Tight spread" })
			const log = w.node("log", "output.log", { message: "Tight: {{ question }}" })

			manual.to(tight)
			tight.port("true").to(log)
		})`)
	require.NoError(t, err)

	assert.Equal(t, domain.Workflow{
		Name: "Spread watch",
		Nodes: []domain.NodeSpec{
			{ID: "manual", Type: "trigger.manual"},
			{ID: "tight", Type: "logic.if", Name: "Tight spread", Params: map[string]any{"field": "spread", "operator": "lt", "value": "0.02"}},
			{ID: "log", Type: "output.log", Params: map[string]any{"message": "Tight: {{ question }}"}},
		},
		Connections: []domain.Connection{
			{From: "manual", FromPort: "main", To: "tight", ToPort: "main"},
			{From: "tight", FromPort: "true", To: "log", ToPort: "main"},
		},
	}, w, "the SDK's dec() works, types go")
}

func TestWorkflowCodeFails(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		source string
		kind   domain.CodeErrorKind
		want   string
	}{
		"importing anything else": {
			source: `import { readFile } from "node:fs"; export default readFile`, // imports used only as types go
			kind:   domain.CodeDidNotCompile,
			want:   `only @polythought/sdk and @polythought/workflow can be imported, not "node:fs"`,
		},
		"not compiling": {
			source: `export default workflow("w", (w) => {`,
			kind:   domain.CodeDidNotCompile,
			want:   "line 1",
		},
		"what the builder refuses": {
			source: `
				import { workflow } from "@polythought/workflow"
				export default workflow("w", (w) => {
					w.trigger("a", "trigger.manual")
					w.node("a", "output.log")
				})`,
			kind: domain.CodeThrew,
			want: `node "a" is declared twice`,
		},
		"asking for data": {
			source: `
				import { workflow } from "@polythought/workflow"
				import { markets } from "@polythought/sdk"
				export default markets.search({ query: "election" }).then(() => workflow("w", () => {}))`,
			kind: domain.CodeThrew,
			want: "markets.search: this code cannot ask for data",
		},
		"exporting nothing": {
			source: `const w = 1`,
			kind:   domain.CodeNoEntry,
			want:   "the code exports no workflow",
		},
		"exporting no workflow": {
			source: `export const w = 1`,
			kind:   domain.CodeNoEntry,
			want:   "the code exports no workflow",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := workflowOf(t, tc.source)

			var ce *domain.CodeError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, tc.kind, ce.Kind, ce.Message)
			assert.Contains(t, ce.Message, tc.want)
		})
	}

	t.Run("code nodes may not import the builder", func(t *testing.T) {
		t.Parallel()

		_, err := compiler(t).Compile(context.Background(), `import { workflow } from "@polythought/workflow"; export const run = () => workflow`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `only @polythought/sdk can be imported, not "@polythought/workflow"`)
	})
}
