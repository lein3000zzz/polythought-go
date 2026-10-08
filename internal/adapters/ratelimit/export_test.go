package ratelimit

import "time"

// NewMemoryWithClock exposes the clock seam to the external test package.
func NewMemoryWithClock(every time.Duration, burst int, now func() time.Time) *Memory {
	return newMemory(every, burst, now)
}
