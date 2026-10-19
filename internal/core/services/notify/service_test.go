package notify_test

import (
	"context"
	"errors"
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
	require.Len(t, got, 3)
	assert.Equal(t, "workflow_failed", got[0].Key)
	assert.Equal(t, "approval_waiting", got[1].Key)
	assert.Equal(t, "session_key_expiring", got[2].Key)

	for _, p := range got {
		assert.True(t, p.Enabled, "%s on by default", p.Key)
	}

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

func TestRemindQuestion(t *testing.T) {
	ctx := context.Background()

	t.Run("replies to the question", func(t *testing.T) {
		s, d := newService(t, "")

		for _, left := range []time.Duration{15 * time.Minute, 20 * time.Second, 6 * time.Hour, 42 * time.Hour, 54 * time.Hour} {
			require.NoError(t, s.RemindQuestion(ctx, "alice", 12, left))
		}

		texts := make([]string, len(d.messenger.sent))
		for i, m := range d.messenger.sent {
			assert.Equal(t, 12, m.ReplyTo)
			assert.False(t, m.Silent)
			texts[i] = m.HTML
		}

		assert.Equal(t, []string{
			"⏳ This question is still waiting for your answer, for about 15 minutes more.",
			"⏳ This question is still waiting for your answer, for about 1 minute more.",
			"⏳ This question is still waiting for your answer, for about 6 hours more.",
			"⏳ This question is still waiting for your answer, for about 42 hours more.",
			"⏳ This question is still waiting for your answer, for about 2 days more.",
		}, texts)
	})

	t.Run("not when switched off", func(t *testing.T) {
		s, d := newService(t, "")
		_, err := s.SetPref(ctx, alice, "approval_waiting", false)
		require.NoError(t, err)

		require.NoError(t, s.RemindQuestion(ctx, "alice", 12, time.Hour))
		assert.Zero(t, d.messenger.count())
	})

	t.Run("nowhere to send it", func(t *testing.T) {
		s, d := newService(t, "")
		d.messenger.fail(domain.ErrTelegramNotLinked)

		require.ErrorIs(t, s.RemindQuestion(ctx, "alice", 12, time.Hour), domain.ErrTelegramNotLinked)
	})
}

func TestRemindSessionKey(t *testing.T) {
	ctx := context.Background()
	w := domain.Wallet{ID: "w-1", UserID: "alice", Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", Label: "Main <1>"}

	t.Run("how long it has left, with a link", func(t *testing.T) {
		s, d := newService(t, "https://poly.example/")

		require.NoError(t, s.RemindSessionKey(ctx, "alice", w, now.Add(14*24*time.Hour)))

		require.Len(t, d.messenger.sent, 1)
		msg := d.messenger.sent[0]
		assert.Equal(t, "🔑 The session key of <b>Main &lt;1&gt;</b> expires in about 14 days, on 28 October 2026 (UTC). "+
			"Authorise a new one on the Wallets page before then, so the wallet keeps trading.", msg.HTML)
		assert.Equal(t, [][]ports.TelegramButton{{{Text: "Open wallets", URL: "https://poly.example/wallets"}}}, msg.Buttons)
	})

	t.Run("expired already; a wallet without a label by its address", func(t *testing.T) {
		s, d := newService(t, "")
		unnamed := w
		unnamed.Label = ""

		require.NoError(t, s.RemindSessionKey(ctx, "alice", unnamed, now.Add(-time.Hour)))

		require.Len(t, d.messenger.sent, 1)
		assert.Equal(t, "🔑 The session key of <b>0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed</b> has expired. "+
			"Authorise a new one on the Wallets page, so the wallet can trade again.", d.messenger.sent[0].HTML)
		assert.Nil(t, d.messenger.sent[0].Buttons)
	})

	t.Run("not when switched off", func(t *testing.T) {
		s, d := newService(t, "")
		_, err := s.SetPref(ctx, alice, "session_key_expiring", false)
		require.NoError(t, err)

		require.NoError(t, s.RemindSessionKey(ctx, "alice", w, now.Add(time.Hour)))
		assert.Zero(t, d.messenger.count())
	})

	t.Run("nowhere to send it is no error", func(t *testing.T) {
		for _, err := range []error{domain.ErrTelegramNotLinked, domain.ErrTelegramUnavailable} {
			s, d := newService(t, "")
			d.messenger.fail(err)

			require.NoError(t, s.RemindSessionKey(ctx, "alice", w, now.Add(time.Hour)))
		}
	})

	t.Run("a failure to send is, to try again", func(t *testing.T) {
		s, d := newService(t, "")
		d.messenger.fail(errors.New("telegram: 502"))

		require.Error(t, s.RemindSessionKey(ctx, "alice", w, now.Add(time.Hour)))
	})
}
