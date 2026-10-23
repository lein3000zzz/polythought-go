package ratelimit

import (
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.RateLimiter = (*PerMinute)(nil)

// PerMinute allows a number of attempts a minute per key, all at once if they come so,
// read as each attempt comes: a setting changed applies at once, every bucket full again.
type PerMinute struct {
	perMinute func() int
	now       func() time.Time

	mu  sync.Mutex
	at  int
	mem *Memory
}

func NewPerMinute(perMinute func() int) *PerMinute {
	return &PerMinute{perMinute: perMinute, now: time.Now}
}

func (p *PerMinute) Allow(key string) bool {
	n := max(p.perMinute(), 1)

	p.mu.Lock()
	if n != p.at {
		p.mem, p.at = newMemory(time.Minute/time.Duration(n), n, p.now), n
	}

	m := p.mem
	p.mu.Unlock()

	return m.Allow(key)
}
