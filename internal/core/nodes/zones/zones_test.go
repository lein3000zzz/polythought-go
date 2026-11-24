package zones_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/zones"
)

func TestLoad(t *testing.T) {
	for _, name := range []string{"", "UTC", "utc", " America/Chicago ", "Europe/London", "Asia/Kolkata"} {
		_, err := zones.Load(name)
		require.NoError(t, err, name)
	}

	for _, name := range []string{"America/Chicag", "Local", "CST6CDT/x", "../etc/passwd"} {
		_, err := zones.Load(name)
		assert.ErrorContains(t, err, "is not a time zone", name)
	}

	require.NoError(t, zones.Check("America/New_York"))
	require.Error(t, zones.Check(3))
}

func TestClocks(t *testing.T) {
	got, err := zones.ParseClocks([]string{"18:00", " 06:30", "", "06:30", "00:00"})
	require.NoError(t, err)
	assert.Equal(t, []zones.Clock{{0, 0}, {6, 30}, {18, 0}}, got, "sorted, each once")
	assert.Equal(t, "06:30", got[1].String())

	for _, bad := range []string{"6:30pm", "24:00", "12:60", "noon"} {
		_, err := zones.ParseClocks([]string{bad})
		assert.ErrorContains(t, err, "is not a time of day", bad)
	}

	require.NoError(t, zones.CheckClocks([]any{"06:30", "18:00"}))
	require.ErrorContains(t, zones.CheckClocks([]any{"06:30", "25:00"}), `"25:00"`)
	require.Error(t, zones.CheckClocks("06:30"))

	days, err := zones.Days([]string{"Mon", "fri"})
	require.NoError(t, err)
	assert.Equal(t, map[time.Weekday]bool{time.Monday: true, time.Friday: true}, days)

	_, err = zones.Days([]string{"funday"})
	require.ErrorContains(t, err, "is not a day")
}

func TestTimes(t *testing.T) {
	chicago, err := zones.Load("America/Chicago")
	require.NoError(t, err)

	at := func(s string) time.Time {
		t.Helper()

		v, err := time.Parse(time.RFC3339, s)
		require.NoError(t, err)

		return v
	}

	daily := zones.Times{Clocks: []zones.Clock{{6, 30}, {18, 0}}, Loc: chicago}

	tests := []struct {
		name       string
		times      zones.Times
		from       string
		next, prev string
	}{
		{
			name: "later the same day, by the zone's clock", times: daily,
			from: "2026-11-24T12:00:00Z", // 06:00 in Chicago
			next: "2026-11-24T06:30:00-06:00", prev: "2026-11-23T18:00:00-06:00",
		},
		{
			name: "a time itself is the previous, not the next", times: daily,
			from: "2026-11-24T06:30:00-06:00",
			next: "2026-11-24T18:00:00-06:00", prev: "2026-11-24T06:30:00-06:00",
		},
		{
			name: "across midnight", times: daily,
			from: "2026-11-24T19:00:00-06:00",
			next: "2026-11-25T06:30:00-06:00", prev: "2026-11-24T18:00:00-06:00",
		},
		{
			name: "clocks going forward: the same 06:30, an hour less from UTC", times: daily,
			from: "2027-03-13T20:00:00-06:00",
			next: "2027-03-14T06:30:00-05:00", prev: "2027-03-13T18:00:00-06:00",
		},
		{
			name: "clocks going back", times: daily,
			from: "2027-11-06T20:00:00-05:00",
			next: "2027-11-07T06:30:00-06:00", prev: "2027-11-06T18:00:00-05:00",
		},
		{
			name:  "a time the clocks skip is the moment after the gap",
			times: zones.Times{Clocks: []zones.Clock{{2, 30}}, Loc: chicago},
			from:  "2027-03-14T00:00:00-06:00",
			next:  "2027-03-14T03:30:00-05:00", prev: "2027-03-13T02:30:00-06:00",
		},
		{
			name:  "weekdays only",
			times: zones.Times{Clocks: []zones.Clock{{9, 0}}, Days: map[time.Weekday]bool{time.Monday: true, time.Friday: true}, Loc: chicago},
			from:  "2026-11-24T10:00:00-06:00", // a Tuesday
			next:  "2026-11-27T09:00:00-06:00", prev: "2026-11-23T09:00:00-06:00",
		},
		{
			name:  "UTC",
			times: zones.Times{Clocks: []zones.Clock{{0, 0}}, Loc: time.UTC},
			from:  "2026-11-24T00:00:00Z",
			next:  "2026-11-25T00:00:00Z", prev: "2026-11-24T00:00:00Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			from := at(tc.from)
			assert.True(t, at(tc.next).Equal(tc.times.Next(from)), "next: %s", tc.times.Next(from))
			assert.True(t, at(tc.prev).Equal(tc.times.Prev(from)), "prev: %s", tc.times.Prev(from))
		})
	}

	assert.True(t, zones.Times{Loc: chicago}.Next(time.Now()).IsZero(), "no times, never")
}
