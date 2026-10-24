package tasks_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/tasks"
	"github.com/lein3000zzz/polythought-go/internal/core/services/waits"
)

const owner domain.UserID = "u-1"

var start = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// clock is the time the tests say it is.
type clock struct {
	mu sync.Mutex
	at time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.at
}

func (c *clock) pass(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.at = c.at.Add(d)
}

// memWaits keeps waits in memory, handing tasks out as the database does.
type memWaits struct {
	mu   sync.Mutex
	recs map[domain.ExecutionID]ports.WaitRecord
}

func (m *memWaits) Add(_ context.Context, w ports.WaitRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recs[w.ExecutionID] = w

	return nil
}

func (m *memWaits) CountForOwner(context.Context, domain.UserID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.recs), nil
}

func (m *memWaits) Get(_ context.Context, o domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.recs[id]
	if !ok || w.OwnerID != o {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return w, nil
}

func (m *memWaits) take(id domain.ExecutionID, ok func(ports.WaitRecord) bool) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, found := m.recs[id]
	if !found || !ok(w) {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	delete(m.recs, id)

	return w, nil
}

func (m *memWaits) Take(_ context.Context, o domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	return m.take(id, func(w ports.WaitRecord) bool { return w.OwnerID == o })
}

func (m *memWaits) TakeWithKey(_ context.Context, o domain.UserID, id domain.ExecutionID, key string) (ports.WaitRecord, error) {
	return m.take(id, func(w ports.WaitRecord) bool { return w.OwnerID == o && key != "" && w.Key == key })
}

func (m *memWaits) TakeDue(_ context.Context, now time.Time, _ int) ([]ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []ports.WaitRecord

	for id, w := range m.recs {
		if !w.Until.After(now) {
			out = append(out, w)
			delete(m.recs, id)
		}
	}

	return out, nil
}

func (m *memWaits) TakeForWorkflow(context.Context, domain.UserID, domain.WorkflowID) ([]ports.WaitRecord, error) {
	return nil, nil
}

func (m *memWaits) LeaseTask(_ context.Context, o domain.UserID, lease ports.TaskLease, now time.Time) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var oldest *ports.WaitRecord

	for _, w := range m.recs {
		free := w.Lease == nil || !w.Lease.Until.After(now)
		if w.OwnerID == o && w.Task != nil && free && (oldest == nil || w.CreatedAt.Before(oldest.CreatedAt)) {
			oldest = &w
		}
	}

	if oldest == nil {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	oldest.Lease = &lease
	m.recs[oldest.ExecutionID] = *oldest

	return *oldest, nil
}

func (m *memWaits) ReleaseTask(_ context.Context, o domain.UserID, id domain.ExecutionID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.recs[id]
	if !ok || w.OwnerID != o || w.Task == nil {
		return domain.ErrNotFound
	}

	w.Lease = nil
	m.recs[id] = w

	return nil
}

func (m *memWaits) RenewLeases(_ context.Context, o domain.UserID, holder string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, w := range m.recs {
		if w.OwnerID == o && w.Lease != nil && w.Lease.Holder == holder {
			w.Lease = &ports.TaskLease{Holder: holder, Name: w.Lease.Name, Until: until}
			m.recs[id] = w
		}
	}

	return nil
}

func (m *memWaits) TakeTask(_ context.Context, o domain.UserID, id domain.ExecutionID, holder string) (ports.WaitRecord, error) {
	return m.take(id, func(w ports.WaitRecord) bool {
		return w.OwnerID == o && w.Task != nil && w.Lease != nil && w.Lease.Holder == holder
	})
}

func (m *memWaits) lease(id domain.ExecutionID) *ports.TaskLease {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.recs[id].Lease
}

// capture keeps what reaches it.
type capture struct {
	mu  sync.Mutex
	got []engine.Item
}

func (*capture) Type() engine.NodeType {
	return engine.NodeType{Name: "test.capture", Inputs: []string{engine.DefaultPort}, Outputs: []string{engine.DefaultPort}}
}

func (c *capture) Run(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.got = append(c.got, in[engine.DefaultPort]...)

	return engine.Ports{}, nil
}

func (c *capture) items() []engine.Item {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.got
}

type fixture struct {
	clock    *clock
	repo     *memWaits
	presence *tasks.Presence
	executor *engine.Executor
	waits    *waits.Service
	tasks    *tasks.Service
	captured *capture
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		clock: &clock{at: start}, repo: &memWaits{recs: map[domain.ExecutionID]ports.WaitRecord{}}, captured: &capture{},
	}

	f.presence = tasks.NewPresence(func() time.Duration { return time.Minute }, f.clock.now)

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(triggers.Manual{}))
	require.NoError(t, r.RegisterNode(ai.NewLocalAgent(f.presence, f.clock.now)))
	require.NoError(t, r.RegisterNode(f.captured))

	repo := tasks.Announcing(f.repo, f.presence)

	var err error
	f.executor, err = engine.NewExecutor(observability.NoopUtility(), r,
		engine.WithClock(f.clock.now), engine.WithWaits(repo, func() engine.WaitLimits { return engine.WaitLimits{} }))
	require.NoError(t, err)

	f.waits = waits.New(repo, f.executor, logging.Noop())
	f.tasks = tasks.New(repo, f.waits, f.presence, func() tasks.Config { return tasks.Config{Wait: 500 * time.Millisecond, Lease: 10 * time.Minute} },
		tasks.WithClock(f.clock.now), tasks.WithPoll(10*time.Millisecond))

	return f
}

