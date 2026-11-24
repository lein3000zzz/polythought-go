package triggers_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
)

// fires keeps each schedule's last fire in memory.
type fires struct {
	mu   sync.Mutex
	last time.Time
	kept []time.Time
}

func (f *fires) LastFire(context.Context, domain.WorkflowID, domain.NodeID) (time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.last, nil
}

func (f *fires) Fired(_ context.Context, _ domain.WorkflowID, _ domain.NodeID, due time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.last = due
	f.kept = append(f.kept, due)

	return nil
}

// running is a clock that starts at from and runs as time does.
func running(from time.Time) func() time.Time {
	start := time.Now()
	return func() time.Time { return from.Add(time.Since(start)) }
}

func chicago(t *testing.T, s string) time.Time {
	t.Helper()

	loc, err := time.LoadLocation("America/Chicago")
	require.NoError(t, err)

	v, err := time.ParseInLocation("2006-01-02 15:04:05", s, loc)
	require.NoError(t, err)

	return v
}

func start(t *testing.T, s *triggers.Schedule, params map[string]any, resumed bool) (*recorder, func()) {
	t.Helper()

	rec := &recorder{}
	tc := triggerContext(t, s.Type(), params)
	tc.Resumed = resumed

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- s.Start(ctx, tc, rec.emit) }()

	return rec, func() {
		cancel()
		require.NoError(t, <-done)
	}
}

func item(p engine.Ports) map[string]any { return p[engine.DefaultPort][0].JSON }

func TestScheduleAtTimes(t *testing.T) {
	// A second before 06:30 in Chicago, as the clock runs.
	s := triggers.NewSchedule(time.Second, triggers.WithClock(running(chicago(t, "2026-11-24 06:29:59"))))

	rec, stop := start(t, s, map[string]any{
		"mode": "at", "times": []any{"06:30", "18:00"}, "zone": "America/Chicago",
	}, false)
	defer stop()

	require.Eventually(t, func() bool { return rec.count() == 1 }, waitFor, 10*time.Millisecond)

	fired := item(rec.first())
	assert.True(t, chicago(t, "2026-11-24 06:30:00").Equal(fired["due"].(time.Time)), "due at 06:30 Chicago: %v", fired["due"])
	assert.Equal(t, false, fired["missed"])
	require.NoError(t, shape.Check(s.Type(), rec.first()))

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, rec.count(), "the next is at 18:00")
}

func TestScheduleByACitysZone(t *testing.T) {
	// A second before 06:30 in Chicago, Austin's zone.
	s := triggers.NewSchedule(time.Second, triggers.WithClock(running(chicago(t, "2026-11-24 06:29:59"))))

	rec, stop := start(t, s, map[string]any{"mode": "at", "times": []any{"06:30"}, "city": "Austin", "zone": "Asia/Tokyo"}, false)
	defer stop()

	require.Eventually(t, func() bool { return rec.count() == 1 }, waitFor, 10*time.Millisecond)
	assert.True(t, chicago(t, "2026-11-24 06:30:00").Equal(item(rec.first())["due"].(time.Time)), "the city's, over the zone's")
}

