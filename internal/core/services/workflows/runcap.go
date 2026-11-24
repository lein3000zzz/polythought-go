package workflows

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// RunCap counts the runs each workflow's triggers started in the last hour, and pauses
// one that reaches the instance's most (D435): the run past it never starts. Runs by hand
// never count: they do not come through the triggers.
type RunCap struct {
	most  func() int // 0: no cap
	pause func(w domain.Workflow, why string)
	now   func() time.Time

	mu   sync.Mutex
	runs map[domain.WorkflowID]*lastHour
}

// lastHour is a workflow's runs, by the minute, for the last 60 minutes.
type lastHour struct {
	minute [60]int64 // which minute each slot counts
	runs   [60]int
	paused bool // pausing it already: told once
}

// WithRunCap pauses workflows whose triggers start more runs in an hour than most says,
// read on every run, so a change applies at once.
func WithRunCap(most func() int) Option {
	return func(s *Service) {
		// The service's clock, as it is once every option applied.
		s.runCap = &RunCap{most: most, now: func() time.Time { return s.now() }, runs: map[domain.WorkflowID]*lastHour{}}
		s.runCap.pause = func(w domain.Workflow, why string) {
			ctx := context.Background()
			if err := s.pause(ctx, w, why); err != nil {
				s.log.Error(ctx, "Workflow past its runs an hour not paused", "workflow_id", w.ID, "error", err.Error())
			}
		}
	}
}

// RunCap is the cap on the runs of the workflows' triggers, for the engine's runner; nil
// without WithRunCap.
func (s *Service) RunCap() *RunCap { return s.runCap }

// Allow says whether a trigger may start w's next run, counting it if so. One past the
// cap is refused, and the workflow paused, apart, as pausing waits for its triggers.
func (c *RunCap) Allow(w domain.Workflow) bool {
	most := c.most()
	if most <= 0 {
		return true
	}

	minute := c.now().Unix() / 60

	c.mu.Lock()
	defer c.mu.Unlock()

	h := c.runs[w.ID]
	if h == nil {
		h = &lastHour{}
		c.runs[w.ID] = h
	}

	total := 0

	for i := range h.minute {
		if minute-h.minute[i] < 60 {
			total += h.runs[i]
		}
	}

	if total >= most {
		if !h.paused {
			h.paused = true
			go c.pause(w, fmt.Sprintf("its triggers started it %d times in an hour, the most this instance allows", most))
		}

		return false
	}

	slot := minute % 60
	if h.minute[slot] != minute {
		h.minute[slot], h.runs[slot] = minute, 0
	}

	h.runs[slot]++

	return true
}

// Forget starts a workflow's count over, as it is switched on again.
func (c *RunCap) Forget(id domain.WorkflowID) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.runs, id)
}
