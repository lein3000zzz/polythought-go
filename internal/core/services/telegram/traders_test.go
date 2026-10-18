package telegram_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

const (
	aliceWallet = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	bobWallet   = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
)

// copyFlow follows Alice, on, and Bob, off; Alice's second row is not counted.
func copyFlow(id domain.WorkflowID) domain.Workflow {
	return domain.Workflow{
		ID: id, Name: "Copy whales", Version: 1, Active: true,
		Nodes: []domain.NodeSpec{
			{ID: "watch", Type: triggers.WalletActivityType, Params: map[string]any{
				"traders": []any{
					map[string]any{"label": "Alice", "address": aliceWallet, "on": true, "scale": 2.0},
					map[string]any{"label": "", "address": bobWallet, "on": false},
					map[string]any{"label": "Alice again", "address": "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "on": true},
				},
				"sides": "both",
			}},
			{ID: "copy", Type: "trading.placeOrder"},
		},
		Connections: []domain.Connection{{From: "watch", To: "copy"}},
	}
}

func tradersMenu(t *testing.T) (*telegram.Service, menuDeps) {
	t.Helper()

	s, m := newMenu(t)
	m.workflows.items = append(m.workflows.items, copyFlow("wf-3"))

	num := decimal.RequireFromString
	m.trading["wf-3"] = trading.Summary{
		Account: trading.AccountSummary{Open: true, Worth: num("1012.4"), Profit: num("12.4")},
		Traders: []trading.TraderSummary{
			{Trader: aliceWallet, Label: "Alice", Copies: 5, Volume: num("120"), Realized: num("8.1"), Unrealized: num("2.3"), Wins: 3, Closed: 5},
			{Copies: 1, Volume: num("10"), Realized: num("-1.2")},
		},
	}

	return s, m
}

// aliceOn is Alice's On, as the workflow has it now.
func aliceOn(m menuDeps) any {
	return m.workflows.items[2].Nodes[0].Params["traders"].([]any)[0].(map[string]any)["on"]
}

func TestMenuTraders(t *testing.T) {
	ctx := context.Background()

	t.Run("the keyboard opens the workflows that follow traders", func(t *testing.T) {
		s, m := tradersMenu(t)

		s.HandleUpdate(ctx, message("👥 Traders"))
		list := m.bot.last(t)
		assert.Contains(t, list.HTML, "<b>Traders</b>")
		assert.Equal(t, []string{"● Copy whales · 1 of 2 on=tw:wf-3"}, buttons(list), "a wallet once")

		s.HandleUpdate(ctx, press("tf:"))
		assert.Equal(t, []string{"● Copy whales · 1 of 2 on=tw:wf-3"}, buttons(m.bot.lastEdit(t)))
	})

	t.Run("none yet", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, message("traders"))
		assert.Contains(t, m.bot.last(t).HTML, "No workflow follows traders yet")
	})

	t.Run("a workflow's traders, with what copying them came to", func(t *testing.T) {
		s, m := tradersMenu(t)

		s.HandleUpdate(ctx, press("tw:wf-3"))
		screen := m.bot.lastEdit(t)

		assert.Equal(t, "<b>Copy whales</b> · paper, worth $1012.40 (+$12.40)\n"+
			"● Alice  +$8.10 realized, +$2.30 open, 3 of 5\n"+
			"○ 0xfB69…d359  no copies yet", screen.HTML, "copied from no one is not a trader")
		assert.Equal(t, []string{
			"● Alice=tt:wf-3:0:0x5aaeb605", "○ 0xfB69…d359=tt:wf-3:1:0xfb691609",
			"↻ Refresh=tw:wf-3", "« Traders=tf:",
		}, buttons(screen))
	})

	t.Run("a trader, switched off and on again, a new version each time (D108)", func(t *testing.T) {
		s, m := tradersMenu(t)
		before := m.workflows.items[2].Nodes

		s.HandleUpdate(ctx, press("tt:wf-3:0:0x5aaeb605"))
		screen := m.bot.lastEdit(t)
		assert.Equal(t, "<b>Alice</b> (0x5aAe…eAed) in Copy whales: on\n"+
			"5 copies, $120.00 traded\n+$8.10 realized, +$2.30 open, 3 of 5 won", screen.HTML)
		assert.Equal(t, []string{"⏻ Switch off=ts:wf-3:0:0x5aaeb605:0", "« Copy whales=tw:wf-3"}, buttons(screen))

		s.HandleUpdate(ctx, press("ts:wf-3:0:0x5aaeb605:0"))
		screen = m.bot.lastEdit(t)
		assert.Contains(t, screen.HTML, "Switched off: their trades are no longer copied.\n\n<b>Alice</b> (0x5aAe…eAed) in Copy whales: off")
		assert.Equal(t, []string{"⏻ Switch on=ts:wf-3:0:0x5aaeb605:1", "« Copy whales=tw:wf-3"}, buttons(screen))

		assert.Equal(t, false, aliceOn(m))
		assert.Equal(t, 2, m.workflows.items[2].Version)
		assert.Equal(t, 2.0, m.workflows.items[2].Nodes[0].Params["traders"].([]any)[0].(map[string]any)["scale"], "the rest kept")
		assert.Equal(t, true, before[0].Params["traders"].([]any)[0].(map[string]any)["on"], "the workflow read is not changed")

		s.HandleUpdate(ctx, press("ts:wf-3:0:0x5aaeb605:0"))
		assert.Equal(t, 1, m.workflows.saves, "already off: nothing to save")

		s.HandleUpdate(ctx, press("ts:wf-3:0:0x5aaeb605:1"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Switched on: their trades are copied again.")
		assert.Equal(t, true, aliceOn(m))
		assert.Equal(t, 3, m.workflows.items[2].Version)
	})

	t.Run("a save in between is retried once", func(t *testing.T) {
		s, m := tradersMenu(t)
		m.workflows.conflicts = 1

		s.HandleUpdate(ctx, press("ts:wf-3:0:0x5aaeb605:0"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Switched off")
		assert.Equal(t, false, aliceOn(m))

		m.workflows.conflicts = 2

		s.HandleUpdate(ctx, press("ts:wf-3:0:0x5aaeb605:1"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Could not switch them: the workflow was being saved meanwhile. Try again.")
		assert.Equal(t, false, aliceOn(m))
		assert.Equal(t, 1, m.workflows.saves)
	})

	t.Run("a trader no longer in the table", func(t *testing.T) {
		s, m := tradersMenu(t)

		s.HandleUpdate(ctx, press("tt:wf-3:0:0xdeadbeef"))
		screen := m.bot.lastEdit(t)
		assert.Equal(t, "This trader is no longer in the workflow's table.", screen.HTML)
		assert.Equal(t, []string{"« Back=tw:wf-3"}, buttons(screen))

		s.HandleUpdate(ctx, press("ts:wf-3:5:0x5aaeb605:0"))
		assert.Equal(t, "This trader is no longer in the workflow's table.", m.bot.lastEdit(t).HTML)
		assert.Equal(t, 0, m.workflows.saves)
	})

	t.Run("a workflow gone, or not following anyone now", func(t *testing.T) {
		s, m := tradersMenu(t)

		s.HandleUpdate(ctx, press("tw:wf-gone"))
		assert.Equal(t, "This workflow no longer exists.", m.bot.lastEdit(t).HTML)

		s.HandleUpdate(ctx, press("tw:wf-1"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "It follows no traders now.")
	})

	t.Run("button data fits Telegram's 64 bytes", func(t *testing.T) {
		s, m := newMenu(t)
		m.workflows.items = append(m.workflows.items, copyFlow("0199f3a2-7c41-7b6e-9d2a-5f0c3e8b1a47"))

		s.HandleUpdate(ctx, press("tt:0199f3a2-7c41-7b6e-9d2a-5f0c3e8b1a47:1:0xfb691609"))
		screen := m.bot.lastEdit(t)
		require.NotEmpty(t, screen.Buttons)

		for _, row := range screen.Buttons {
			for _, b := range row {
				assert.LessOrEqual(t, len(b.Data), 64, b.Data)
			}
		}
	})

	t.Run("without trading", func(t *testing.T) {
		s, m := newMenu(t)
		s.UseMenu(telegram.Menu{Workflows: m.workflows})

		s.HandleUpdate(ctx, message("Traders"))
		assert.Equal(t, "This part of the menu is not available.", m.bot.last(t).HTML)

		s.HandleUpdate(ctx, press("tw:wf-1"))
		assert.Equal(t, []string{"This button no longer works."}, m.bot.answers)
	})
}
