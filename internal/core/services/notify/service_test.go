package notify_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"
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
	mail      *mailbox
	prefs     *prefs
	workflows *portsmocks.MockWorkflowRepository
	clock     *clock
}

// mailbox takes mailed notifications while on.
type mailbox struct {
	mu   sync.Mutex
	on   bool
	sent []ports.UserMail
	err  error
}

func (m *mailbox) On() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.on
}

func (m *mailbox) MailUser(_ context.Context, _ domain.UserID, mail ports.UserMail) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sent = append(m.sent, mail)

	return m.err
}

func (m *mailbox) mails() []ports.UserMail {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]ports.UserMail(nil), m.sent...)
}

func newService(t *testing.T, publicURL string) (*notify.Service, deps) {
	t.Helper()

	d := deps{
		messenger: &messenger{}, mail: &mailbox{on: true}, prefs: &prefs{changed: map[string]bool{}},
		workflows: portsmocks.NewMockWorkflowRepository(t), clock: &clock{now: now},
	}
	d.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("wf-1")).Return(spreads, nil).Maybe()

	s := notify.New(d.messenger, d.prefs, d.workflows, publicURL, logging.Noop(),
		notify.WithClock(d.clock.Now), notify.WithMail(d.mail))

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

	on := map[string]bool{}
	groups := map[string]string{}

	for _, p := range got {
		on[p.Key], groups[p.Key] = p.Enabled, p.Group
	}

	assert.Equal(t, map[string]bool{
		"workflow_failed": true, "approval_waiting": true, "session_key_expiring": true, "live_trade_failed": true,
		"agent_spend_80": true, "agent_spend_cap": true,
		"live_order_filled": true, "live_order_refused": true, // D230
		"live_order_unfilled": false, "live_order_rests": false, "live_order_ended": false,
		"paper_order_filled": false, "paper_order_refused": false, "paper_order_unfilled": false, // D232
		"paper_order_rests": false, "paper_order_ended": false,
		"workflow_paused": true, // D319; plugin_crashed only an admin sees (D320)
	}, on, "what is on by default")

	admin := auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}}
	all, err := s.Prefs(ctx, admin)
	require.NoError(t, err)
	assert.Len(t, all, len(got)+1, "an admin sees plugin_crashed too")

	_, err = s.SetPref(ctx, alice, "plugin_crashed", false)
	require.ErrorIs(t, err, domain.ErrNotFound, "none of a user's")
	assert.Equal(t, "workflow_failed", got[0].Key, "in Settings' order")
	assert.Equal(t, "Live orders", groups["live_trade_failed"])
	assert.Equal(t, "Paper orders", groups["paper_order_ended"])

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
			require.NoError(t, s.RemindQuestion(ctx, "alice", "wf-1", 12, left))
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

		require.NoError(t, s.RemindQuestion(ctx, "alice", "wf-1", 12, time.Hour))
		assert.Zero(t, d.messenger.count())
	})

	t.Run("nowhere to send it", func(t *testing.T) {
		s, d := newService(t, "")
		d.messenger.fail(domain.ErrTelegramNotLinked)

		require.ErrorIs(t, s.RemindQuestion(ctx, "alice", "wf-1", 12, time.Hour), domain.ErrTelegramNotLinked)
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

func TestEmailPrefs(t *testing.T) {
	ctx := context.Background()
	s, d := newService(t, "")

	got, err := s.Prefs(ctx, alice)
	require.NoError(t, err)

	for _, p := range got {
		assert.Equal(t, p.Default, p.Enabled, "%s on Telegram as its kind has it", p.Key)
		assert.Equal(t, p.EmailDefault, p.Email, "%s by email as its kind has it: off until chosen (D170), but a paused workflow (D319)", p.Key)
	}

	pref, err := s.SetEmailPref(ctx, alice, "workflow_failed", true)
	require.NoError(t, err)
	assert.True(t, pref.Email)
	assert.True(t, pref.Enabled, "Telegram as it was")
	assert.Equal(t, map[string]bool{"workflow_failed.email": true}, d.prefs.changed)

	pref, err = s.SetPref(ctx, alice, "workflow_failed", false)
	require.NoError(t, err)
	assert.False(t, pref.Enabled)
	assert.True(t, pref.Email, "the other channel as it was")

	_, err = s.SetEmailPref(ctx, alice, "nope", true)
	require.ErrorIs(t, err, domain.ErrNotFound)

	assert.True(t, s.MailOn())
	d.mail.on = false
	assert.False(t, s.MailOn())
}

func TestFailuresByEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("by email alone", func(t *testing.T) {
		s, d := newService(t, "https://poly.example")
		_, err := s.SetPref(ctx, alice, "workflow_failed", false)
		require.NoError(t, err)
		_, err = s.SetEmailPref(ctx, alice, "workflow_failed", true)
		require.NoError(t, err)

		s.ExecutionFinished("alice", failed)
		require.Eventually(t, func() bool { return len(d.mail.mails()) == 1 }, time.Second, time.Millisecond)

		m := d.mail.mails()[0]
		assert.Equal(t, "Spreads <tight> failed", m.Subject)
		assert.Equal(t, "Spreads <tight> failed.", m.Paragraphs[0])
		assert.Equal(t, `The error: node "hook": webhook to discord.com answered 404`, m.Paragraphs[1])
		assert.Contains(t, m.Paragraphs[2], "“A workflow fails” is on by email in Settings → Notifications")
		assert.Contains(t, m.Paragraphs[2], "https://poly.example/settings")
		assert.Equal(t, "https://poly.example/workflows/wf-1", m.LinkURL)
		assert.Zero(t, d.messenger.count(), "not on Telegram")
	})

	t.Run("both, under one cooldown", func(t *testing.T) {
		s, d := newService(t, "")
		_, err := s.SetEmailPref(ctx, alice, "workflow_failed", true)
		require.NoError(t, err)

		s.ExecutionFinished("alice", failed)
		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)

		assert.Equal(t, 1, d.messenger.count())
		assert.Len(t, d.mail.mails(), 1)
	})

	t.Run("not while mail is off", func(t *testing.T) {
		s, d := newService(t, "")
		d.mail.on = false
		_, err := s.SetEmailPref(ctx, alice, "workflow_failed", true)
		require.NoError(t, err)

		s.ExecutionFinished("alice", failed)
		settle(t, d, 1)

		assert.Empty(t, d.mail.mails())
	})
}

