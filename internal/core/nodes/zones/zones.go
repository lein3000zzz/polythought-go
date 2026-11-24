// Package zones reads time zones and times of day for the nodes that run by the clock
// (D440): schedules at times of day, Wait until one, in a zone as the IANA database names
// it, daylight saving followed. The server embeds that database, so every zone is there
// whatever the machine has.
package zones

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Weekdays are the days a schedule may run on, as its settings name them.
var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var weekdayOf = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

// Load is the zone named, UTC for none.
func Load(name string) (*time.Location, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "utc") {
		return time.UTC, nil
	}

	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		return nil, fmt.Errorf("%q is not a time zone: one like America/Chicago or Europe/London", name)
	}

	return loc, nil
}

// Check is a time zone setting's check, for its ParamSpec.
func Check(v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("must be a time zone's name")
	}

	_, err := Load(s)

	return err
}

// Clock is a time of day, to the minute.
type Clock struct {
	Hour, Minute int
}

func (c Clock) String() string { return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute) }

// ParseClock reads HH:MM, 24-hour.
func ParseClock(s string) (Clock, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return Clock{}, fmt.Errorf("%q is not a time of day like 06:30", s)
	}

	return Clock{Hour: t.Hour(), Minute: t.Minute()}, nil
}

// ParseClocks reads times of day, sorted, each once.
func ParseClocks(list []string) ([]Clock, error) {
	var out []Clock

	for _, s := range list {
		if strings.TrimSpace(s) == "" {
			continue
		}

		c, err := ParseClock(s)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	slices.SortFunc(out, func(a, b Clock) int { return (a.Hour*60 + a.Minute) - (b.Hour*60 + b.Minute) })

	return slices.Compact(out), nil
}

// CheckClocks is a times-of-day setting's check, for its ParamSpec.
func CheckClocks(v any) error {
	list, ok := v.([]any)
	if !ok {
		if s, isList := v.([]string); isList {
			for _, e := range s {
				list = append(list, e)
			}
		} else {
			return errors.New("must be a list of times of day")
		}
	}

	for _, e := range list {
		s, ok := e.(string)
		if !ok {
			return errors.New("must be a list of times of day")
		}

		if _, err := ParseClocks([]string{s}); err != nil {
			return err
		}
	}

	return nil
}

// Days reads weekdays by their names, mon to sun; none is every day.
func Days(names []string) (map[time.Weekday]bool, error) {
	out := map[time.Weekday]bool{}

	for _, n := range names {
		d, ok := weekdayOf[strings.ToLower(strings.TrimSpace(n))]
		if !ok {
			return nil, fmt.Errorf("%q is not a day: one of %s", n, strings.Join(Weekdays, ", "))
		}

		out[d] = true
	}

	return out, nil
}

// Times is when a schedule runs: at these times of day, on these days (none for every
// day), in loc.
type Times struct {
	Clocks []Clock
	Days   map[time.Weekday]bool
	Loc    *time.Location
}

func (t Times) on(day time.Weekday) bool {
	return len(t.Days) == 0 || t.Days[day]
}

// at is clock c on the day d days from date, in loc. A time daylight saving skips, like
// 02:30 the night clocks go forward, is the moment after the gap, as the clock reads it
// once more.
func (t Times) at(date time.Time, d int, c Clock) time.Time {
	at := time.Date(date.Year(), date.Month(), date.Day()+d, c.Hour, c.Minute, 0, 0, t.Loc)

	// Go may read a skipped time with the offset after the gap, which lands before it:
	// moved on by what its clock lacks, it reads the time asked for at the old offset.
	want := time.Date(date.Year(), date.Month(), date.Day()+d, c.Hour, c.Minute, 0, 0, time.UTC)
	got := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)

	if lacks := want.Sub(got); lacks > 0 {
		at = at.Add(lacks)
	}

	return at
}

// Next is the first time strictly after after, or the zero time for none.
func (t Times) Next(after time.Time) time.Time {
	if len(t.Clocks) == 0 {
		return time.Time{}
	}

	local := after.In(t.Loc)

	for d := 0; d <= 8; d++ {
		for _, c := range t.Clocks {
			at := t.at(local, d, c)
			if at.After(after) && t.on(at.Weekday()) {
				return at
			}
		}
	}

	return time.Time{}
}

// Prev is the last time at or before before, or the zero time for none.
func (t Times) Prev(before time.Time) time.Time {
	if len(t.Clocks) == 0 {
		return time.Time{}
	}

	local := before.In(t.Loc)

	for d := 0; d >= -8; d-- {
		for _, c := range slices.Backward(t.Clocks) {
			at := t.at(local, d, c)
			if !at.After(before) && t.on(at.Weekday()) {
				return at
			}
		}
	}

	return time.Time{}
}
