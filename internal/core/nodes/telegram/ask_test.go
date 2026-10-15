package telegram_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var askedAt = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// asker records questions and how they were closed.
type asker struct {
	asked  []ports.Question
	closed map[int]string
	err    error
}

func (a *asker) AskUser(_ context.Context, user domain.UserID, q ports.Question) (int, error) {
	if user != owner {
		return 0, errors.New("wrong user")
	}

	if a.err != nil {
		return 0, a.err
	}

	a.asked = append(a.asked, q)

	return 12, nil
}

func (a *asker) CloseQuestion(_ context.Context, _ domain.UserID, id int, html string) error {
	if a.closed == nil {
		a.closed = map[int]string{}
	}

	a.closed[id] = html

	return nil
}

type reminder struct {
	left map[int]time.Duration
}

func (r *reminder) RemindQuestion(_ context.Context, _ domain.UserID, id int, left time.Duration) error {
	r.left[id] = left
	return errors.New("not linked any more") // must not matter
}

type askTest struct {
	asker    *asker
	reminder *reminder
	clock    *time.Time
	node     *telegram.Ask
	rc       engine.RunContext
}

func newAsk(params map[string]any, withReminder bool) askTest {
	clock := askedAt
	at := askTest{asker: &asker{}, reminder: &reminder{left: map[int]time.Duration{}}, clock: &clock}

	var r ports.QuestionReminder
	if withReminder {
		r = at.reminder
	}

	at.node = telegram.NewAsk(at.asker, r, func() time.Time { return *at.clock })
	at.rc = engine.RunContext{ExecutionID: "ex-1", OwnerID: owner, Params: engine.LiteralParams(at.node.Type(), params)}

	return at
}

// wait runs the node and returns how it waits, its state through JSON as the executor
// keeps it.
func (at askTest) wait(t *testing.T, in engine.Ports) *engine.Wait {
	t.Helper()

	out, err := at.node.Run(context.Background(), at.rc, in)
	require.Nil(t, out)

	var w *engine.Wait
	require.ErrorAs(t, err, &w)

	raw, err := json.Marshal(w.State)
	require.NoError(t, err)

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	require.NoError(t, dec.Decode(&w.State))

	return w
}

func TestAskType(t *testing.T) {
	typ := telegram.NewAsk(&asker{}, nil, time.Now).Type()

	assert.Equal(t, []string{"timeout", "Yes", "No"}, typ.OutputsOf(nil))
	assert.Equal(t, []string{"timeout", "Buy", "Skip"}, typ.OutputsOf(map[string]any{"buttons": []any{"Buy", "Skip"}}))

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterNode(telegram.NewAsk(&asker{}, nil, time.Now)))
}

func TestAsk(t *testing.T) {
	ctx := context.Background()

	t.Run("asks once for all items, then waits", func(t *testing.T) {
		at := newAsk(map[string]any{"message": "Buy {{question}}?"}, true)

		w := at.wait(t, items("A & B", "C"))

		require.Len(t, at.asker.asked, 1)
		q := at.asker.asked[0]
		assert.Equal(t, "Buy A &amp; B?\nBuy C?", q.HTML)
		assert.Equal(t, []string{"Yes", "No"}, q.Options)
		assert.Equal(t, domain.ExecutionID("ex-1"), q.ExecutionID)

		assert.Len(t, w.Key, 11, "8 random bytes")
		assert.Equal(t, w.Key, q.Key)
		assert.Equal(t, askedAt.Add(45*time.Minute), w.Until, "the reminder, with a quarter of the hour left")
	})

	t.Run("no reminder for short questions, or without reminders", func(t *testing.T) {
		at := newAsk(map[string]any{"timeout": "5m"}, true)
		assert.Equal(t, askedAt.Add(5*time.Minute), at.wait(t, items("A")).Until)

		at = newAsk(nil, false)
		assert.Equal(t, askedAt.Add(time.Hour), at.wait(t, items("A")).Until)
	})

	t.Run("each question a new key", func(t *testing.T) {
		at := newAsk(nil, false)

		assert.NotEqual(t, at.wait(t, items("A")).Key, at.wait(t, items("A")).Key)
	})

	t.Run("nothing to ask about", func(t *testing.T) {
		at := newAsk(nil, true)

		out, err := at.node.Run(ctx, at.rc, engine.Ports{})
		require.NoError(t, err)
		assert.Zero(t, out.Len())
		assert.Empty(t, at.asker.asked)
	})

	t.Run("nowhere to ask fails the node", func(t *testing.T) {
		at := newAsk(nil, true)
		at.asker.err = domain.ErrTelegramNotLinked

		_, err := at.node.Run(ctx, at.rc, items("A"))
		require.ErrorIs(t, err, domain.ErrTelegramNotLinked)
	})

	t.Run("reminds once, then waits on with the same key", func(t *testing.T) {
		at := newAsk(nil, true)
		w := at.wait(t, items("A"))

		*at.clock = w.Until
		out, err := at.node.Resume(ctx, at.rc, items("A"), engine.Resumption{TimedOut: true, State: w.State})
		require.Nil(t, out)

		var again *engine.Wait
		require.ErrorAs(t, err, &again)
		assert.Equal(t, map[int]time.Duration{12: 15 * time.Minute}, at.reminder.left)
		assert.Equal(t, askedAt.Add(time.Hour), again.Until)
		assert.Equal(t, w.Key, again.Key)
		assert.Equal(t, false, again.State["remind"])
		assert.Empty(t, at.asker.closed)
	})

	t.Run("an answer sends the items through its button", func(t *testing.T) {
		at := newAsk(map[string]any{"message": "{{question}}?", "buttons": []any{"Buy", "Skip"}}, true)
		w := at.wait(t, items("A", "B"))

		out, err := at.node.Resume(ctx, at.rc, items("A", "B"), engine.Resumption{Payload: map[string]any{"option": 1}, State: w.State})
		require.NoError(t, err)

		assert.Equal(t, []string{"Skip"}, usedPorts(out))
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, out["Skip"][1].Source)
		assert.Equal(t, map[int]string{12: "A?\nB?\n\n✅ <b>Skip</b>"}, at.asker.closed)
	})

	t.Run("no answer in time", func(t *testing.T) {
		at := newAsk(map[string]any{"message": "{{question}}?", "timeout": "5m"}, true)
		w := at.wait(t, items("A"))

		out, err := at.node.Resume(ctx, at.rc, items("A"), engine.Resumption{TimedOut: true, State: w.State})
		require.NoError(t, err)

		assert.Equal(t, []string{"timeout"}, usedPorts(out))
		assert.Equal(t, map[int]string{12: "A?\n\n⌛ <i>No answer in time.</i>"}, at.asker.closed)
	})

	t.Run("an answer for no button", func(t *testing.T) {
		at := newAsk(nil, true)
		w := at.wait(t, items("A"))

		_, err := at.node.Resume(ctx, at.rc, items("A"), engine.Resumption{Payload: map[string]any{"option": 2}, State: w.State})
		require.ErrorContains(t, err, "an answer for button 2, of 2")
	})
}

func usedPorts(out engine.Ports) []string {
	var names []string

	for name, items := range out {
		if len(items) > 0 {
			names = append(names, name)
		}
	}

	return names
}
