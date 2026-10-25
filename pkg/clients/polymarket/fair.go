package polymarket

import (
	"context"
	"sync"

	"golang.org/x/time/rate"
)

// fair hands a limiter's requests to its callers in turn: one that asks for many waits
// behind the others' few rather than ahead of them, and alone it waits only for the
// limiter. Requests with no caller share one turn.
type fair struct {
	limiter *rate.Limiter

	mu      sync.Mutex
	queues  map[string][]*waiter
	order   []string // the callers waiting, in turn
	running bool
}

// waiter is a request waiting for its turn, and what came of it.
type waiter struct {
	ctx   context.Context
	ready chan struct{}
	err   error
}

func newFair(l *rate.Limiter) *fair {
	return &fair{limiter: l, queues: map[string][]*waiter{}}
}

// Wait waits for caller's turn, then for the limiter, as rate.Limiter.Wait does.
func (f *fair) Wait(ctx context.Context, caller string) error {
	w := &waiter{ctx: ctx, ready: make(chan struct{})}

	f.mu.Lock()
	if len(f.queues[caller]) == 0 {
		f.order = append(f.order, caller)
	}

	f.queues[caller] = append(f.queues[caller], w)

	if !f.running {
		f.running = true
		go f.run()
	}
	f.mu.Unlock()

	select {
	case <-w.ready:
		return w.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run lets the waiting requests through, a caller's at a time in turn, until none waits.
func (f *fair) run() {
	for {
		f.mu.Lock()
		w := f.next()
		if w == nil {
			f.running = false
			f.mu.Unlock()

			return
		}
		f.mu.Unlock()

		w.err = f.limiter.Wait(w.ctx)
		close(w.ready)
	}
}

// next is the next caller's first request still wanted, the caller going to the back of the
// line while it has more. Under f.mu.
func (f *fair) next() *waiter {
	for len(f.order) > 0 {
		caller := f.order[0]
		f.order = f.order[1:]

		q := f.queues[caller]
		w := q[0]

		if len(q) > 1 {
			f.queues[caller] = q[1:]
			f.order = append(f.order, caller)
		} else {
			delete(f.queues, caller)
		}

		if w.ctx.Err() == nil {
			return w
		}
	}

	return nil
}
