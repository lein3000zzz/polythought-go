package triggers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/cities"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/zones"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// DefaultMinInterval keeps schedules from hammering the APIs they feed (D435).
const DefaultMinInterval = 5 * time.Second

// The ways a schedule runs (D440).
const (
	ScheduleEvery = "every"
	ScheduleAt    = "at"
)

// ScheduleOutput is what a schedule emits when it fires at at, on time: one item with the
// time.
func ScheduleOutput(at time.Time) engine.Ports {
	return ScheduleFired(at, at, false)
}

// ScheduleFired is what a schedule emits firing at at, for the run due at due; missed,
// for one it makes up (D440).
func ScheduleFired(at, due time.Time, missed bool) engine.Ports {
	return engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"firedAt": at.UTC(), "due": due.UTC(), "missed": missed}}}}
}

// Schedule fires at an interval, or at times of day in a time zone, emitting one item with
// the time. One may make up a run it missed while Polythought was down, keeping when it
// last fired for that (D440).
type Schedule struct {
	minInterval time.Duration
	fires       ports.ScheduleFires // nil: no making up
	now         func() time.Time
}

type ScheduleOption func(*Schedule)

// WithFires keeps each schedule's last fire, for those making up a missed run.
func WithFires(f ports.ScheduleFires) ScheduleOption {
	return func(s *Schedule) { s.fires = f }
}

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) ScheduleOption {
	return func(s *Schedule) { s.now = now }
}

