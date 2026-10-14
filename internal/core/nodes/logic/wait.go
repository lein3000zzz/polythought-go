package logic

import (
	"context"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

const WaitType = "logic.wait"

var _ engine.Resumer = (*Wait)(nil)

// Wait holds the items it gets for a while, or until a time of day, then passes them on.
// The execution waits meanwhile, keeping nothing busy, and carries on after a restart.
type Wait struct {
	now func() time.Time
}

func NewWait(now func() time.Time) *Wait {
	return &Wait{now: now}
}

func (*Wait) Type() engine.NodeType {
	return engine.NodeType{
		Name:  WaitType,
		Label: "Wait",
		Description: "Holds the items for a while, or until a time of day, then passes them on. The execution " +
			"waits meanwhile without keeping anything busy, and carries on after a restart.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{
				Name: "mode", Label: "Wait", Kind: engine.ParamString, Default: "for", Options: []string{"for", "until"},
				Description: "for: a while. until: the next time the clock shows a time of day.",
			},
			{
				Name: "for", Label: "For", Kind: engine.ParamDuration, Default: "10m",
				Description: "How long, e.g. 30s, 10m or 2h.",
			},
			{
				Name: "until", Label: "Until", Kind: engine.ParamString,
				Description: "A time of day in UTC, as HH:MM, e.g. 09:30. With mode until.",
			},
		},
	}
}

func (n *Wait) Run(_ context.Context, rc engine.RunContext, _ engine.Ports) (engine.Ports, error) {
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	mode, err := p.String("mode")
	if err != nil {
		return nil, err
	}

	now := n.now()

	if mode == "until" {
		at, err := p.String("until")
		if err != nil {
			return nil, err
		}

		until, err := nextTimeOfDay(now, at)
		if err != nil {
			return nil, fmt.Errorf("%w %q: %w", engine.ErrInvalidParam, "until", err)
		}

		return nil, &engine.Wait{Until: until}
	}

	d, err := p.Duration("for")
	if err != nil {
		return nil, err
	}

	return nil, &engine.Wait{Until: now.Add(d)}
}

// Resume passes on the items it held, however the wait ended.
func (*Wait) Resume(_ context.Context, _ engine.RunContext, in engine.Ports, _ engine.Resumption) (engine.Ports, error) {
	out := engine.Ports{}

	for i, it := range in[engine.DefaultPort] {
		out.Add(engine.DefaultPort, engine.Item{JSON: it.JSON, Source: []engine.ItemRef{{Port: engine.DefaultPort, Index: i}}})
	}

	return out, nil
}

// nextTimeOfDay is the first time after now that the clock shows hh:mm in UTC.
func nextTimeOfDay(now time.Time, hhmm string) (time.Time, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a time of day like 09:30", hhmm)
	}

	now = now.UTC()
	at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)

	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}

	return at, nil
}
