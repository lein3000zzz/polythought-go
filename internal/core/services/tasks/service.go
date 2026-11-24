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
	"slices"
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

	p.prune(owner)

	return p.waiting[owner] > 0 || len(p.seen[owner]) > 0
}

// prune forgets owner's holders gone a while, every one of them, and owner once none is
// left (A26). Under p.mu.
func (p *Presence) prune(owner domain.UserID) {
	since := p.now().Add(-p.window())

	for holder, at := range p.seen[owner] {
		if !at.After(since) {
			delete(p.seen[owner], holder)
		}
	}

	if len(p.seen[owner]) == 0 {
		delete(p.seen, owner)
	}
}

// Taking is which of owner's grants and tokens take tasks now, by holder ("grant:<id>",
// "token:<id>"), for Settings to mark them (D413).
func (p *Presence) Taking(owner domain.UserID) map[string]bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.prune(owner)

	out := map[string]bool{}
	for holder := range p.seen[owner] {
		out[holder] = true
	}

	return out
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

// mark notes holder was around now, forgetting those gone a while. Under p.mu.
func (p *Presence) mark(owner domain.UserID, holder string) {
	p.prune(owner)

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

// Answerer carries on a task's execution with its answer, once it is taken: from holder,
// or with its key, as the user answers it: the waits service.
type Answerer interface {
	AnswerTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID, holder string, payload map[string]any) error
	Answer(ctx context.Context, owner domain.UserID, id domain.ExecutionID, key string, payload map[string]any) error
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
// run waits for it and the agent has it. A task too big to take at once is handed in
// parts, each answered on its own, or, answered together, in pages (D436).
type Task struct {
	ID         domain.ExecutionID
	WorkflowID domain.WorkflowID
	NodeID     domain.NodeID
	// Task is what it asks: its part's items, or a task taken together's first page.
	Task  ports.WaitTask
	Part  int       // which part, from 0
	Parts int       // of how many
	First int       // the first of the task's items in it, from 0
	Pages int       // a task taken together: how many pages its items come in
	Until time.Time // when the run stops waiting
	// LeaseUntil is when the part goes back to the others, unless the agent waits again or
	// answers.
	LeaseUntil time.Time
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

		rec, part, err := s.repo.LeaseTask(ctx, owner, ports.TaskLease{Holder: holder, Name: nameOf(p), Until: now.Add(cfg.Lease)}, now)
		switch {
		case err == nil:
			return taskOf(rec, part), true, nil
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

// taskOf is a task's part as an agent takes it: its items alone, or a task taken
// together's first page of them.
func taskOf(rec ports.WaitRecord, part int) Task {
	t := Task{ID: rec.ExecutionID, WorkflowID: rec.WorkflowID, NodeID: rec.NodeID, Until: rec.Until, Part: part, Parts: len(rec.Parts)}

	if rec.Task == nil {
		return t
	}

	t.Task = *rec.Task
	t.Task.Splits = nil

	if p, ok := partOf(rec, part); ok {
		if p.Lease != nil {
			t.LeaseUntil = p.Lease.Until
		}

		if t.Task.Call != ai.CallTogether {
			t.First, t.Task.Items = p.First, rec.Task.Items[p.First:p.First+p.Items]
			return t
		}
	}

	pages := pagesOf(*rec.Task)
	t.Pages, t.Task.Items = len(pages), pages[0]

	return t
}

// partOf is one of a task's parts, by its index.
func partOf(rec ports.WaitRecord, part int) (ports.TaskPart, bool) {
	for _, p := range rec.Parts {
		if p.Index == part {
			return p, true
		}
	}

	return ports.TaskPart{}, false
}

// pagesOf are a task's items, a page at a time, as an agent reads them before it answers
// them together (D436): one page, empty, without items.
func pagesOf(t ports.WaitTask) [][]ports.TaskItem {
	splits, err := ai.Splits(t)
	if err != nil {
		// The node split it so: every item fits a page.
		splits = nil
	}

	starts := append([]int{0}, splits...)
	pages := make([][]ports.TaskItem, len(starts))

	for i, first := range starts {
		end := len(t.Items)
		if i+1 < len(starts) {
			end = starts[i+1]
		}

		pages[i] = t.Items[first:end]
	}

	return pages
}

// Page is one page of the items of a task the agent has, taken together, from 1 (D436).
func (s *Service) Page(ctx context.Context, p auth.Principal, id domain.ExecutionID, page int) ([]ports.TaskItem, int, error) {
	holder, err := holderOf(p)
	if err != nil {
		return nil, 0, err
	}

	rec, _, err := s.held(ctx, p, holder, id, 0)
	if err != nil {
		return nil, 0, err
	}

	pages := pagesOf(*rec.Task)
	if page < 1 || page > len(pages) {
		return nil, 0, fmt.Errorf("%w: page: from 1 to %d", domain.ErrInvalidInput, len(pages))
	}

	s.presence.touch(p.User.ID, holder)

	return pages[page-1], len(pages), nil
}

// Answer answers a part of a task the agent has, held to its shape: for each of its items
// an answer, in order, or one for all of them together. A wrong one is refused, saying
// why, and the part stays the agent's to answer again. Once every part is answered, the
// run carries on; until then, left says how many are not.
func (s *Service) Answer(ctx context.Context, p auth.Principal, id domain.ExecutionID, part int, answer any, answers []any) (left int, err error) {
	holder, err := holderOf(p)
	if err != nil {
		return 0, err
	}

	rec, held, err := s.held(ctx, p, holder, id, part)
	if err != nil {
		return 0, err
	}

	t := *rec.Task
	if t.Call != ai.CallTogether {
		t.Items = t.Items[held.First : held.First+held.Items]
	}

	payload, err := checked(t, answer, answers)
	if err != nil {
		return 0, err
	}

	given := []any{payload[ai.TaskAnswer]}
	if t.Call != ai.CallTogether {
		given, _ = payload[ai.TaskAnswers].([]any)
	}

	parts, err := s.repo.AnswerPart(ctx, p.User.ID, id, part, holder, given, nameOf(p))
	if errors.Is(err, domain.ErrNotFound) {
		return 0, ErrNoTask
	} else if err != nil {
		return 0, err
	}

	if !ports.Answered(parts) {
		s.presence.touch(p.User.ID, holder)

		for _, p := range parts {
			if p.Answers == nil {
				left++
			}
		}

		return left, nil
	}

	return 0, s.carryOn(ctx, p, holder, id, whole(rec.Task.Call, parts))
}

// whole is every part's answers as the run takes them: an answer per item in order, or the
// one answer, and who gave them.
func whole(call string, parts []ports.TaskPart) map[string]any {
	var (
		answers []any
		by      []string
	)

	for _, p := range parts {
		answers = append(answers, p.Answers...)

		if p.AnsweredBy != "" && !slices.Contains(by, p.AnsweredBy) {
			by = append(by, p.AnsweredBy)
		}
	}

	payload := map[string]any{engine.AnsweredBy: strings.Join(by, ", ")}

	if call == ai.CallTogether {
		if len(answers) > 0 {
			payload[ai.TaskAnswer] = answers[0]
		}
	} else {
		payload[ai.TaskAnswers] = answers
	}

	return payload
}

// checked is an answer held to its task's shape, as the node takes it.
func checked(t ports.WaitTask, answer any, answers []any) (map[string]any, error) {
	if t.Call == ai.CallTogether {
		got, err := ai.CheckAnswer(t, answer)
		if err != nil {
			return nil, fmt.Errorf("%w: answer: %v", domain.ErrInvalidInput, err)
		}

		return map[string]any{ai.TaskAnswer: got}, nil
	}

	if len(answers) != len(t.Items) {
		return nil, fmt.Errorf("%w: answers: one for each of the %d items, in order; %d given", domain.ErrInvalidInput, len(t.Items), len(answers))
	}

	problems := domain.ValidationError{Base: domain.ErrInvalidInput}
	got := make([]any, len(answers))

	for i, a := range answers {
		var err error
		if got[i], err = ai.CheckAnswer(t, a); err != nil {
			problems.Add(domain.Problem{Param: fmt.Sprintf("answers[%d]", i), Message: err.Error()})
		}
	}

	if err := problems.Err(); err != nil {
		return nil, err
	}

	return map[string]any{ai.TaskAnswers: got}, nil
}

// Fail says the agent cannot do a part of a task it has, and why: the task's items fail
// with that.
func (s *Service) Fail(ctx context.Context, p auth.Principal, id domain.ExecutionID, part int, reason string) error {
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

	if _, _, err := s.held(ctx, p, holder, id, part); err != nil {
		return err
	}

	// A part it cannot do fails the whole task: the run takes its items together.
	return s.carryOn(ctx, p, holder, id, map[string]any{engine.AnsweredBy: nameOf(p), ai.TaskFailed: reason})
}

// Status is a run's task as the app shows it (D413): what it asks, whether an agent has
// it and until when, and when the run stops waiting; and, in parts, how many are answered.
type Status struct {
	Task
	HeldBy   string // the agents or tokens that have its parts, by their names; empty while none does
	Answered int    // of its parts
}

// Status is the task a run of the user's waits on; domain.ErrNotFound for a run waiting on
// none, or not waiting.
func (s *Service) Status(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID) (Status, error) {
	rec, err := s.task(ctx, p, workflow, id)
	if err != nil {
		return Status{}, err
	}

	st := Status{Task: taskOf(rec, 0)}
	st.Task.Task = *rec.Task // the whole of it, for its user
	st.Task.Task.Splits, st.First, st.LeaseUntil = nil, 0, time.Time{}

	var names []string

	for _, p := range rec.Parts {
		switch {
		case p.Answers != nil:
			st.Answered++
		case p.Lease != nil && p.Lease.Until.After(s.now()):
			if !slices.Contains(names, p.Lease.Name) {
				names = append(names, p.Lease.Name)
			}

			if p.Lease.Until.After(st.LeaseUntil) {
				st.LeaseUntil = p.Lease.Until
			}
		}
	}

	st.HeldBy = strings.Join(names, ", ")

	return st, nil
}

// HandBack takes a task from the agent that has it, for another of the user's agents to
// take at once (D413). The user's, at a browser.
func (s *Service) HandBack(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID) error {
	if !p.Itself() {
		return fmt.Errorf("%w: handing a task back is the user's, at a browser", domain.ErrForbidden)
	}

	if _, err := s.task(ctx, p, workflow, id); err != nil {
		return err
	}

	if err := s.repo.ReleaseTask(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.presence.Wake(p.User.ID)

	return nil
}

// AnswerYourself answers a task as the user, at a browser, held to its shape as an
// agent's answer is (D413), whichever agent has it.
func (s *Service) AnswerYourself(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID, answer any, answers []any) error {
	if !p.Itself() {
		return fmt.Errorf("%w: a task is your agents' to answer with answer_task; at a browser, you can answer it yourself", domain.ErrForbidden)
	}

	rec, err := s.task(ctx, p, workflow, id)
	if err != nil {
		return err
	}

	payload, err := checked(*rec.Task, answer, answers)
	if err != nil {
		return err
	}

	payload[engine.AnsweredBy] = ByYou

	if err := s.answerer.Answer(ctx, p.User.ID, id, rec.Key, payload); errors.Is(err, domain.ErrNotFound) {
		return ErrNoTask
	} else if err != nil {
		return err
	}

	return nil
}

// ByYou is who answered a task the user answered themself.
const ByYou = "you"

// task is the task a run of the user's, of workflow, waits on.
func (s *Service) task(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, id domain.ExecutionID) (ports.WaitRecord, error) {
	rec, err := s.repo.Get(ctx, p.User.ID, id)

	switch {
	case err != nil:
		return ports.WaitRecord{}, err
	case rec.WorkflowID != workflow || rec.Task == nil:
		return ports.WaitRecord{}, fmt.Errorf("%w: the run waits on no task", domain.ErrNotFound)
	}

	return rec, nil
}

// held is a part of a task the agent has, not answered yet.
func (s *Service) held(ctx context.Context, p auth.Principal, holder string, id domain.ExecutionID, part int) (ports.WaitRecord, ports.TaskPart, error) {
	rec, err := s.repo.Get(ctx, p.User.ID, id)

	switch {
	case errors.Is(err, domain.ErrNotFound):
		return ports.WaitRecord{}, ports.TaskPart{}, ErrNoTask
	case err != nil:
		return ports.WaitRecord{}, ports.TaskPart{}, err
	case rec.Task == nil:
		return ports.WaitRecord{}, ports.TaskPart{}, ErrNoTask
	}

	held, ok := partOf(rec, part)
	if !ok || held.Lease == nil || held.Lease.Holder != holder || held.Answers != nil {
		return ports.WaitRecord{}, ports.TaskPart{}, ErrNoTask
	}

	return rec, held, nil
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