func TestRemindersByEmail(t *testing.T) {
	ctx := context.Background()

	t.Run("a question, pointing to Telegram (D185)", func(t *testing.T) {
		s, d := newService(t, "https://poly.example")
		_, err := s.SetEmailPref(ctx, alice, "approval_waiting", true)
		require.NoError(t, err)

		require.NoError(t, s.RemindQuestion(ctx, "alice", "wf-1", 12, 2*time.Hour))

		mails := d.mail.mails()
		require.Len(t, mails, 1)
		assert.Equal(t, "A question of Spreads <tight> waits for your answer", mails[0].Subject)
		assert.Equal(t, "A question of Spreads <tight> is still waiting for your answer in Telegram, for about 2 hours more.", mails[0].Paragraphs[0])
		assert.Equal(t, 1, d.messenger.count(), "and the reply in the chat")
	})

	t.Run("a session key, mailed though Telegram failed", func(t *testing.T) {
		s, d := newService(t, "")
		_, err := s.SetEmailPref(ctx, alice, "session_key_expiring", true)
		require.NoError(t, err)
		d.messenger.fail(errors.New("telegram: 502"))

		w := domain.Wallet{ID: "w-1", Label: "Main"}
		require.NoError(t, s.RemindSessionKey(ctx, "alice", w, now.Add(14*24*time.Hour)), "told by mail: not again")

		mails := d.mail.mails()
		require.Len(t, mails, 1)
		assert.Equal(t, "The session key of Main expires in about 14 days", mails[0].Subject)
	})

	t.Run("Telegram failing, nothing mailed: tried again", func(t *testing.T) {
		s, d := newService(t, "")
		d.messenger.fail(errors.New("telegram: 502"))

		require.Error(t, s.RemindSessionKey(ctx, "alice", domain.Wallet{ID: "w-1"}, now.Add(time.Hour)))
		assert.Empty(t, d.mail.mails())
	})
}

func TestLiveTradeFailed(t *testing.T) {
	ctx := context.Background()
	d := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }

	failedBuy := domain.PlacedOrder{
		OrderRequest: domain.OrderRequest{WorkflowID: "wf-1", OwnerID: "alice", Mode: domain.ModeLive, Side: domain.SideBuy},
		Status:       domain.OrderFailed,
		Fills:        []domain.OrderFill{{Price: d("0.5"), Shares: d("10"), Dollars: d("5"), Status: domain.FillFailed}},
	}

	t.Run("on Telegram, with a link to the workflow", func(t *testing.T) {
		s, deps := newService(t, "https://poly.example/")

		require.NoError(t, s.LiveTradeFailed(ctx, "alice", failedBuy))

		require.Len(t, deps.messenger.sent, 1)
		msg := deps.messenger.sent[0]
		assert.Equal(t, "⚠️ A live buy of <b>Spreads &lt;tight&gt;</b>'s order matched on Polymarket. Its trade failed on chain, "+
			"so the shares never changed hands; the workflow's books undo it.", msg.HTML)
		assert.Equal(t, [][]ports.TelegramButton{{{Text: "Open the workflow", URL: "https://poly.example/workflows/wf-1"}}}, msg.Buttons)
	})

	t.Run("part of it, by mail too when chosen", func(t *testing.T) {
		s, deps := newService(t, "https://poly.example")
		_, err := s.SetEmailPref(ctx, alice, "live_trade_failed", true)
		require.NoError(t, err)

		partly := failedBuy
		partly.Status = domain.OrderPartial
		partly.Fills = append(slices.Clone(failedBuy.Fills), domain.OrderFill{Price: d("0.5"), Shares: d("4"), Dollars: d("2"), Status: domain.FillConfirmed})

		require.NoError(t, s.LiveTradeFailed(ctx, "alice", partly))

		require.Len(t, deps.mail.sent, 1)
		assert.Equal(t, "A live trade of Spreads <tight> failed", deps.mail.sent[0].Subject)
		assert.Contains(t, deps.mail.sent[0].Paragraphs[0], "but part of its trade failed on chain; the books undo that part and keep 4 shares for $2.00.")
		require.Len(t, deps.messenger.sent, 1)
	})

	t.Run("not when switched off", func(t *testing.T) {
		s, deps := newService(t, "")
		_, err := s.SetPref(ctx, alice, "live_trade_failed", false)
		require.NoError(t, err)

		require.NoError(t, s.LiveTradeFailed(ctx, "alice", failedBuy))
		assert.Zero(t, deps.messenger.count())
	})

	t.Run("a deleted workflow", func(t *testing.T) {
		s, deps := newService(t, "")
		deps.workflows.EXPECT().Get(mock.Anything, domain.UserID("alice"), domain.WorkflowID("gone")).Return(domain.Workflow{}, domain.ErrNotFound)

		gone := failedBuy
		gone.WorkflowID = "gone"

		require.NoError(t, s.LiveTradeFailed(ctx, "alice", gone))
		require.Len(t, deps.messenger.sent, 1)
		assert.Contains(t, deps.messenger.sent[0].HTML, "A live buy of <b>a deleted workflow</b>'s order")
	})
}
