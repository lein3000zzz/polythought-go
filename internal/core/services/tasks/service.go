// Package tasks hands the tasks of local agent nodes to their owners' own agents (D386,
// D412): an agent with the tasks scope waits for one, which is handed to it alone for a
// lease, and answers it, the answer held to the task's shape; the run waiting on it then
// carries on. Which owners have an agent waiting is kept here too, for the node to fail
// at once where none is.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	// poll is how often a waiting agent looks again, besides being woken as a task comes:
	// for leases that ran out, and tasks kept elsewhere.
	poll = 5 * time.Second

	maxReason = 500 // what an agent may say of why it could not, in characters
)

// ErrNoTask means no task of the agent's has the ID: it was answered, it timed out, or
// another agent has it.
var ErrNoTask = fmt.Errorf("%w: no task of yours has this ID: it was answered, it timed out, or another agent has it",
	domain.ErrNotFound)

// Presence is which owners have an agent taking tasks: one waiting for one now, or that
// waited a moment ago (Window), as a task's node asks before it hands one out. It also
// wakes an owner's waiting agents as a task comes.
type Presence struct {
	window func() time.Duration
	now    func() time.Time

	mu      sync.Mutex
	waiting map[domain.UserID]int                  // agents waiting now
	seen    map[domain.UserID]map[string]time.Time // when each holder last waited or answered
	wake    map[domain.UserID]chan struct{}        // closed as a task comes, then replaced
}

func NewPresence(window func() time.Duration, now func() time.Time) *Presence {
	return &Presence{
		window: window, now: now,
		waiting: map[domain.UserID]int{}, seen: map[domain.UserID]map[string]time.Time{}, wake: map[domain.UserID]chan struct{}{},
	}
}

var _ ports.TaskAgents = (*Presence)(nil)

// Around says one of owner's agents waits for a task, or did within the window.
func (p *Presence) Around(owner domain.UserID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.waiting[owner] > 0 {
		return true
	}

	since := p.now().Add(-p.window())

	for holder, at := range p.seen[owner] {
		if at.After(since) {
			return true
		}

		delete(p.seen[owner], holder) // gone a while: forgotten
	}

	return false
}

// Wake tells owner's waiting agents a task came.
func (p *Presence) Wake(owner domain.UserID) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ch, ok := p.wake[owner]; ok {
		close(ch)
		delete(p.wake, owner)
	}
}

// enter counts holder as waiting for owner's tasks until leave.
func (p *Presence) enter(owner domain.UserID, holder string) (leave func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.waiting[owner]++
	p.mark(owner, holder)

	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()

		if p.waiting[owner]--; p.waiting[owner] <= 0 {
			delete(p.waiting, owner)
		}

		p.mark(owner, holder)
	}
}

// touch counts holder as around, as an answer shows it is.
func (p *Presence) touch(owner domain.UserID, holder string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.mark(owner, holder)
}

// mark notes holder was around now. Under p.mu.
func (p *Presence) mark(owner domain.UserID, holder string) {
	if p.seen[owner] == nil {
		p.seen[owner] = map[string]time.Time{}
	}

	p.seen[owner][holder] = p.now()
}

// signal is what closes as owner's next task comes. Take it before looking, so a task
// that comes meanwhile is not missed.
func (p *Presence) signal(owner domain.UserID) <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()

	ch, ok := p.wake[owner]
	if !ok {
		ch = make(chan struct{})
		p.wake[owner] = ch
	}

	return ch
}

// Announcing keeps waits in repo, waking the owner's agents as one with a task is kept.
func Announcing(repo ports.WaitRepository, p *Presence) ports.WaitRepository {
	return announcing{WaitRepository: repo, presence: p}
}

type announcing struct {
	ports.WaitRepository
	presence *Presence
}

func (a announcing) Add(ctx context.Context, w ports.WaitRecord) error {
	if err := a.WaitRepository.Add(ctx, w); err != nil {
		return err
	}

	if w.Task != nil {
		a.presence.Wake(w.OwnerID)
	}

	return nil
}

// Answerer carries on a task's execution with its answer, once it is taken from holder:
// the waits service.
type Answerer interface {
	AnswerTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID, holder string, payload map[string]any) error
}

// Config is how long a waiting agent's call is held open, within its client's timeout, and
// how long a task is handed to an agent before it goes back to the others.
type Config struct {
	Wait  time.Duration
	Lease time.Duration
}

