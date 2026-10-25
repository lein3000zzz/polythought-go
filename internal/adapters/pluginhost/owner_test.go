package pluginhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/waits"
)

func TestMessages(t *testing.T) {
	tg := newTelegram()
	_, r := startWith(t, pluginhost.Deps{Telegram: pluginhost.Telegram{Messenger: tg}})

	assert.Empty(t, kept(t, r, "plugin.echo.tell", map[string]any{"text": "Rain & sun"})["error"])
	kept(t, r, "plugin.echo.tell", map[string]any{"text": "<b>Rain</b>", "html": true})

	require.Len(t, tg.messages, 2)
	assert.Equal(t, "🧩 <b>Echo</b>\nRain &amp; sun", tg.messages[0].HTML, "the plugin's name first, text escaped (D308)")
	assert.Equal(t, "🧩 <b>Echo</b>\n<b>Rain</b>", tg.messages[1].HTML, "its HTML as it wrote it")

	assert.Equal(t, "the message is empty", kept(t, r, "plugin.echo.tell", map[string]any{"text": " "})["error"])

	_, none := start(t)
	assert.Equal(t, "this is not available here", kept(t, none, "plugin.echo.tell", map[string]any{"text": "hi"})["error"])
}

// asked runs the ask node over two items, as the executor would: it waits.
func asked(t *testing.T, r *engine.Registry, params map[string]any) (*engine.Wait, engine.Ports) {
	t.Helper()

	n, _ := r.Node("plugin.echo.ask")
	in := items(map[string]any{"a": 1}, map[string]any{"a": 2})

	_, err := runWith(context.Background(), n, engine.LiteralParams(n.Type(), params), in)

	var wait *engine.Wait
	require.ErrorAs(t, err, &wait)

	return wait, in
}

// resumed resumes the ask node's run with r, as the executor would after the wait.
func resumed(t *testing.T, reg *engine.Registry, in engine.Ports, r engine.Resumption) (engine.Ports, error) {
	t.Helper()

	n, _ := reg.Node("plugin.echo.ask")

	// The engine keeps a wait's state through JSON.
	raw, err := json.Marshal(r.State)
	require.NoError(t, err)

	r.State = nil
	require.NoError(t, json.Unmarshal(raw, &r.State))

	return n.(engine.Resumer).Resume(context.Background(), engine.RunContext{
		ExecutionID: "ex-1", WorkflowID: "wf-1", OwnerID: "alice", NodeID: "n-1", StartedAt: time.Now(),
		Params: engine.LiteralParams(n.Type(), nil),
	}, in, r)
}

