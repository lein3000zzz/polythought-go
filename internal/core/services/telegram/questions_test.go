package telegram_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// fakeAnswers records answers, and answers each wait once.
type fakeAnswers struct {
	mu    sync.Mutex
	got   []answer
	taken map[domain.ExecutionID]bool
	err   error
}

type answer struct {
	owner   domain.UserID
	id      domain.ExecutionID
	key     string
	payload map[string]any
}

func (f *fakeAnswers) Answer(_ context.Context, owner domain.UserID, id domain.ExecutionID, key string, payload map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}

	if f.taken[id] || owner != alice.ID || key != "k1" {
		return domain.ErrNotFound
	}

	f.taken[id] = true
	f.got = append(f.got, answer{owner, id, key, payload})

	return nil
}

const execID = domain.ExecutionID("0b9d6f2e-3c4a-4e8b-9a1d-5f6e7c8d9a0b")

func TestAskUser(t *testing.T) {
	ctx := context.Background()
	q := ports.Question{HTML: "Buy <b>YES</b>?", Options: []string{"Yes", "No"}, ExecutionID: execID, Key: "k1"}

	t.Run("a button per option, to the linked chat", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		id, err := s.AskUser(ctx, alice.ID, q)
		require.NoError(t, err)
		assert.Equal(t, 1, id)

		assert.Equal(t, ports.TelegramMessage{ChatID: 70, HTML: "Buy <b>YES</b>?", Buttons: [][]ports.TelegramButton{{
			{Text: "Yes", Data: "ask:" + string(execID) + ":k1:0"},
			{Text: "No", Data: "ask:" + string(execID) + ":k1:1"},
		}}}, d.bot.last(t))

		for _, b := range d.bot.last(t).Buttons[0] {
			assert.LessOrEqual(t, len(b.Data), 64, "Telegram's limit")
		}
	})

	t.Run("short options share rows, long ones get their own", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		long := q
		long.Options = []string{"Buy", "Sell", "Hold", "Close", "Close everything and stop", "Later"}
		_, err := s.AskUser(ctx, alice.ID, long)
		require.NoError(t, err)

		var rows [][]string
		for _, row := range d.bot.last(t).Buttons {
			var texts []string
			for _, b := range row {
				texts = append(texts, b.Text)
			}

			rows = append(rows, texts)
		}

		assert.Equal(t, [][]string{{"Buy", "Sell", "Hold"}, {"Close"}, {"Close everything and stop"}, {"Later"}}, rows)
	})

	t.Run("not linked, or a key that cannot go in a button", func(t *testing.T) {
		s, d := newService(t)

		_, err := s.AskUser(ctx, alice.ID, q)
		require.ErrorIs(t, err, domain.ErrTelegramNotLinked)

		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		bad := q
		bad.Key = "a:b"
		_, err = s.AskUser(ctx, alice.ID, bad)
		require.Error(t, err)

		bad.Key, bad.ExecutionID = "k1", domain.ExecutionID(strings.Repeat("x", 60))
		_, err = s.AskUser(ctx, alice.ID, bad)
		require.ErrorContains(t, err, "more than 64")
		assert.Empty(t, d.bot.sent)
	})

	t.Run("closing it", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		require.NoError(t, s.CloseQuestion(ctx, alice.ID, 12, "Buy?\n\n✅ Yes"))
		assert.Equal(t, []ports.TelegramMessage{{ChatID: 70, HTML: "Buy?\n\n✅ Yes"}}, d.bot.edits)
		assert.Equal(t, []int{12}, d.bot.edited)
	})
}

func TestAnsweringQuestions(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*fakeAnswers, deps, func(ports.TelegramUpdate)) {
		t.Helper()

		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		answers := &fakeAnswers{taken: map[domain.ExecutionID]bool{}}
		s.UseAnswers(answers)

		return answers, d, func(u ports.TelegramUpdate) { s.HandleUpdate(ctx, u) }
	}

	t.Run("a press answers the owner's execution", func(t *testing.T) {
		answers, d, handle := setup(t)

		handle(press("ask:" + string(execID) + ":k1:1"))

		assert.Equal(t, []answer{{alice.ID, execID, "k1", map[string]any{"option": 1}}}, answers.got)
		assert.Equal(t, []string{""}, d.bot.answers)
		assert.Empty(t, d.bot.edits, "the question's node says what was picked")
	})

	t.Run("a closed question loses its buttons", func(t *testing.T) {
		_, d, handle := setup(t)

		for _, data := range []string{
			"ask:" + string(execID) + ":k0:1", // an earlier question's
			"ask:" + string(execID) + ":k1",
			"ask:" + string(execID) + ":k1:-1",
			"ask:" + string(execID) + ":k1:x",
		} {
			handle(press(data))
		}

		require.Len(t, d.bot.answers, 4)

		for i := range d.bot.answers {
			assert.Contains(t, d.bot.answers[i], "This question is closed")
			assert.Equal(t, ports.TelegramMessage{ChatID: 70}, d.bot.edits[i], "only the buttons go")
			assert.Equal(t, 9, d.bot.edited[i], "the message pressed")
		}
	})

	t.Run("answered once", func(t *testing.T) {
		answers, d, handle := setup(t)

		handle(press("ask:" + string(execID) + ":k1:0"))
		handle(press("ask:" + string(execID) + ":k1:1"))

		assert.Len(t, answers.got, 1)
		assert.Contains(t, d.bot.answers[1], "This question is closed")
	})

	t.Run("something going wrong", func(t *testing.T) {
		answers, d, handle := setup(t)
		answers.err = errors.New("db down")

		handle(press("ask:" + string(execID) + ":k1:0"))

		assert.Equal(t, []string{"Something went wrong on our side. Try again in a minute."}, d.bot.answers)
		assert.Empty(t, d.bot.edits)
	})

	t.Run("someone not linked", func(t *testing.T) {
		answers, d, handle := setup(t)

		u := press("ask:" + string(execID) + ":k1:0")
		u.Callback.From = ports.TelegramUser{ID: 99}
		handle(u)

		assert.Empty(t, answers.got)
		assert.Equal(t, []string{"This chat is not linked to Polythought."}, d.bot.answers)
	})
}
