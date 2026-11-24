package mlmodels

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Request is a batch of items asked of a model, as a worker takes it: each item an object
// of the fields the asker chose, and the schema each answer is to keep to, if any.
type Request struct {
	ID       string            `json:"id"`
	Items    []json.RawMessage `json:"items"`
	Schema   json.RawMessage   `json:"schema,omitempty"`
	Deadline time.Time         `json:"deadline"`
}

// Answer is a model's answer to a request.
type Answer = ports.ModelAnswer

// Worker is one taking a model's requests: the name its SDK gives it, unique among the
// model's, and the version it serves.
type Worker struct {
	Name    string
	Version string
}

// Connected is a worker connected now: polling for requests lately, or holding one.
type Connected struct {
	Name    string
	Version string
	Since   time.Time // first seen, this time
	Seen    time.Time // last polled
	Holding int       // requests it holds now
}

// pending is a request waiting for its answer: free, or leased to a worker until leaseEnd,
// after which another may take it.
type pending struct {
	Request

	model    domain.ModelID
	leased   string
	leaseEnd time.Time
	done     chan outcome // buffered: what answers it never waits
}

type outcome struct {
	answer Answer
	err    error
}

// queue is one model's requests and workers.
type queue struct {
	pending []*pending
	wake    chan struct{} // closed when a request comes, then replaced
	workers map[string]*Connected
}

// broker hands each model's requests to its workers, in memory: a request lives while its
// asker waits for it, so a restart loses those waiting, which fail as timed out.
type broker struct {
	mu     sync.Mutex
	queues map[domain.ModelID]*queue
	now    func() time.Time
}

func newBroker(now func() time.Time) *broker {
	return &broker{queues: map[domain.ModelID]*queue{}, now: now}
}

func (b *broker) queue(model domain.ModelID) *queue {
	q, ok := b.queues[model]
	if !ok {
		q = &queue{wake: make(chan struct{}), workers: map[string]*Connected{}}
		b.queues[model] = q
	}

	return q
}

// prune forgets the workers not seen within window that hold nothing. b.mu is held.
func (q *queue) prune(now time.Time, window time.Duration) {
	for name, w := range q.workers {
		if w.Seen.Before(now.Add(-window)) && q.holding(name, now) == 0 {
			delete(q.workers, name)
		}
	}
}

// holding is how many requests the worker holds a lease on now.
func (q *queue) holding(name string, now time.Time) int {
	n := 0

	for _, p := range q.pending {
		if p.leased == name && p.leaseEnd.After(now) {
			n++
		}
	}

	return n
}

// next is the first request a worker may take: not past its deadline, and free or its
// lease run out. Also when the next lease runs out, for a worker to wait no longer.
func (q *queue) next(now time.Time) (free *pending, nextLease time.Time) {
	for _, p := range q.pending {
		switch {
		case !p.Deadline.After(now):
			continue
		case p.leased == "" || !p.leaseEnd.After(now):
			return p, time.Time{}
		case nextLease.IsZero() || p.leaseEnd.Before(nextLease):
			nextLease = p.leaseEnd
		}
	}

	return nil, nextLease
}

// remove takes p off its queue; false when an answer took it first. b.mu is held.
func (b *broker) remove(p *pending) bool {
	q := b.queue(p.model)

	i := slices.Index(q.pending, p)
	if i < 0 {
		return false
	}

	q.pending = slices.Delete(q.pending, i, i+1)

	return true
}

// connected are the model's workers connected now, by name.
func (b *broker) connected(model domain.ModelID, window time.Duration) []Connected {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	q := b.queue(model)
	q.prune(now, window)

	out := make([]Connected, 0, len(q.workers))
	for name, w := range q.workers {
		c := *w
		c.Holding = q.holding(name, now)
		out = append(out, c)
	}

	slices.SortFunc(out, func(a, b Connected) int { return strings.Compare(a.Name, b.Name) })

	return out
}

// take hands the worker the model's next request, waiting up to wait for one: none, when
// none came. A worker new to the model is refused past most connected.
func (b *broker) take(ctx context.Context, model domain.ModelID, w Worker, wait, lease, window time.Duration, most int) (Request, bool, error) {
	until := b.now().Add(wait)

	for {
		b.mu.Lock()

		now := b.now()
		q := b.queue(model)
		q.prune(now, window)

		c, known := q.workers[w.Name]
		if !known {
			if len(q.workers) >= most {
				b.mu.Unlock()
				return Request{}, false, fmt.Errorf("%w: the model has %d workers connected, the most it may", domain.ErrModelLimit, most)
			}

			c = &Connected{Name: w.Name, Since: now}
			q.workers[w.Name] = c
		}

		c.Version, c.Seen = w.Version, now

		p, nextLease := q.next(now)
		if p != nil {
			p.leased, p.leaseEnd = w.Name, minTime(now.Add(lease), p.Deadline)
			r := p.Request
			b.mu.Unlock()

			return r, true, nil
		}

		wake := q.wake
		b.mu.Unlock()

		left := until.Sub(now)
		if left <= 0 {
			return Request{}, false, nil
		}

		// A lease running out frees its request: look again then.
		if !nextLease.IsZero() && nextLease.Sub(now) < left {
			left = nextLease.Sub(now)
		}

		timer := time.NewTimer(left)

		select {
		case <-wake:
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return Request{}, false, ctx.Err()
		}

		timer.Stop()
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}

	return b
}

// settle answers or fails the request: the first to does, a later one finds it gone.
func (b *broker) settle(model domain.ModelID, id string, o outcome) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	q := b.queue(model)

	i := slices.IndexFunc(q.pending, func(p *pending) bool { return p.ID == id })
	if i < 0 {
		return fmt.Errorf("%w: no such request waits: it was answered already, or its asker stopped waiting", domain.ErrNotFound)
	}

	p := q.pending[i]

	if o.err == nil && len(o.answer.Answers) != len(p.Items) {
		return fmt.Errorf("%w: %d answers for %d items: one an item, in their order", domain.ErrInvalidInput, len(o.answer.Answers), len(p.Items))
	}

	q.pending = slices.Delete(q.pending, i, i+1)
	p.done <- o

	return nil
}

// ask puts the request on the model's queue and waits for its answer until timeout,
// failing at once when no worker is connected.
func (b *broker) ask(ctx context.Context, model domain.ModelID, items []json.RawMessage, schema json.RawMessage, timeout, window time.Duration) (Answer, error) {
	b.mu.Lock()

	now := b.now()
	q := b.queue(model)
	q.prune(now, window)

	if len(q.workers) == 0 {
		b.mu.Unlock()
		return Answer{}, domain.ErrModelOffline
	}

	p := &pending{
		Request: Request{ID: newRequestID(), Items: items, Schema: schema, Deadline: now.Add(timeout)},
		model:   model,
		done:    make(chan outcome, 1),
	}
	q.pending = append(q.pending, p)

	close(q.wake)
	q.wake = make(chan struct{})
	b.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var stopped error

	select {
	case o := <-p.done:
		return o.answer, o.err
	case <-timer.C:
		stopped = fmt.Errorf("%w: no answer within %s", domain.ErrModelTimeout, timeout)
	case <-ctx.Done():
		stopped = ctx.Err()
	}

	b.mu.Lock()
	removed := b.remove(p)
	b.mu.Unlock()

	if !removed { // answered meanwhile
		o := <-p.done
		return o.answer, o.err
	}

	return Answer{}, stopped
}

func newRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}
