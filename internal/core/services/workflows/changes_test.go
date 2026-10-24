package workflows

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// A change waiting for its user says what it changes, node by node and setting by setting,
// and of a personal setting only that it changed (A3).
func TestChanges(t *testing.T) {
	s := &Service{personal: func(nodeType string) []string {
		if nodeType == "notify.webhook" {
			return []string{"url"}
		}

		return nil
	}}

	before := domain.Workflow{
		Name: "Spreads",
		Nodes: []domain.NodeSpec{
			{ID: "start", Type: "trigger.manual"},
			{ID: "gate", Type: "logic.if", Params: map[string]any{"limit": 1, "field": "price"}},
			{ID: "hook", Type: "notify.webhook", Params: map[string]any{"url": "https://a.example"}},
			{ID: "old", Type: "util.noop"},
		},
		Connections: []domain.Connection{
			{From: "start", FromPort: "out", To: "gate", ToPort: "in"},
			{From: "gate", FromPort: "true", To: "old", ToPort: "in"},
		},
	}

	t.Run("each change, a line each", func(t *testing.T) {
		after := before
		after.Name = "Spreads, live"
		after.Nodes = []domain.NodeSpec{
			{ID: "start", Type: "trigger.manual", Position: domain.CanvasPosition{X: 9}},
			{ID: "gate", Type: "logic.if", Name: "Gate", Params: map[string]any{"limit": 2, "op": "gt"}, ContinueOnFail: true},
			{ID: "hook", Type: "notify.webhook", Params: map[string]any{"url": "https://b.example"}},
			{ID: "buy", Type: "polymarket.order", Params: map[string]any{"size": 50, "url": strings.Repeat("x", 200)}},
		}
		after.Connections = []domain.Connection{
			{From: "start", FromPort: "out", To: "gate", ToPort: "in"},
			{From: "gate", FromPort: "true", To: "buy", ToPort: "in"},
		}

		assert.Equal(t, strings.Join([]string{
			`Name: "Spreads" → "Spreads, live"`,
			`~ node gate (logic.if): name "" → "Gate"; continue on failure false → true; field removed; limit 1 → 2; op "gt"`,
			`~ node hook (notify.webhook): url changed (personal)`,
			`+ node buy (polymarket.order): size 50, url "` + strings.Repeat("x", maxValue-1) + `…`,
			`- node old (util.noop)`,
			`+ connection gate.true → buy.in`,
			`- connection gate.true → old.in`,
		}, "\n"), s.changes(before, after))
	})

	t.Run("a personal setting added says only that", func(t *testing.T) {
		after := before
		after.Nodes = append(append([]domain.NodeSpec{}, before.Nodes...),
			domain.NodeSpec{ID: "hook2", Type: "notify.webhook", Params: map[string]any{"url": "https://secret.example"}})

		assert.Equal(t, "+ node hook2 (notify.webhook): url set (personal)", s.changes(before, after))
	})

	t.Run("moved on the canvas only", func(t *testing.T) {
		after := before
		after.Nodes = append([]domain.NodeSpec{}, before.Nodes...)
		after.Nodes[0].Position = domain.CanvasPosition{X: 40, Y: 80}

		assert.Equal(t, "Nothing but where its nodes sit on the canvas.", s.changes(before, after))
	})
}