func TestQuestions(t *testing.T) {
	tg := newTelegram()
	_, r := startWith(t, pluginhost.Deps{Telegram: pluginhost.Telegram{Messenger: tg, Asker: tg, Reminder: tg}})

	t.Run("asked, the run waits, reminded, then answered", func(t *testing.T) {
		before := time.Now()
		wait, in := asked(t, r, nil)

		require.Len(t, tg.asked, 1)
		q := tg.asked[0]
		assert.Equal(t, []string{"Yes", "No"}, q.Options)
		assert.Equal(t, domain.ExecutionID("ex-1"), q.ExecutionID, "a press answers this run")
		assert.Equal(t, q.Key, wait.Key, "and only this question")
		assert.Equal(t, "🧩 <b>Echo</b>\nRain &lt;tomorrow&gt;?", q.HTML)
		assert.WithinRange(t, wait.Until, before.Add(45*time.Minute), time.Now().Add(45*time.Minute), "the reminder, at three quarters of the hour")

		again, err := resumed(t, r, in, engine.Resumption{TimedOut: true, State: wait.State})
		assert.Nil(t, again)

		var reminded *engine.Wait
		require.ErrorAs(t, err, &reminded, "reminded, it waits on")
		assert.Equal(t, []int{101}, tg.reminded)
		assert.Equal(t, wait.Key, reminded.Key)
		assert.WithinRange(t, reminded.Until, before.Add(time.Hour), time.Now().Add(time.Hour))

		out, err := resumed(t, r, in, engine.Resumption{Payload: map[string]any{"option": json.Number("1")}, State: reminded.State})
		require.NoError(t, err)
		require.Len(t, out[engine.DefaultPort], 2, "the plugin's Resume, with the items the run had")
		assert.Equal(t, map[string]any{"option": json.Number("1"), "label": "No", "items": json.Number("2")}, out[engine.DefaultPort][0].JSON,
			"the answer, and what it kept")
		assert.Equal(t, "🧩 <b>Echo</b>\nRain &lt;tomorrow&gt;?\n\n✅ <b>No</b>", tg.closed[101], "the question closed, saying which")
	})

	t.Run("no answer in time", func(t *testing.T) {
		wait, in := asked(t, r, map[string]any{"timeout": "5m"})
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), wait.Until, time.Minute, "too short to be reminded of")

		out, err := resumed(t, r, in, engine.Resumption{TimedOut: true, State: wait.State})
		require.NoError(t, err)
		assert.Len(t, out["timeout"], 2)
		assert.Contains(t, tg.closed[102], "⌛ <i>No answer in time.</i>")
	})

	t.Run("one question a run, and one not waited on is closed", func(t *testing.T) {
		got := kept(t, r, "plugin.echo.ask", map[string]any{"how": "twice"})
		assert.Equal(t, "a run asks one question", got["error"])
		assert.Contains(t, tg.closed[103], "No longer waiting for an answer", "its buttons would answer nothing")

		kept(t, r, "plugin.echo.ask", map[string]any{"how": "nowait"})
		assert.Contains(t, tg.closed[104], "No longer waiting for an answer")
	})

	t.Run("no wait without a question, nor from a node that cannot resume", func(t *testing.T) {
		_, err := run(t, r, "plugin.echo.ask", map[string]any{"how": "unasked"}, items(map[string]any{}))
		require.EqualError(t, err, "plugin echo waited, but asked no question")

		var wait *engine.Wait
		assert.False(t, errors.As(err, &wait))

		_, err = run(t, r, "plugin.echo.misbehave", map[string]any{"how": "wait"}, items(map[string]any{}))
		require.EqualError(t, err, `plugin echo: node "misbehave" asked, but has no Resume to finish its run`, "the SDK says so first")
	})

	t.Run("never in a trigger", func(t *testing.T) {
		tr, _ := r.Trigger("plugin.echo.tick")

		var got any

		err := tr.Start(context.Background(), engine.TriggerContext{
			WorkflowID: "wf-1", OwnerID: "alice", NodeID: "tick", Params: engine.Params{"times": 1, "ask": true},
		}, func(_ context.Context, out engine.Ports) error {
			got = out[engine.DefaultPort][0].JSON["asked"]
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, "a trigger cannot ask: only a node's run waits", got)
	})
}

func TestOrders(t *testing.T) {
	tr := &trading{}
	_, r := startWith(t, pluginhost.Deps{Trading: pluginhost.Trading{Orders: tr, Batches: tr, Canceller: tr}})

	t.Run("placed for the workflow, together, each saying how it went", func(t *testing.T) {
		got := kept(t, r, "plugin.echo.trade", nil)
		assert.Equal(t, `o-1 filled 0.5; order 1: side "hold": buy or sell; o-2 filled 0.5`, got["results"])

		assert.Equal(t, []int{2}, tr.batches, "the two it could try, together (D244)")
		require.Len(t, tr.placed, 2)

		o := tr.placed[0]
		assert.Equal(t, domain.WorkflowID("wf-1"), o.WorkflowID)
		assert.Equal(t, domain.ExecutionID("ex-1"), o.ExecutionID, "the run's, as Place order's are")
		assert.Equal(t, domain.NodeID("n-1"), o.NodeID)
		assert.Equal(t, domain.UserID("alice"), o.OwnerID)
		assert.True(t, o.Price.Equal(decimal.RequireFromString("0.5")))
		assert.Empty(t, o.Mode, "the trading service's to say, as the workflow trades")
	})

	t.Run("one at a time without a batch placer", func(t *testing.T) {
		single := &trading{}
		_, r := startWith(t, pluginhost.Deps{Trading: pluginhost.Trading{Orders: single}})

		kept(t, r, "plugin.echo.trade", nil)
		assert.Len(t, single.placed, 2)
		assert.Empty(t, single.batches)
	})

	t.Run("cancelled, the workflow's own", func(t *testing.T) {
		got := kept(t, r, "plugin.echo.trade", map[string]any{"cancel": true})
		assert.Equal(t, json.Number("1"), got["cancelled"])

		require.Len(t, tr.cancelled, 1)
		assert.Equal(t, domain.WorkflowID("wf-1"), tr.cancelled[0].WorkflowID)
		assert.True(t, tr.cancelled[0].All)
	})

	t.Run("only from a node marked trades", func(t *testing.T) {
		got := kept(t, r, "plugin.echo.untraded", nil)
		assert.Equal(t, true, got["notAllowed"])
		assert.Equal(t, "plugin.echo.untraded does not trade: the manifest marks trades the nodes that place orders", got["error"])
	})
}

