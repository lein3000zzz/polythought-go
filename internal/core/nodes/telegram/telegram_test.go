package telegram_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const owner = domain.UserID("alice")

// messenger records messages, failing with err when set.
type messenger struct {
	sent []ports.UserMessage
	err  error
}

func (m *messenger) MessageUser(_ context.Context, user domain.UserID, msg ports.UserMessage) error {
	if user != owner {
		return errors.New("wrong user")
	}

	m.sent = append(m.sent, msg)

	return m.err
}

func items(questions ...string) engine.Ports {
	in := engine.Ports{}
	for _, q := range questions {
		in.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{"question": q}})
	}

	return in
}

func run(t *testing.T, m *messenger, params map[string]any, in engine.Ports) (engine.Ports, error) {
	t.Helper()

	node := telegram.NewSend(m)
	rc := engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(node.Type(), params)}

	return node.Run(context.Background(), rc, in)
}

func TestSend(t *testing.T) {
	t.Run("one message for all, values escaped", func(t *testing.T) {
		m := &messenger{}

		out, err := run(t, m, map[string]any{"message": "{{question}} <b>?</b>"}, items("A & B", "<i>C</i>"))
		require.NoError(t, err)
		assert.Len(t, out[engine.DefaultPort], 2)

		require.Len(t, m.sent, 1)
		assert.Equal(t, "A &amp; B &lt;b&gt;?&lt;/b&gt;\n&lt;i&gt;C&lt;/i&gt; &lt;b&gt;?&lt;/b&gt;", m.sent[0].HTML, "text is text")
		assert.False(t, m.sent[0].Silent)
	})

	t.Run("html keeps the template's tags, not the items'", func(t *testing.T) {
		m := &messenger{}

		_, err := run(t, m, map[string]any{"message": "<b>{{question}}</b>", "format": "html", "silent": true}, items("<i>C</i>"))
		require.NoError(t, err)
		assert.Equal(t, "<b>&lt;i&gt;C&lt;/i&gt;</b>", m.sent[0].HTML)
		assert.True(t, m.sent[0].Silent)
	})

	t.Run("a message per item, twenty at most", func(t *testing.T) {
		m := &messenger{}
		questions := make([]string, 22)

		for i := range questions {
			questions[i] = "Q"
		}

		out, err := run(t, m, map[string]any{"message": "{{question}}", "send": "each"}, items(questions...))
		require.NoError(t, err)
		assert.Len(t, m.sent, 20)
		assert.Len(t, out[engine.DefaultPort], 20)
		require.Len(t, out[engine.ErrorPort], 2)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "at most 20 messages a run")
	})

	t.Run("long text is cut, long HTML refused", func(t *testing.T) {
		m := &messenger{}
		long := strings.Repeat("x", 5000)

		_, err := run(t, m, map[string]any{"message": "{{question}}"}, items(long))
		require.NoError(t, err)
		assert.Equal(t, 4096, len([]rune(m.sent[0].HTML)))
		assert.True(t, strings.HasSuffix(m.sent[0].HTML, "…"))

		out, err := run(t, m, map[string]any{"message": "{{question}}", "format": "html"}, items(long))
		require.NoError(t, err)
		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "longer than the 4096 characters")
	})

	t.Run("a failed message fails its items", func(t *testing.T) {
		m := &messenger{err: errors.New("telegram: Bad Request: can't parse entities")}

		out, err := run(t, m, map[string]any{"message": "<b", "format": "html"}, items("A", "B"))
		require.NoError(t, err)
		assert.Len(t, out[engine.ErrorPort], 2)
	})

	t.Run("no linked chat fails the node", func(t *testing.T) {
		m := &messenger{err: domain.ErrTelegramNotLinked}

		_, err := run(t, m, nil, items("A"))
		require.ErrorIs(t, err, domain.ErrTelegramNotLinked)
		assert.ErrorContains(t, err, "Settings → Telegram")
	})

	t.Run("no bot fails the node", func(t *testing.T) {
		m := &messenger{err: domain.ErrTelegramUnavailable}

		_, err := run(t, m, nil, items("A"))
		require.ErrorIs(t, err, domain.ErrTelegramUnavailable)
	})

	t.Run("nothing to send", func(t *testing.T) {
		m := &messenger{}

		out, err := run(t, m, nil, engine.Ports{})
		require.NoError(t, err)
		assert.Empty(t, out)
		assert.Empty(t, m.sent)
	})
}

// inbox hands out the messages the test sends to the trigger's subscription.
type inbox struct {
	mu    sync.Mutex
	owner domain.UserID
	match func(string) bool
	ch    chan ports.TelegramInboxMessage
}

func (i *inbox) Subscribe(owner domain.UserID, match func(string) bool) (<-chan ports.TelegramInboxMessage, func()) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.owner, i.match = owner, match

	return i.ch, func() {}
}

func (i *inbox) matches(text string) bool {
	i.mu.Lock()
	defer i.mu.Unlock()

	return i.match != nil && i.match(text)
}

func TestTrigger(t *testing.T) {
	ib := &inbox{ch: make(chan ports.TelegramInboxMessage, 1)}
	trigger := telegram.NewTrigger(ib)

	ctx, cancel := context.WithCancel(context.Background())
	emitted := make(chan engine.Ports, 1)
	done := make(chan error)

	go func() {
		done <- trigger.Start(ctx, engine.TriggerContext{OwnerID: owner, Params: engine.Params{"startsWith": " /Report "}},
			func(_ context.Context, out engine.Ports) error {
				emitted <- out
				return nil
			})
	}()

	require.Eventually(t, func() bool { return ib.matches("/report") }, time.Second, time.Millisecond)
	assert.Equal(t, owner, ib.owner)
	assert.True(t, ib.matches("/REPORT weekly"), "any letter case")
	assert.False(t, ib.matches("please /report"))
	assert.False(t, ib.matches("/rep"))

	at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ib.ch <- ports.TelegramInboxMessage{Text: "/report  weekly ", Username: "alice_tg", FirstName: "Alice", At: at}

	out := <-emitted
	assert.Equal(t, map[string]any{
		"text": "/report  weekly ", "rest": "weekly", "username": "alice_tg", "firstName": "Alice", "receivedAt": at,
	}, out[engine.DefaultPort][0].JSON)
	require.NoError(t, shape.Check(trigger.Type(), out), "items as declared")

	cancel()
	require.NoError(t, <-done)

	t.Run("without a prefix, every message", func(t *testing.T) {
		assert.Equal(t, "hello there", telegram.Output(ports.TelegramInboxMessage{Text: " hello there"}, "")[engine.DefaultPort][0].JSON["rest"])
	})
}
