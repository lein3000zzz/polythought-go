// Package ratelimit implements the core RateLimiter port.
package ratelimit

import (
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.RateLimiter = (*Memory)(nil)

// Memory keeps a token bucket per key in process memory. It suits a single instance;
// several instances behind a load balancer need a shared store such as Redis.
type Memory struct {
	every time.Duration
	burst int
	idle  time.Duration // a bucket unused this long is full again and can be forgotten
	now   func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	limiter *rate.Limiter
	seen    time.Time
}

// NewMemory allows burst attempts per key at once, then one more every interval.
func NewMemory(every time.Duration, burst int) *Memory {
	return newMemory(every, burst, time.Now)
}

func newMemory(every time.Duration, burst int, now func() time.Time) *Memory {
	return &Memory{
		every:   every,
		burst:   burst,
		idle:    every * time.Duration(burst),
		now:     now,
		buckets: make(map[string]*bucket),
	}
}

func (m *Memory) Allow(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	m.sweep(now)

	b, ok := m.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(rate.Every(m.every), m.burst)}
		m.buckets[key] = b
	}

	b.seen = now

	return b.limiter.AllowN(now, 1)
}

// sweep drops idle buckets at most once per idle period, keeping memory bounded by the
// number of keys active recently rather than ever seen.
func (m *Memory) sweep(now time.Time) {
	if now.Sub(m.lastSweep) < m.idle {
		return
	}

	for key, b := range m.buckets {
		if now.Sub(b.seen) >= m.idle {
			delete(m.buckets, key)
		}
	}

	m.lastSweep = now
}

// Len is the number of keys currently tracked.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.buckets)
}