// memWaits keeps waits in memory.
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

func (m *memWaits) CountForOwner(context.Context, domain.UserID) (int, error) { return 0, nil }

func (m *memWaits) Get(_ context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.recs[id]
	if !ok || w.OwnerID != owner {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return w, nil
}

func (m *memWaits) Take(context.Context, domain.UserID, domain.ExecutionID) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}

func (m *memWaits) TakeWithKey(_ context.Context, owner domain.UserID, id domain.ExecutionID, key string) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.recs[id]
	if !ok || w.OwnerID != owner || key == "" || w.Key != key {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	delete(m.recs, id)

	return w, nil
}

func (m *memWaits) TakeDue(context.Context, time.Time, int) ([]ports.WaitRecord, error) {
	return nil, nil
}

func (m *memWaits) TakeForWorkflow(context.Context, domain.UserID, domain.WorkflowID) ([]ports.WaitRecord, error) {
	return nil, nil
}

// capture keeps the items that reach it.
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

func TestQuestionFlow(t *testing.T) {
	ctx := context.Background()
	tg := newTelegram()

	_, r := startWith(t, pluginhost.Deps{Telegram: pluginhost.Telegram{Messenger: tg, Asker: tg}}) // Manual among the types

	answered := &capture{}
	require.NoError(t, r.RegisterNode(answered))

	repo := &memWaits{recs: map[domain.ExecutionID]ports.WaitRecord{}}
	ex, err := engine.NewExecutor(observability.NoopUtility(), r,
		engine.WithWaits(repo, func() engine.WaitLimits { return engine.WaitLimits{} }))
	require.NoError(t, err)

	w := domain.Workflow{
		ID: "wf-1", OwnerID: "alice", Version: 1,
		Nodes: []domain.NodeSpec{
			{ID: "go", Type: triggers.ManualType},
			{ID: "ask", Type: "plugin.echo.ask"},
			{ID: "answered", Type: "test.capture"},
		},
		Connections: []domain.Connection{
			{From: "go", FromPort: engine.DefaultPort, To: "ask", ToPort: engine.DefaultPort},
			{From: "ask", FromPort: engine.DefaultPort, To: "answered", ToPort: engine.DefaultPort},
		},
	}
	require.NoError(t, r.Validate(w))

	res, err := ex.Execute(ctx, w, "go", items(map[string]any{"city": "Paris"}))
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionWaiting, res.Execution.Status, "the plugin's node waits as Telegram Ask does")

	require.Len(t, tg.asked, 1)
	key := tg.asked[0].Key

	s := waits.New(repo, ex, logging.Noop())
	require.ErrorIs(t, s.Answer(ctx, "alice", res.Execution.ID, "other", map[string]any{"option": 0}), domain.ErrNotFound, "another question's key")
	require.NoError(t, s.Answer(ctx, "alice", res.Execution.ID, key, map[string]any{"option": 0}))

	require.Eventually(t, func() bool { return len(answered.items()) == 1 }, patience, 10*time.Millisecond)
	assert.Equal(t, "Yes", answered.items()[0].JSON["label"])
	assert.Contains(t, tg.closed[101], "✅ <b>Yes</b>")
}

// No tasks here.
func (*memWaits) LeaseTask(context.Context, domain.UserID, ports.TaskLease, time.Time) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}

func (*memWaits) ReleaseTask(context.Context, domain.UserID, domain.ExecutionID) error {
	return domain.ErrNotFound
}

func (*memWaits) RenewLeases(context.Context, domain.UserID, string, time.Time) error { return nil }

func (*memWaits) TakeTask(context.Context, domain.UserID, domain.ExecutionID, string) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}
