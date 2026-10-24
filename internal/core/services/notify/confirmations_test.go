package notify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
)

// asker is Telegram: what it asked, as message 12, and how each was closed.
type asker struct {
	mu     sync.Mutex
	asked  []string
	closed []string
}

func (a *asker) AskConfirmation(_ context.Context, _ domain.UserID, _ domain.ConfirmationID, html string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.asked = append(a.asked, html)

	return 12, nil
}

func (a *asker) CloseQuestion(_ context.Context, _ domain.UserID, id int, html string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id == 12 {
		a.closed = append(a.closed, html)
	}

	return nil
}

var asking = domain.Confirmation{
	ID: "c-1", OwnerID: "alice", Kind: domain.ConfirmRun, GrantID: "g-1", Agent: "Claude <Code>", What: `Run "Spread watch" by hand, which trades live`,
	Status: domain.ConfirmPending, ExpiresAt: time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC),
}

// An agent's live action waiting for the user comes on Telegram with its buttons, by mail
// only where the user chose it, and Telegram's message says how it was decided (D406).
func TestConfirmationAsked(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (*notify.Service, deps, *asker) {
		t.Helper()

		_, d := newService(t, "https://poly.example")
		a := &asker{}
		s := notify.New(d.messenger, d.prefs, d.workflows, "https://poly.example", logging.Noop(),
			notify.WithClock(d.clock.Now), notify.WithMail(d.mail), notify.WithConfirmAsker(a))

		return s, d, a
	}

	t.Run("on Telegram, not by mail, to start with", func(t *testing.T) {
		s, d, a := setup(t)

		id, err := s.ConfirmationAsked(ctx, asking)
		require.NoError(t, err)
		assert.Equal(t, 12, id)

		require.Len(t, a.asked, 1)
		assert.Equal(t, "🔐 <b>Claude &lt;Code&gt;</b> asks you to confirm:\nRun &#34;Spread watch&#34; by hand, which trades live.\n\n"+
			"It waits until 13:00 on 14 October (UTC).", a.asked[0])
		assert.Empty(t, d.mail.mails())
	})

	t.Run("by mail too, where the user chose it", func(t *testing.T) {
		s, d, _ := setup(t)
		_, err := s.SetEmailPref(ctx, alice, "confirmation_asked", true)
		require.NoError(t, err)

		_, err = s.ConfirmationAsked(ctx, asking)
		require.NoError(t, err)

		mails := d.mail.mails()
		require.Len(t, mails, 1)
		assert.Equal(t, "Claude <Code> asks you to confirm a live action", mails[0].Subject)
		assert.Equal(t, "https://poly.example/settings/connected#confirmations", mails[0].LinkURL)
	})

	t.Run("nowhere, switched off", func(t *testing.T) {
		s, _, a := setup(t)
		_, err := s.SetPref(ctx, alice, "confirmation_asked", false)
		require.NoError(t, err)

		id, err := s.ConfirmationAsked(ctx, asking)
		require.NoError(t, err)
		assert.Zero(t, id)
		assert.Empty(t, a.asked)
	})

	t.Run("closed, saying how", func(t *testing.T) {
		s, _, a := setup(t)

		for _, c := range []domain.Confirmation{
			{Status: domain.ConfirmConfirmed}, {Status: domain.ConfirmDone, Result: "execution ex-1, succeeded"},
			{Status: domain.ConfirmFailed, Result: "<boom>"}, {Status: domain.ConfirmRefused}, {Status: domain.ConfirmExpired},
			{Status: domain.ConfirmPending},
		} {
			full := asking
			full.Status, full.Result, full.Message = c.Status, c.Result, 12
			s.ConfirmationClosed(ctx, full)
		}

		s.ConfirmationClosed(ctx, asking) // no message went: nothing to close

		require.Len(t, a.closed, 5, "a pending one stays as it is")
		assert.Contains(t, a.closed[0], "\n\n✅ <b>Confirmed</b>: being carried out.")
		assert.Contains(t, a.closed[1], "\n\n✅ <b>Confirmed</b>, and done: execution ex-1, succeeded.")
		assert.Contains(t, a.closed[2], "\n\n⚠️ <b>Confirmed</b>, but it failed: &lt;boom&gt;")
		assert.Contains(t, a.closed[3], "\n\n✖️ <b>Refused</b>.")
		assert.Contains(t, a.closed[4], "\n\n⌛ <i>Not confirmed in time.</i>")
	})
}
