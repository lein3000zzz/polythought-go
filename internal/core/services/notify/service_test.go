package notify_test

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	now   = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "alice"}, SessionID: "s-1"}
)

// spreads runs on a schedule; byHand has only a manual trigger.
var (
	spreads = domain.Workflow{ID: "wf-1", OwnerID: "alice", Name: "Spreads <tight>", Nodes: []domain.NodeSpec{
		{ID: "every", Type: "trigger.schedule"}, {ID: "manual", Type: "trigger.manual"},
	}}
	failed = domain.Execution{WorkflowID: "wf-1", TriggeredBy: "every", Status: domain.ExecutionFailed, Error: `node "hook": webhook to discord.com answered 404`}
)

type messenger struct {
	mu   sync.Mutex
	sent []ports.UserMessage
	err  error
}

func (m *messenger) MessageUser(_ context.Context, _ domain.UserID, msg ports.UserMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sent = append(m.sent, msg)

	return m.err
}

func (m *messenger) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.err = err
}

func (m *messenger) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.sent)
}

type prefs struct {
	mu      sync.Mutex
	changed map[string]bool
}

func (p *prefs) Prefs(context.Context, domain.UserID) (map[string]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	return maps.Clone(p.changed), nil
}

func (p *prefs) SetPref(_ context.Context, _ domain.UserID, kind string, enabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.changed[kind] = enabled

	return nil
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = t
}

type deps struct {
	messenger *messenger
	prefs     *prefs
	workflows *portsmocks.MockWorkflowRepository
	clock     *clock
}

func newService(t *testing.T, publicURL string) (*notify.Service, deps) {
	t.Helper()

	d := deps{
		messenger: &messenger{}, prefs: &prefs{changed: map[string]bool{}},
		workflows: portsmocks.NewMockWorkflowRepository(t), clock: &clock{now: now},
	}
	d.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("wf-1")).Return(spreads, nil).Maybe()

	s := notify.New(d.messenger, d.prefs, d.workflows, publicURL, logging.Noop(), notify.WithClock(d.clock.Now))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go s.Run(ctx)

	return s, d
}

// settle waits until n messages were sent, and a little more to catch extra ones.
func settle(t *testing.T, d deps, n int) {
	t.Helper()

	require.Eventually(t, func() bool { return d.messenger.count() >= n }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	assert.Equal(t, n, d.messenger.count())
}

func TestWorkflowFailed(t *testing.T) {
	t.Run("tells the owner, with a link when the app is on https", func(t *testing.T) {
		s, d := newService(t, "https://poly.example/")

		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)

		msg := d.messenger.sent[0]
		assert.Equal(t, "⚠️ <b>Spreads &lt;tight&gt;</b> failed.\n<code>node &#34;hook&#34;: webhook to discord.com answered 404</code>", msg.HTML)
		assert.Equal(t, [][]ports.TelegramButton{{{Text: "Open workflow", URL: "https://poly.example/workflows/wf-1"}}}, msg.Buttons)
	})

	t.Run("no link button for a local address", func(t *testing.T) {
		s, d := newService(t, "http://localhost:3000")

		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)
		assert.Nil(t, d.messenger.sent[0].Buttons)
	})

	t.Run("once an hour per workflow, counting the rest", func(t *testing.T) {
		s, d := newService(t, "")

		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)

		d.clock.set(now.Add(30 * time.Minute))
		s.ExecutionFinished("alice", failed)
		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)

		d.clock.set(now.Add(61 * time.Minute))
		s.ExecutionFinished("alice", failed)
		settle(t, d, 2)
		assert.Contains(t, d.messenger.sent[1].HTML, "It failed 2 more times since the last message.")
	})

	t.Run("not for successes, runs by hand, or when switched off", func(t *testing.T) {
		s, d := newService(t, "")

		ok := failed
		ok.Status = domain.ExecutionSucceeded
		s.ExecutionFinished("alice", ok)

		byHand := failed
		byHand.TriggeredBy = "manual"
		s.ExecutionFinished("alice", byHand)

		settle(t, d, 0)

		require.NoError(t, d.prefs.SetPref(context.Background(), "alice", "workflow_failed", false))
		s.ExecutionFinished("alice", failed)
		settle(t, d, 0)
	})

	t.Run("a deleted workflow, or nowhere to send, is no trouble", func(t *testing.T) {
		s, d := newService(t, "")
		d.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("gone")).Return(domain.Workflow{}, domain.ErrNotFound)

		gone := failed
		gone.WorkflowID = "gone"
		s.ExecutionFinished("alice", gone)

		d.messenger.fail(domain.ErrTelegramNotLinked)
		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)
	})
}

func TestPrefs(t *testing.T) {
	ctx := context.Background()
	s, d := newService(t, "")

	got, err := s.Prefs(ctx, alice)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "workflow_failed", got[0].Key)
	assert.True(t, got[0].Enabled, "on by default")

	pref, err := s.SetPref(ctx, alice, "workflow_failed", false)
	require.NoError(t, err)
	assert.False(t, pref.Enabled)
	assert.Equal(t, map[string]bool{"workflow_failed": false}, d.prefs.changed)

	got, err = s.Prefs(ctx, alice)
	require.NoError(t, err)
	assert.False(t, got[0].Enabled)

	_, err = s.SetPref(ctx, alice, "gossip", true)
	require.ErrorIs(t, err, domain.ErrNotFound)
}
