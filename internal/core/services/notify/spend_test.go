package notify_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func TestAgentSpendNotices(t *testing.T) {
	ctx := context.Background()
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

	sent := func(d deps, n int) []ports.UserMessage {
		t.Helper()

		require.Eventually(t, func() bool {
			d.messenger.mu.Lock()
			defer d.messenger.mu.Unlock()

			return len(d.messenger.sent) == n
		}, time.Second, 5*time.Millisecond)

		d.messenger.mu.Lock()
		defer d.messenger.mu.Unlock()

		return append([]ports.UserMessage(nil), d.messenger.sent...)
	}

	t.Run("80% of a cap, on Telegram, with a link to the spend", func(t *testing.T) {
		s, d := orderService(t, "https://poly.example")

		s.AgentSpendNoticed("alice", domain.SpendAlert{Notice: domain.NoticeWarned, What: "dollars", Cap: "$20.00", Used: "$16.10", Resets: november})

		msg := sent(d, 1)[0]
		assert.Equal(t, "📈 Your AI agent nodes have used $16.10 of $20.00 this month, 80% of their cap. It resets on 1 November, 00:00 UTC.", msg.HTML)
		assert.Equal(t, [][]ports.TelegramButton{{{Text: "See your spend", URL: "https://poly.example/settings"}}}, msg.Buttons)
	})

	t.Run("a cap reached, by mail too when chosen", func(t *testing.T) {
		s, d := orderService(t, "https://poly.example")
		_, err := s.SetEmailPref(ctx, alice, "agent_spend_cap", true)
		require.NoError(t, err)

		s.AgentSpendNoticed("alice", domain.SpendAlert{
			Notice: domain.NoticeCapped, What: "tokens", Cap: "20,000,000", Used: "20,000,000", Resets: november,
		})

		msg := sent(d, 1)[0]
		assert.Contains(t, msg.HTML, "⛔ Your AI agent nodes have used 20,000,000 of 20,000,000 tokens this month: their cap is reached")
		assert.Contains(t, msg.HTML, "until it resets on 1 November, 00:00 UTC, unless an admin raises it.")

		require.Eventually(t, func() bool { return len(d.mail.mails()) == 1 }, time.Second, 5*time.Millisecond)
		assert.Equal(t, "Your AI agents reached a monthly cap", d.mail.mails()[0].Subject)
	})

	t.Run("not when switched off", func(t *testing.T) {
		s, d := orderService(t, "")
		_, err := s.SetPref(ctx, alice, "agent_spend_80", false)
		require.NoError(t, err)

		s.AgentSpendNoticed("alice", domain.SpendAlert{Notice: domain.NoticeWarned, What: "tokens", Cap: "10", Used: "8", Resets: november})
		s.AgentSpendNoticed("alice", domain.SpendAlert{Notice: domain.NoticeCapped, What: "tokens", Cap: "10", Used: "10", Resets: november})

		msgs := sent(d, 1)
		assert.Contains(t, msgs[0].HTML, "their cap is reached", "only the one still on")
		assert.Empty(t, msgs[0].Buttons, "no link without an https address")
	})
}