func agent(grant domain.GrantID, name string, scopes ...domain.Scope) auth.Principal {
	return auth.Principal{User: domain.User{ID: owner}, GrantID: grant, Agent: name, Scopes: scopes}
}

var (
	claude = agent("g-1", "Claude Code", domain.ScopeRead, domain.ScopeTasks)
	codex  = agent("g-2", "Codex", domain.ScopeRead, domain.ScopeTasks)
)

// run runs a workflow of a manual trigger, the local agent node with params and a
// capture, on items.
func (f *fixture) run(t *testing.T, params map[string]any, items ...map[string]any) engine.Result {
	t.Helper()

	w := domain.Workflow{
		ID: "wf-1", OwnerID: owner, Version: 1,
		Nodes: []domain.NodeSpec{
			{ID: "go", Type: triggers.ManualType},
			{ID: "ask", Type: ai.LocalAgentType, Params: params},
			{ID: "got", Type: "test.capture"},
		},
		Connections: []domain.Connection{
			{From: "go", FromPort: engine.DefaultPort, To: "ask", ToPort: engine.DefaultPort},
			{From: "ask", FromPort: engine.DefaultPort, To: "got", ToPort: engine.DefaultPort},
		},
	}

	in := engine.Ports{}
	for _, it := range items {
		in.Add(engine.DefaultPort, engine.Item{JSON: it})
	}

	res, err := f.executor.Execute(context.Background(), w, "go", in)
	require.NoError(t, err)

	return res
}

// waiting has p wait for a task in the background, as an agent's call does.
func (f *fixture) waiting(p auth.Principal) <-chan tasks.Task {
	got := make(chan tasks.Task, 1)

	go func() {
		t, ok, err := f.tasks.Wait(context.Background(), p)
		if err == nil && ok {
			got <- t
		}

		close(got)
	}()

	return got
}

// around waits for the presence to see one of the owner's agents waiting.
func (f *fixture) around(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return f.presence.Around(owner) }, time.Second, time.Millisecond)
}

