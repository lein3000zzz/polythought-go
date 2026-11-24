// Package triggers holds the nodes that start executions.
package triggers

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const (
	ManualType   = "trigger.manual"
	ScheduleType = "trigger.schedule"
)

var (
	_ engine.Trigger = Manual{}
	_ engine.Trigger = (*Schedule)(nil)
)

// Manual never fires on its own; a manual run calls the executor with ManualOutput.
type Manual struct{}

func (Manual) Type() engine.NodeType {
	return engine.NodeType{
		Name:        ManualType,
		Label:       "Manual",
		Description: "Starts the workflow when you run it by hand.",
		Outputs:     []string{engine.DefaultPort},
		Items:       shape.Own(), // one empty item
	}
}

func (Manual) Start(ctx context.Context, _ engine.TriggerContext, _ engine.Emit) error {
	<-ctx.Done()
	return nil
}

// ManualOutput is what a manual trigger emits: one empty item, so the next node runs once.
func ManualOutput() engine.Ports {
	return engine.Ports{engine.DefaultPort: {{JSON: map[string]any{}}}}
}
