// Package triggers holds the nodes that start executions.
package triggers

import (
	"context"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
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
	return engine.NodeType{Name: ManualType, Outputs: []string{engine.DefaultPort}}
}

func (Manual) Start(ctx context.Context, _ engine.TriggerContext, _ engine.Emit) error {
	<-ctx.Done()
	return nil
}

// ManualOutput is what a manual trigger emits: one empty item, so the next node runs once.
func ManualOutput() engine.Ports {
	return engine.Ports{engine.DefaultPort: {{JSON: map[string]any{}}}}
}

// DefaultMinInterval keeps schedules from hammering the APIs they feed.
const DefaultMinInterval = time.Second

// Schedule fires every interval, emitting one item with the fire time.
type Schedule struct {
	minInterval time.Duration
}

func NewSchedule(minInterval time.Duration) *Schedule {
	return &Schedule{minInterval: minInterval}
}

func (*Schedule) Type() engine.NodeType {
	return engine.NodeType{
		Name:    ScheduleType,
		Outputs: []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "interval", Kind: engine.ParamString, Required: true}, // Go duration: "30s", "5m"
			{Name: "runOnStart", Kind: engine.ParamBool, Default: true},
		},
	}
}

// Start fires until ctx is done. A tick that comes while the previous execution is still
// running is dropped, so executions never pile up. Emit errors are logged by the runner
// and do not stop the schedule.
func (s *Schedule) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	interval, err := s.interval(tc.Params)
	if err != nil {
		return err
	}

	runOnStart, err := tc.Params.Bool("runOnStart")
	if err != nil {
		return err
	}

	fire := func(at time.Time) {
		_ = emit(ctx, engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"firedAt": at.UTC()}}}})
	}

	if runOnStart {
		fire(time.Now())
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case at := <-ticker.C:
			fire(at)
		}
	}
}

func (s *Schedule) interval(p engine.Params) (time.Duration, error) {
	raw, err := p.String("interval")
	if err != nil {
		return 0, err
	}

	interval, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%w %q: %w", engine.ErrInvalidParam, "interval", err)
	}

	if interval < s.minInterval {
		return 0, fmt.Errorf("%w %q: %s is below the minimum of %s", engine.ErrInvalidParam, "interval", interval, s.minInterval)
	}

	return interval, nil
}
