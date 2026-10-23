package ratelimit_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
)

func TestPerMinute(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	perMinute := 3
	l := ratelimit.NewPerMinuteWithClock(func() int { return perMinute }, func() time.Time { return now })

	for range 3 {
		assert.True(t, l.Allow("agent"))
	}

	assert.False(t, l.Allow("agent"), "three a minute")
	assert.True(t, l.Allow("another"), "each agent its own")

	now = now.Add(20 * time.Second)
	assert.True(t, l.Allow("agent"), "one more each 20 seconds")
	assert.False(t, l.Allow("agent"))

	perMinute = 10
	for range 10 {
		assert.True(t, l.Allow("agent"), "the setting raised applies at once")
	}

	assert.False(t, l.Allow("agent"))

	perMinute = 0
	assert.True(t, l.Allow("agent"), "at least one, with the buckets full again")
	assert.False(t, l.Allow("agent"))
}
