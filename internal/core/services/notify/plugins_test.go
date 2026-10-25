package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

// admins are the instance's: two.
type admins struct{}

func (admins) Admins(context.Context) ([]domain.User, error) {
	return []domain.User{{ID: "root", Role: domain.RoleAdmin}, {ID: "ops", Role: domain.RoleAdmin}}, nil
}

func TestPluginNotices(t *testing.T) {
	d := deps{messenger: &messenger{}, mail: &mailbox{on: true}, prefs: &prefs{changed: map[string]bool{}}}

	s := notify.New(d.messenger, d.prefs, portsmocks.NewMockWorkflowRepository(t), "https://poly.example", logging.Noop(),
		notify.WithMail(d.mail), notify.WithAdmins(admins{}))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	go s.Run(ctx)

	t.Run("an owner hears why their workflow was paused, on Telegram and by mail (D319)", func(t *testing.T) {
		s.WorkflowPaused("alice", domain.Workflow{ID: "wf-1", Name: "Rain"}, "the plugin weather is disabled")
		settle(t, d, 1)

		assert.Equal(t, "⏸ Your workflow “Rain” was paused: the plugin weather is disabled. Turn it on again once it can run.", d.messenger.sent[0].HTML)
		assert.Equal(t, "https://poly.example/workflows/wf-1", d.messenger.sent[0].Buttons[0][0].URL)

		require.Eventually(t, func() bool { return len(d.mail.mails()) == 1 }, time.Second, time.Millisecond)
		assert.Equal(t, "Your workflow “Rain” was paused", d.mail.mails()[0].Subject, "by mail, unless they turned it off")
	})

	t.Run("every admin hears of a plugin stopped as crashed (D320)", func(t *testing.T) {
		s.PluginCrashed("weather", "it crashed 5 times in 10 minutes: exit status 3")
		settle(t, d, 3)

		assert.Contains(t, d.messenger.sent[1].HTML, "The plugin weather is stopped: it crashed 5 times in 10 minutes: exit status 3.")
		assert.Equal(t, "https://poly.example/admin/plugins", d.messenger.sent[2].Buttons[0][0].URL)
	})

	t.Run("every admin hears of a request to join, its note as plain text (D394)", func(t *testing.T) {
		s.JoinRequested("new@example.com", "I trade <b>weather</b> markets.")
		settle(t, d, 5)

		assert.Equal(t, "🙋 new@example.com asks to join. They say: “I trade &lt;b&gt;weather&lt;/b&gt; markets.”", d.messenger.sent[3].HTML)
		assert.Equal(t, "https://poly.example/admin/invites", d.messenger.sent[4].Buttons[0][0].URL)
	})

	t.Run("every admin hears of a newer release (D424)", func(t *testing.T) {
		s.UpdateAvailable("0.0.2", "0.0.1")
		settle(t, d, 7)

		assert.Equal(t, "🆕 Polythought 0.0.2 is out; this server runs 0.0.1. See what changed, and update, on the Config page.", d.messenger.sent[5].HTML)
		assert.Equal(t, "https://poly.example/admin/config", d.messenger.sent[6].Buttons[0][0].URL)
	})
}
