package telegram_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const confID = domain.ConfirmationID("0b9d6f2e-3c4a-4e8b-9a1d-5f6e7c8d9a0c")

// fakeConfirms decides each confirmation once, keeping who did and how.
type fakeConfirms struct {
	mu      sync.Mutex
	by      []auth.Principal
	how     []string
	decided bool
}

func (f *fakeConfirms) decide(p auth.Principal, id domain.ConfirmationID, how string) (domain.Confirmation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if id != confID || p.User.ID != alice.ID {
		return domain.Confirmation{}, domain.ErrNotFound
	}

	if f.decided {
		return domain.Confirmation{}, domain.ErrConflict
	}

	f.decided = true
	f.by, f.how = append(f.by, p), append(f.how, how)

	return domain.Confirmation{ID: id}, nil
}

func (f *fakeConfirms) Confirm(_ context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	return f.decide(p, id, "confirmed")
}

func (f *fakeConfirms) Refuse(_ context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	return f.decide(p, id, "refused")
}

// A live action waiting for its user comes to Telegram with a button to confirm and one to
// refuse (D406).
func TestConfirmations(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*fakeConfirms, deps, func(ports.TelegramUpdate)) {
		t.Helper()

		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		c := &fakeConfirms{}
		s.UseConfirmations(c)

		id, err := s.AskConfirmation(ctx, alice.ID, confID, "🔐 <b>Claude Code</b> asks you to confirm:")
		require.NoError(t, err)
		assert.Equal(t, 1, id)

		return c, d, func(u ports.TelegramUpdate) { s.HandleUpdate(ctx, u) }
	}

	t.Run("asked with its two buttons", func(t *testing.T) {
		_, d, _ := setup(t)

		assert.Equal(t, ports.TelegramMessage{ChatID: 70, HTML: "🔐 <b>Claude Code</b> asks you to confirm:", Buttons: [][]ports.TelegramButton{{
			{Text: "✅ Confirm", Data: "conf:" + string(confID) + ":y"}, {Text: "✖️ Refuse", Data: "conf:" + string(confID) + ":n"},
		}}}, d.bot.last(t))
		assert.LessOrEqual(t, len(d.bot.last(t).Buttons[0][0].Data), 64, "Telegram's limit")
	})

	t.Run("confirmed by its user, in their chat", func(t *testing.T) {
		c, d, handle := setup(t)

		handle(press("conf:" + string(confID) + ":y"))

		require.Len(t, c.by, 1)
		assert.True(t, c.by[0].Itself(), "as the user, never a token or an agent")
		assert.Equal(t, []string{"confirmed"}, c.how)
		assert.Equal(t, []string{"Confirmed: it is being carried out"}, d.bot.answers)

		handle(press("conf:" + string(confID) + ":n"))
		assert.Contains(t, d.bot.answers[1], "This is closed", "decided once")
	})

	t.Run("refused", func(t *testing.T) {
		c, d, handle := setup(t)

		handle(press("conf:" + string(confID) + ":n"))

		assert.Equal(t, []string{"refused"}, c.how)
		assert.Equal(t, []string{"Refused"}, d.bot.answers)
	})

	t.Run("buttons that lead nowhere", func(t *testing.T) {
		c, d, handle := setup(t)

		for _, data := range []string{"conf:" + string(confID), "conf:" + string(confID) + ":maybe", "conf::y", "conf:other:y"} {
			handle(press(data))
		}

		assert.Empty(t, c.how)
		require.Len(t, d.bot.answers, 4)

		for _, a := range d.bot.answers {
			assert.Contains(t, a, "This is closed")
		}
	})
}