func NewSchedule(minInterval time.Duration, opts ...ScheduleOption) *Schedule {
	s := &Schedule{minInterval: minInterval, now: time.Now}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

func (*Schedule) Type() engine.NodeType {
	return engine.NodeType{
		Name:        ScheduleType,
		Label:       "Schedule",
		Description: "Starts the workflow at a fixed interval, or at times of day in a time zone.",
		Outputs:     []string{engine.DefaultPort},
		Items: shape.Own(
			shape.Time("firedAt", "When the schedule fired."),
			shape.Time("due", "When the run was due: the time of day it is for, or the tick."),
			shape.Bool("missed", "A run made up, missed while Polythought was down."),
		),
		Params: []engine.ParamSpec{
			{
				Name: "mode", Label: "Runs", Kind: engine.ParamString, Default: ScheduleEvery, Options: []string{ScheduleEvery, ScheduleAt},
				Description: "every: at a fixed interval. at: at times of day, on the days ticked, in a time zone.",
			},
			{Name: "interval", Label: "Every", Description: "With every: a duration like 30s or 5m.", Kind: engine.ParamDuration},
			{
				Name: "runOnStart", Label: "Run on start", Kind: engine.ParamBool, Default: true,
				Description: "With every: also fire as soon as the workflow is switched on.",
			},
			{
				Name: "times", Label: "At", Kind: engine.ParamList, Check: zones.CheckClocks,
				Description: "With at: times of day, 24-hour, one per line, e.g. 06:30.",
			},
			{
				Name: "days", Label: "On", Kind: engine.ParamList, Options: zones.Weekdays,
				Description: "With at: the days it runs; none ticked, every day.",
			},
			{
				Name: "zone", Label: "Time zone", Kind: engine.ParamString, Default: "UTC", Check: zones.Check,
				Description: "With at: whose clock the times are, as the IANA database names it, e.g. America/Chicago or " +
					"Europe/London. Daylight saving is followed.",
			},
			{
				Name: "city", Label: "City's time zone", Kind: engine.ParamString, Options: cities.Names(),
				Description: "With at: a city Polymarket lists temperature markets for, whose time zone it takes in place " +
					"of Time zone (D441).",
			},
			{
				Name: "catchUp", Label: "Make up a missed run", Kind: engine.ParamBool, Default: false,
				Description: "When Polythought was down as a run fell due, run once as it starts again, for the latest time " +
					"missed; its item says missed: true. With every, only when Run on start is off.",
			},
			{
				Name: "catchUpWithin", Label: "Make up within", Kind: engine.ParamDuration, Default: "1h",
				Description: "How long after its time a missed run is still made up.",
			},
		},
		Check: func(p engine.Params) error {
			_, err := plan(p, 0)
			return err
		},
	}
}

// schedulePlan is a schedule's settings, read.
type schedulePlan struct {
	mode       string
	interval   time.Duration
	runOnStart bool
	times      zones.Times
	catchUp    bool
	within     time.Duration
}

func plan(p engine.Params, minInterval time.Duration) (schedulePlan, error) {
	var (
		sp  schedulePlan
		err error
	)

	sp.mode = ScheduleEvery
	if p["mode"] != nil {
		if sp.mode, err = p.String("mode"); err != nil {
			return sp, err
		}
	}

	if p["catchUp"] != nil {
		if sp.catchUp, err = p.Bool("catchUp"); err != nil {
			return sp, err
		}
	}

	sp.within = time.Hour
	if p["catchUpWithin"] != nil {
		if sp.within, err = p.Duration("catchUpWithin"); err != nil {
			return sp, err
		}
	}

	if sp.mode == ScheduleAt {
		return sp, sp.readTimes(p)
	}

	if p["interval"] == nil {
		return sp, fmt.Errorf("%w %q: say how often it runs, under Every", engine.ErrMissingParam, "interval")
	}

	if sp.interval, err = p.Duration("interval"); err != nil {
		return sp, fmt.Errorf("%w %q: %w", engine.ErrInvalidParam, "interval", err)
	}

	// One saved under a lower minimum, as an admin raised it since, fires at the minimum
	// rather than not at all (D435).
	sp.interval = max(sp.interval, minInterval)

	sp.runOnStart = true
	if p["runOnStart"] != nil {
		if sp.runOnStart, err = p.Bool("runOnStart"); err != nil {
			return sp, err
		}
	}

	return sp, nil
}

func (sp *schedulePlan) readTimes(p engine.Params) error {
	var clocks, days []string

	if p["times"] != nil {
		var err error
		if clocks, err = p.Strings("times"); err != nil {
			return err
		}
	}

	if p["days"] != nil {
		var err error
		if days, err = p.Strings("days"); err != nil {
			return err
		}
	}

	parsed, err := zones.ParseClocks(clocks)
	if err != nil {
		return err
	}

	if len(parsed) == 0 {
		return fmt.Errorf("%w %q: give a time of day it runs at, under At, like 06:30", engine.ErrMissingParam, "times")
	}

	on, err := zones.Days(days)
	if err != nil {
		return err
	}

	zone := ""
	if p["zone"] != nil {
		if zone, err = p.String("zone"); err != nil {
			return err
		}
	}

	if p["city"] != nil {
		name, err := p.String("city")
		if err != nil {
			return err
		}

		if c, ok := cities.Find(name); ok {
			zone = c.Zone
		} else if strings.TrimSpace(name) != "" {
			return fmt.Errorf("%w %q: %q is not one of the cities", engine.ErrInvalidParam, "city", name)
		}
	}

	loc, err := zones.Load(zone)
	if err != nil {
		return err
	}

	sp.times = zones.Times{Clocks: parsed, Days: on, Loc: loc}

	return nil
}

// Start fires until ctx is done. A time that comes while the previous execution is still
// running is dropped, so executions never pile up. Emit errors are logged by the runner
// and do not stop the schedule.
func (s *Schedule) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	sp, err := plan(tc.Params, s.minInterval)
	if err != nil {
		return err
	}

	keeps := sp.catchUp && s.fires != nil

	fire := func(due time.Time, missed bool) {
		if keeps {
			// Kept before the run, so a run that is long, or the server stopping in it, is
			// not made up again.
			_ = s.fires.Fired(ctx, tc.WorkflowID, tc.NodeID, due)
		}

		_ = emit(ctx, ScheduleFired(s.now(), due, missed))
	}

	if keeps && tc.Resumed {
		if due, ok := s.missed(ctx, tc, sp); ok {
			fire(due, true)
		}
	}

	if sp.mode == ScheduleAt {
		return s.atTimes(ctx, sp, fire)
	}

	if sp.runOnStart {
		fire(s.now(), false)
	}

	ticker := time.NewTicker(sp.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case at := <-ticker.C:
			fire(at, false)
		}
	}
}

// atTimes fires at each time of day as it comes, until ctx is done.
func (s *Schedule) atTimes(ctx context.Context, sp schedulePlan, fire func(due time.Time, missed bool)) error {
	var last time.Time

	for {
		now := s.now()
		next := sp.times.Next(maxTime(now, last))

		if next.IsZero() {
			return errors.New("the schedule has no time to run at")
		}

		timer := time.NewTimer(next.Sub(now))

		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
			last = next
			fire(next, false)
		}
	}
}

// missed is the latest run due since the schedule last fired, if one is, within the time
// it may still be made up; with every and Run on start, the start makes it up already.
func (s *Schedule) missed(ctx context.Context, tc engine.TriggerContext, sp schedulePlan) (time.Time, bool) {
	last, err := s.fires.LastFire(ctx, tc.WorkflowID, tc.NodeID)
	if err != nil || last.IsZero() {
		return time.Time{}, false
	}

	now := s.now()

	var due time.Time

	switch {
	case sp.mode == ScheduleAt:
		due = sp.times.Prev(now)
	case sp.runOnStart:
		return time.Time{}, false
	default:
		due = last.Add(now.Sub(last) / sp.interval * sp.interval)
	}

	if due.IsZero() || !due.After(last) || now.Sub(due) > sp.within {
		return time.Time{}, false
	}

	return due, true
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}

	return b
}