func TestScheduleMakesUpAMissedRun(t *testing.T) {
	at := map[string]any{"mode": "at", "times": []any{"06:30"}, "zone": "America/Chicago", "catchUp": true}

	t.Run("the latest time missed, once, as the server starts again", func(t *testing.T) {
		f := &fires{last: chicago(t, "2026-11-23 06:30:00")}
		s := triggers.NewSchedule(time.Second, triggers.WithFires(f), triggers.WithClock(running(chicago(t, "2026-11-24 07:00:00"))))

		rec, stop := start(t, s, at, true)

		require.Eventually(t, func() bool { return rec.count() == 1 }, waitFor, 10*time.Millisecond)
		stop()

		made := item(rec.first())
		assert.Equal(t, true, made["missed"])
		assert.True(t, chicago(t, "2026-11-24 06:30:00").Equal(made["due"].(time.Time)))
		assert.Len(t, f.kept, 1, "kept, so it is not made up again")
	})

	for _, tc := range []struct {
		name    string
		last    string
		now     string
		params  map[string]any
		resumed bool
	}{
		{name: "missed more than an hour ago", last: "2026-11-23 06:30:00", now: "2026-11-24 07:31:00", params: at, resumed: true},
		{name: "not missed", last: "2026-11-24 06:30:00", now: "2026-11-24 07:00:00", params: at, resumed: true},
		{name: "switched on by hand, not starting again", last: "2026-11-23 06:30:00", now: "2026-11-24 07:00:00", params: at},
		{name: "never fired", now: "2026-11-24 07:00:00", params: at, resumed: true},
		{
			name: "not asked to", last: "2026-11-23 06:30:00", now: "2026-11-24 07:00:00", resumed: true,
			params: map[string]any{"mode": "at", "times": []any{"06:30"}, "zone": "America/Chicago"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fires{}
			if tc.last != "" {
				f.last = chicago(t, tc.last)
			}

			s := triggers.NewSchedule(time.Second, triggers.WithFires(f), triggers.WithClock(running(chicago(t, tc.now))))

			rec, stop := start(t, s, tc.params, tc.resumed)
			time.Sleep(50 * time.Millisecond)
			stop()

			assert.Zero(t, rec.count())
		})
	}

	t.Run("every, the tick missed, when Run on start is off", func(t *testing.T) {
		f := &fires{last: chicago(t, "2026-11-24 06:00:00")}
		s := triggers.NewSchedule(time.Second, triggers.WithFires(f), triggers.WithClock(running(chicago(t, "2026-11-24 06:45:00"))))

		rec, stop := start(t, s, map[string]any{"interval": "20m", "runOnStart": false, "catchUp": true, "catchUpWithin": "10m"}, true)

		require.Eventually(t, func() bool { return rec.count() == 1 }, waitFor, 10*time.Millisecond)
		stop()

		made := item(rec.first())
		assert.Equal(t, true, made["missed"])
		assert.True(t, chicago(t, "2026-11-24 06:40:00").Equal(made["due"].(time.Time)), "06:00, 06:20, 06:40: %v", made["due"])
	})

	t.Run("every with Run on start: the start makes it up", func(t *testing.T) {
		f := &fires{last: chicago(t, "2026-11-24 06:00:00")}
		s := triggers.NewSchedule(time.Second, triggers.WithFires(f), triggers.WithClock(running(chicago(t, "2026-11-24 06:45:00"))))

		rec, stop := start(t, s, map[string]any{"interval": "20m", "catchUp": true}, true)

		require.Eventually(t, func() bool { return rec.count() == 1 }, waitFor, 10*time.Millisecond)
		time.Sleep(30 * time.Millisecond)
		stop()

		assert.Equal(t, 1, rec.count())
		assert.Equal(t, false, item(rec.first())["missed"])
	})
}

func TestScheduleChecksItsSettings(t *testing.T) {
	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(triggers.NewSchedule(time.Second)))
	require.NoError(t, r.RegisterTrigger(triggers.Manual{}))

	check := func(params map[string]any) error {
		return r.Validate(domain.Workflow{ID: "wf", Nodes: []domain.NodeSpec{{ID: "every", Type: triggers.ScheduleType, Params: params}}})
	}

	require.NoError(t, check(map[string]any{"interval": "5m"}), "every, as saved before modes")
	require.NoError(t, check(map[string]any{"mode": "at", "times": []any{"06:30"}, "days": []any{"mon", "fri"}, "zone": "Europe/London"}))
	require.NoError(t, check(map[string]any{"mode": "at", "times": []any{"06:30"}, "city": "NYC"}), "a weather city's zone (D441)")
	require.ErrorContains(t, check(map[string]any{"mode": "at", "times": []any{"06:30"}, "city": "Atlantis"}), "Atlantis")

	for _, tc := range []struct {
		name   string
		params map[string]any
		want   string
	}{
		{name: "every, but how often", params: map[string]any{"mode": "every"}, want: `missing param "interval"`},
		{name: "at, but when", params: map[string]any{"mode": "at"}, want: `missing param "times"`},
		{name: "a time that is none", params: map[string]any{"mode": "at", "times": []any{"25:00"}}, want: `"25:00" is not a time of day`},
		{name: "a zone that is none", params: map[string]any{"mode": "at", "times": []any{"06:30"}, "zone": "Mars/Olympus"}, want: `"Mars/Olympus" is not a time zone`},
		{name: "a day that is none", params: map[string]any{"mode": "at", "times": []any{"06:30"}, "days": []any{"someday"}}, want: "someday"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorContains(t, check(tc.params), tc.want)
		})
	}
}
