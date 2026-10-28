package mcpserver_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// overview is 25 workflows, the first trading on paper, its last run failed.
type overview struct{}

func (overview) Overview(_ context.Context, p auth.Principal) (stats.Overview, error) {
	o := stats.Overview{
		Paper: stats.Totals{Workflows: 1, Profit: d("12.4"), Orders: 7},
		Live:  stats.Totals{},
		Day:   domain.RunCounts{Runs: 4, Failed: 1}, Week: domain.RunCounts{Runs: 30, Failed: 2}, At: at,
	}

	for i := range 25 {
		w := stats.Workflow{ID: domain.WorkflowID(fmt.Sprintf("wf-%02d", i)), Name: fmt.Sprintf("workflow %02d", i)}
		if i == 0 {
			w.Active, w.Trading = true, &trading.Result{Mode: domain.ModePaper, Profit: d("12.4"), Realized: d("10"), Unrealized: d("2.4"), Positions: 3, Orders: 7}
			w.Runs = domain.RunStats{Day: domain.RunCounts{Runs: 4, Failed: 1}, Last: &domain.LastRun{ID: "ex-9", Status: domain.ExecutionFailed, StartedAt: at}}
		}

		o.Workflows = append(o.Workflows, w)
	}

	if p.User.ID != alice.ID {
		o.Workflows = nil
	}

	return o, nil
}

func TestStatsTool(t *testing.T) {
	withStats := deps(alice, &trades{}, []domain.Scope{domain.ScopeRead})
	withStats.Stats = overview{}
	agent := connect(t, withStats)

	page, msg := call(t, agent, "get_workflow_stats", nil)
	require.Empty(t, msg)

	list := page["workflows"].([]any)
	require.Len(t, list, 20, "a page of 20 unless asked")
	assert.Equal(t, "20", page["next"])

	first := list[0].(map[string]any)
	assert.Equal(t, "workflow 00", first["name"])
	assert.Equal(t, map[string]any{
		"mode": "paper", "profit": "12.4", "realized": "10", "unrealized": "2.4", "positions": float64(3),
		"volume": "0", "orders": float64(7), "wins": float64(0), "closed": float64(0),
	}, first["trading"])
	assert.Equal(t, "failed", first["lastRun"].(map[string]any)["status"])
	assert.NotContains(t, list[1].(map[string]any), "trading", "it does not trade")

	assert.Equal(t, "12.4", page["paper"].(map[string]any)["profit"])
	assert.Equal(t, "0", page["live"].(map[string]any)["profit"], "live apart")

	rest, _ := call(t, agent, "get_workflow_stats", map[string]any{"cursor": "20"})
	assert.Len(t, rest["workflows"].([]any), 5)
	assert.NotContains(t, rest, "next", "the end")

	one, _ := call(t, agent, "get_workflow_stats", map[string]any{"workflow": "wf-03"})
	require.Len(t, one["workflows"].([]any), 1)
	assert.Equal(t, "workflow 03", one["workflows"].([]any)[0].(map[string]any)["name"])

	_, msg = call(t, agent, "get_workflow_stats", map[string]any{"workflow": "wf-99"})
	assert.NotEmpty(t, msg, "no such workflow of hers")
}