type Service struct {
	repo     ports.WaitRepository
	answerer Answerer
	presence *Presence
	config   func() Config
	now      func() time.Time
	poll     time.Duration
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithPoll replaces how often a waiting agent looks again, for tests.
func WithPoll(d time.Duration) Option {
	return func(s *Service) { s.poll = d }
}

func New(repo ports.WaitRepository, answerer Answerer, presence *Presence, config func() Config, opts ...Option) *Service {
	s := &Service{repo: repo, answerer: answerer, presence: presence, config: config, now: time.Now, poll: poll}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Task is a task handed to an agent: the run it is of, what it asks, and until when the
// run waits for it and the agent has it.
type Task struct {
	ID         domain.ExecutionID
	WorkflowID domain.WorkflowID
	NodeID     domain.NodeID
	Task       ports.WaitTask
	Until      time.Time // when the run stops waiting
	LeaseUntil time.Time // when it goes back to the others, unless the agent waits again or answers
}

// Wait hands the agent the owner's oldest task no other agent has, waiting for one as long
// as the config says; false when none came. Waiting again holds on to the tasks it has.
func (s *Service) Wait(ctx context.Context, p auth.Principal) (Task, bool, error) {
	holder, err := holderOf(p)
	if err != nil {
		return Task{}, false, err
	}

	owner, cfg := p.User.ID, s.config()

	leave := s.presence.enter(owner, holder)
	defer leave()

	if err := s.repo.RenewLeases(ctx, owner, holder, s.now().Add(cfg.Lease)); err != nil {
		return Task{}, false, err
	}

	timer := time.NewTimer(cfg.Wait)
	defer timer.Stop()

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	for {
		woken := s.presence.signal(owner)
		now := s.now()

		rec, err := s.repo.LeaseTask(ctx, owner, holder, now, now.Add(cfg.Lease))
		switch {
		case err == nil:
			return taskOf(rec), true, nil
		case !errors.Is(err, domain.ErrNotFound):
			return Task{}, false, err
		}

		select {
		case <-ctx.Done():
			return Task{}, false, ctx.Err()
		case <-timer.C:
			return Task{}, false, nil
		case <-woken:
		case <-ticker.C:
		}
	}
}

func taskOf(rec ports.WaitRecord) Task {
	t := Task{ID: rec.ExecutionID, WorkflowID: rec.WorkflowID, NodeID: rec.NodeID, Until: rec.Until}

	if rec.Task != nil {
		t.Task = *rec.Task
	}

	if rec.Lease != nil {
		t.LeaseUntil = rec.Lease.Until
	}

	return t
}

// Answer answers a task the agent has, held to its shape: for each item an answer, in
// order, or one for all of them together. A wrong one is refused, saying why, and the
// task stays the agent's to answer again.
func (s *Service) Answer(ctx context.Context, p auth.Principal, id domain.ExecutionID, answer any, answers []any) error {
	holder, err := holderOf(p)
	if err != nil {
		return err
	}

	t, err := s.held(ctx, p, holder, id)
	if err != nil {
		return err
	}

	payload := map[string]any{engine.AnsweredBy: nameOf(p)}

	if t.Call == ai.CallTogether {
		checked, err := ai.CheckAnswer(t, answer)
		if err != nil {
			return fmt.Errorf("%w: answer: %v", domain.ErrInvalidInput, err)
		}

		payload[ai.TaskAnswer] = checked
	} else {
		if len(answers) != len(t.Items) {
			return fmt.Errorf("%w: answers: one for each of the %d items, in order; %d given", domain.ErrInvalidInput, len(t.Items), len(answers))
		}

		problems := domain.ValidationError{Base: domain.ErrInvalidInput}
		checked := make([]any, len(answers))

		for i, a := range answers {
			if checked[i], err = ai.CheckAnswer(t, a); err != nil {
				problems.Add(domain.Problem{Param: fmt.Sprintf("answers[%d]", i), Message: err.Error()})
			}
		}

		if err := problems.Err(); err != nil {
			return err
		}

		payload[ai.TaskAnswers] = checked
	}

	return s.carryOn(ctx, p, holder, id, payload)
}

// Fail says the agent cannot do a task it has, and why: its items fail with that.
func (s *Service) Fail(ctx context.Context, p auth.Principal, id domain.ExecutionID, reason string) error {
	holder, err := holderOf(p)
	if err != nil {
		return err
	}

	switch reason = strings.TrimSpace(reason); {
	case reason == "":
		return fmt.Errorf("%w: reason: say why you cannot do it", domain.ErrInvalidInput)
	case utf8.RuneCountInString(reason) > maxReason:
		return fmt.Errorf("%w: reason: %d characters at most", domain.ErrInvalidInput, maxReason)
	}

	if _, err := s.held(ctx, p, holder, id); err != nil {
		return err
	}

	return s.carryOn(ctx, p, holder, id, map[string]any{engine.AnsweredBy: nameOf(p), ai.TaskFailed: reason})
}

// held is a task the agent has.
func (s *Service) held(ctx context.Context, p auth.Principal, holder string, id domain.ExecutionID) (ports.WaitTask, error) {
	rec, err := s.repo.Get(ctx, p.User.ID, id)

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return ports.WaitTask{}, ErrNoTask
	case err != nil:
		return ports.WaitTask{}, err
	case rec.Task == nil || rec.Lease == nil || rec.Lease.Holder != holder:
		return ports.WaitTask{}, ErrNoTask
	}

	return *rec.Task, nil
}

func (s *Service) carryOn(ctx context.Context, p auth.Principal, holder string, id domain.ExecutionID, payload map[string]any) error {
	err := s.answerer.AnswerTask(ctx, p.User.ID, id, holder, payload)
	if errors.Is(err, domain.ErrNotFound) {
		return ErrNoTask // taken meanwhile: timed out, or cancelled
	}

	if err == nil {
		s.presence.touch(p.User.ID, holder)
	}

	return err
}

// holderOf is who a task is handed to: the agent's grant, or the API token.
func holderOf(p auth.Principal) (string, error) {
	if err := p.Need(domain.ScopeTasks); err != nil {
		return "", err
	}

	switch {
	case p.GrantID != "":
		return "grant:" + string(p.GrantID), nil
	case p.TokenID != "":
		return "token:" + string(p.TokenID), nil
	}

	return "", fmt.Errorf("%w: tasks are for your agents and API tokens to take", domain.ErrInvalidInput)
}

// nameOf is the agent by its name, as the run's answer names it.
func nameOf(p auth.Principal) string {
	switch {
	case p.GrantID != "" && p.Agent != "":
		return p.Agent
	case p.TokenName != "":
		return "the API token “" + p.TokenName + "”"
	}

	return "an API token"
}