// A local agent node hands its task to one of its owner's agents waiting for one (D386,
// D412); the answer, held to the task's shape, carries the run on.
func TestTaskFlow(t *testing.T) {
	ctx := context.Background()

	t.Run("none of the owner's agents around: it fails at once", func(t *testing.T) {
		f := newFixture(t)

		res := f.run(t, map[string]any{"prompt": "Look into it"}, map[string]any{"question": "Rain?"})
		assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
		assert.Contains(t, res.Execution.Error, "none of your agents is waiting for tasks")
		assert.Empty(t, f.repo.recs, "nothing waits")
	})

	t.Run("handed to the agent waiting, answered for each item", func(t *testing.T) {
		f := newFixture(t)
		got := f.waiting(claude)
		f.around(t)

		res := f.run(t, map[string]any{"prompt": "Look into it"}, map[string]any{"question": "Rain?"}, map[string]any{"question": "Snow?"})
		require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

		task := <-got
		assert.Equal(t, res.Execution.ID, task.ID)
		assert.Equal(t, ai.CallEach, task.Task.Call)
		assert.Equal(t, ai.AnswerText, task.Task.AnswerAs)
		require.Len(t, task.Task.Items, 2)
		assert.Equal(t, ports.TaskItem{Prompt: "Look into it", Item: map[string]any{"question": "Rain?"}}, task.Task.Items[0])
		assert.Equal(t, start.Add(10*time.Minute), task.Until, "its timeout, 10 minutes unless said")
		assert.Equal(t, start.Add(10*time.Minute), task.LeaseUntil)

		err := f.tasks.Answer(ctx, claude, task.ID, nil, []any{"Likely"})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "one for each of the 2 items")

		err = f.tasks.Answer(ctx, claude, task.ID, nil, []any{"Likely", 3})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "the answer is text")

		require.NoError(t, f.tasks.Answer(ctx, claude, task.ID, nil, []any{"Likely", "Unlikely"}))

		require.Eventually(t, func() bool { return len(f.captured.items()) == 2 }, time.Second, time.Millisecond)
		answer := f.captured.items()[1].JSON["answer"].(map[string]any)
		assert.Equal(t, "Unlikely", answer["text"])
		assert.Equal(t, "Claude Code", answer["by"])
		assert.Equal(t, "Snow?", f.captured.items()[1].JSON["question"], "the item kept")

		require.ErrorIs(t, f.tasks.Answer(ctx, claude, task.ID, nil, []any{"a", "b"}), tasks.ErrNoTask, "once")
	})

	t.Run("together, in fields", func(t *testing.T) {
		f := newFixture(t)
		got := f.waiting(claude)
		f.around(t)

		res := f.run(t, map[string]any{
			"prompt": "Pick the best", "call": "together", "answerAs": "fields",
			"fields": []any{map[string]any{"name": "pick", "type": "text"}, map[string]any{"name": "sure", "type": "true or false"}},
		}, map[string]any{"question": "Rain?"}, map[string]any{"question": "Snow?"})
		require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

		task := <-got
		assert.Equal(t, "Pick the best", task.Task.Prompt)
		assert.Len(t, task.Task.Items, 2, "the items included")
		assert.Contains(t, string(task.Task.Schema), `"pick"`)

		err := f.tasks.Answer(ctx, claude, task.ID, map[string]any{"pick": "Rain?"}, nil)
		require.ErrorIs(t, err, domain.ErrInvalidInput, "sure is missing")

		require.NoError(t, f.tasks.Answer(ctx, claude, task.ID, `{"pick": "Rain?", "sure": true}`, nil), "JSON as text too")

		require.Eventually(t, func() bool { return len(f.captured.items()) == 1 }, time.Second, time.Millisecond)
		answer := f.captured.items()[0].JSON["answer"].(map[string]any)
		assert.Equal(t, map[string]any{"pick": "Rain?", "sure": true}, answer["json"])
	})

	t.Run("one agent's alone, until its lease runs out", func(t *testing.T) {
		f := newFixture(t)
		got := f.waiting(claude)
		f.around(t)

		res := f.run(t, map[string]any{"prompt": "Look into it"}, map[string]any{"question": "Rain?"})
		task := <-got

		_, ok, err := f.tasks.Wait(ctx, codex)
		require.NoError(t, err)
		assert.False(t, ok, "Claude Code has it")
		require.ErrorIs(t, f.tasks.Answer(ctx, codex, task.ID, nil, []any{"No"}), tasks.ErrNoTask)

		f.clock.pass(5 * time.Minute)
		_, _, _ = f.tasks.Wait(ctx, claude) // waiting again holds on to it
		assert.Equal(t, start.Add(15*time.Minute), f.repo.lease(res.Execution.ID).Until)

		f.clock.pass(11 * time.Minute)

		again, ok, err := f.tasks.Wait(ctx, codex)
		require.NoError(t, err)
		require.True(t, ok, "Claude Code let it go")
		assert.Equal(t, task.ID, again.ID)

		require.ErrorIs(t, f.tasks.Answer(ctx, claude, task.ID, nil, []any{"Yes"}), tasks.ErrNoTask, "Codex has it now")
		require.NoError(t, f.tasks.Answer(ctx, codex, task.ID, nil, []any{"No"}))

		require.Eventually(t, func() bool { return len(f.captured.items()) == 1 }, time.Second, time.Millisecond)
		assert.Equal(t, "Codex", f.captured.items()[0].JSON["answer"].(map[string]any)["by"])
	})

	t.Run("one it cannot do fails, saying why", func(t *testing.T) {
		f := newFixture(t)
		got := f.waiting(claude)
		f.around(t)

		res := f.run(t, map[string]any{"prompt": "Look into it"}, map[string]any{"question": "Rain?"})
		task := <-got

		require.ErrorIs(t, f.tasks.Fail(ctx, claude, task.ID, " "), domain.ErrInvalidInput)
		require.NoError(t, f.tasks.Fail(ctx, claude, task.ID, "I cannot reach the forecast"))

		require.Eventually(t, func() bool {
			_, err := f.repo.Get(ctx, owner, res.Execution.ID)
			return err != nil
		}, time.Second, time.Millisecond)
		assert.Empty(t, f.captured.items(), "its items failed")
	})

	t.Run("its timeout", func(t *testing.T) {
		f := newFixture(t)
		got := f.waiting(claude)
		f.around(t)

		f.run(t, map[string]any{"prompt": "Look into it", "timeout": "30m"}, map[string]any{"question": "Rain?"})
		<-got

		due, err := f.repo.TakeDue(ctx, start.Add(31*time.Minute), 10)
		require.NoError(t, err)
		require.Len(t, due, 1)

		res, err := f.executor.Continue(ctx, due[0], engine.Resumption{TimedOut: true})
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionFailed, res.Execution.Status)
		assert.Contains(t, res.Execution.Error, "none of your agents answered within 30m0s")
	})

	t.Run("an agent around a moment ago counts; a while ago, not", func(t *testing.T) {
		f := newFixture(t)

		_, ok, err := f.tasks.Wait(ctx, claude)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.True(t, f.presence.Around(owner), "it waited a moment ago")

		f.clock.pass(2 * time.Minute)
		assert.False(t, f.presence.Around(owner))
	})

	t.Run("the app's: who has it, handed back, answered by the user (D413)", func(t *testing.T) {
		f := newFixture(t)
		user := auth.Principal{User: domain.User{ID: owner}, SessionID: "s-1"}
		got := f.waiting(claude)
		f.around(t)

		res := f.run(t, map[string]any{"prompt": "Look into it"}, map[string]any{"question": "Rain?"})
		<-got

		st, err := f.tasks.Status(ctx, user, "wf-1", res.Execution.ID)
		require.NoError(t, err)
		assert.Equal(t, "Claude Code", st.HeldBy)
		assert.Equal(t, start.Add(10*time.Minute), st.LeaseUntil)
		assert.Equal(t, map[string]bool{"grant:g-1": true}, f.presence.Taking(owner))

		_, err = f.tasks.Status(ctx, user, "wf-other", res.Execution.ID)
		require.ErrorIs(t, err, domain.ErrNotFound, "another workflow's")

		require.ErrorIs(t, f.tasks.HandBack(ctx, claude, "wf-1", res.Execution.ID), domain.ErrForbidden)

		again := f.waiting(codex)
		f.around(t)
		require.NoError(t, f.tasks.HandBack(ctx, user, "wf-1", res.Execution.ID))

		select {
		case task := <-again:
			assert.Equal(t, res.Execution.ID, task.ID, "Codex has it at once")
		case <-time.After(time.Second):
			t.Fatal("not handed to Codex")
		}

		st, err = f.tasks.Status(ctx, user, "wf-1", res.Execution.ID)
		require.NoError(t, err)
		assert.Equal(t, "Codex", st.HeldBy)

		require.ErrorIs(t, f.tasks.AnswerYourself(ctx, claude, "wf-1", res.Execution.ID, nil, []any{"Yes"}), domain.ErrForbidden)
		require.ErrorIs(t, f.tasks.AnswerYourself(ctx, user, "wf-1", res.Execution.ID, nil, []any{}), domain.ErrInvalidInput)
		require.NoError(t, f.tasks.AnswerYourself(ctx, user, "wf-1", res.Execution.ID, nil, []any{"Yes, I looked"}))

		require.Eventually(t, func() bool { return len(f.captured.items()) == 1 }, time.Second, time.Millisecond)
		assert.Equal(t, tasks.ByYou, f.captured.items()[0].JSON["answer"].(map[string]any)["by"])
		require.ErrorIs(t, f.tasks.Answer(ctx, codex, res.Execution.ID, nil, []any{"No"}), tasks.ErrNoTask, "answered already")
	})

	t.Run("the tasks scope, and agents and tokens only", func(t *testing.T) {
		f := newFixture(t)

		_, _, err := f.tasks.Wait(ctx, agent("g-3", "Gemini", domain.ScopeRead, domain.ScopeBuild))
		require.ErrorIs(t, err, domain.ErrInsufficientScope)

		_, _, err = f.tasks.Wait(ctx, auth.Principal{User: domain.User{ID: owner}, SessionID: "s-1"})
		require.ErrorIs(t, err, domain.ErrInvalidInput)

		token := auth.Principal{User: domain.User{ID: owner}, TokenID: "t-1", TokenName: "laptop", Scopes: []domain.Scope{domain.ScopeTasks}}
		_, ok, err := f.tasks.Wait(ctx, token)
		require.NoError(t, err)
		assert.False(t, ok)
	})
}
