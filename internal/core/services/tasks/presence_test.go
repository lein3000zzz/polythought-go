package tasks

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Holders gone a while are forgotten, all of them, however many came and went while
// another stayed, and an owner with none left goes too (A26).
func TestPresenceForgets(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	p := NewPresence(func() time.Duration { return time.Minute }, func() time.Time { return now })

	for i := range 100 {
		p.touch("alice", fmt.Sprintf("token:t-%d", i))
	}

	now = now.Add(2 * time.Minute)
	p.touch("alice", "grant:g-1") // the one that stays

	assert.True(t, p.Around("alice"))
	assert.Equal(t, map[string]bool{"grant:g-1": true}, p.Taking("alice"))

	p.mu.Lock()
	assert.Len(t, p.seen["alice"], 1, "the hundred gone are forgotten")
	p.mu.Unlock()

	now = now.Add(2 * time.Minute)
	assert.False(t, p.Around("alice"))

	p.mu.Lock()
	assert.NotContains(t, p.seen, "alice", "nor kept with no one left")
	p.mu.Unlock()
}
