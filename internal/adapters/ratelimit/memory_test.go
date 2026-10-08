package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
)

func TestMemoryAllow(t *testing.T) {
	start := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

	type step struct {
		after time.Duration // since start
		key   string
		want  bool
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "burst then blocked",
			steps: []step{
				{0, "a", true}, {0, "a", true}, {0, "a", true}, {0, "a", false},
			},
		},
		{
			name: "refills one per interval",
			steps: []step{
				{0, "a", true}, {0, "a", true}, {0, "a", true}, {0, "a", false},
				{time.Minute, "a", true}, {time.Minute, "a", false},
			},
		},
		{
			name: "keys are independent",
			steps: []step{
				{0, "a", true}, {0, "a", true}, {0, "a", true}, {0, "a", false},
				{0, "b", true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := start
			m := ratelimit.NewMemoryWithClock(time.Minute, 3, func() time.Time { return now })

			for i, s := range tt.steps {
				now = start.Add(s.after)
				assert.Equal(t, s.want, m.Allow(s.key), "step %d (%s at +%s)", i, s.key, s.after)
			}
		})
	}
}

func TestMemoryForgetsIdleKeys(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	m := ratelimit.NewMemoryWithClock(time.Minute, 3, func() time.Time { return now })

	m.Allow("old")
	m.Allow("other")
	assert.Equal(t, 2, m.Len())

	now = now.Add(3 * time.Minute) // a full refill: the buckets carry no state any more
	m.Allow("new")

	assert.Equal(t, 1, m.Len())
	assert.True(t, m.Allow("old"), "a forgotten key starts with a full bucket")
}
