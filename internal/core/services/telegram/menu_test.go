package telegram_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

// fakeWorkflows is alice's workflows.
type fakeWorkflows struct {
	items     []domain.Workflow
	runs      map[domain.WorkflowID][]domain.Execution
	ran       domain.Execution
	runErr    error
	toggleErr error
	pageSize  int
	conflicts int // saves in between, each refusing an Update
	saves     int
}

func (f *fakeWorkflows) owned(p auth.Principal) bool { return p.User.ID == alice.ID }

func (f *fakeWorkflows) List(_ context.Context, p auth.Principal, fl ports.PageFilter) (ports.Page[domain.Workflow], error) {
	if !f.owned(p) {
		return ports.Page[domain.Workflow]{}, nil
	}

	start := 0
	if fl.Cursor != "" {
		start = slices.IndexFunc(f.items, func(w domain.Workflow) bool { return string(w.ID) == fl.Cursor }) + 1
	}

	size := fl.Limit
	if f.pageSize > 0 {
		size = min(size, f.pageSize)
	}

	end := min(start+size, len(f.items))
	page := ports.Page[domain.Workflow]{Items: f.items[start:end]}

	if end < len(f.items) {
		page.NextCursor = string(f.items[end-1].ID)
	}

	return page, nil
}

func (f *fakeWorkflows) Get(_ context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	for _, w := range f.items {
		if w.ID == id && f.owned(p) {
			return w, nil
		}
	}

	return domain.Workflow{}, domain.ErrNotFound
}

func (f *fakeWorkflows) setActive(p auth.Principal, id domain.WorkflowID, on bool) (domain.Workflow, error) {
	if f.toggleErr != nil {
		return domain.Workflow{}, f.toggleErr
	}

	for i, w := range f.items {
		if w.ID == id && f.owned(p) {
			f.items[i].Active = on
			return f.items[i], nil
		}
	}

	return domain.Workflow{}, domain.ErrNotFound
}

func (f *fakeWorkflows) Activate(_ context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	return f.setActive(p, id, true)
}

func (f *fakeWorkflows) Deactivate(_ context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error) {
	return f.setActive(p, id, false)
}

func (f *fakeWorkflows) Run(context.Context, auth.Principal, domain.WorkflowID, domain.NodeID) (domain.Execution, error) {
	return f.ran, f.runErr
}

func (f *fakeWorkflows) Executions(_ context.Context, _ auth.Principal, id domain.WorkflowID, _ ports.PageFilter) (ports.Page[domain.Execution], error) {
	return ports.Page[domain.Execution]{Items: f.runs[id]}, nil
}

func (f *fakeWorkflows) Update(_ context.Context, p auth.Principal, id domain.WorkflowID, version int, def workflows.Definition) (domain.Workflow, error) {
	for i, w := range f.items {
		if w.ID != id || !f.owned(p) {
			continue
		}

		if f.conflicts > 0 {
			f.conflicts--
			f.items[i].Version++

			return domain.Workflow{}, domain.ErrConflict
		}

		if version != w.Version {
			return domain.Workflow{}, domain.ErrConflict
		}

		f.saves++
		f.items[i].Name, f.items[i].Nodes, f.items[i].Connections = def.Name, def.Nodes, def.Connections
		f.items[i].Version++

		return f.items[i], nil
	}

	return domain.Workflow{}, domain.ErrNotFound
}

type fakeTrading map[domain.WorkflowID]trading.Summary

func (f fakeTrading) Summary(_ context.Context, p auth.Principal, id domain.WorkflowID, _ domain.TradingMode) (trading.Summary, error) {
	if p.User.ID != alice.ID {
		return trading.Summary{}, domain.ErrNotFound
	}

	return f[id], nil
}

type fakeWallets []domain.Wallet

func (f fakeWallets) List(_ context.Context, p auth.Principal) ([]domain.Wallet, error) {
	if p.User.ID != alice.ID {
		return nil, nil
	}

	return f, nil
}

type fakePositions struct {
	items []domain.Position
	err   error
	asked domain.WalletAddress
}

func (f *fakePositions) Positions(_ context.Context, fl ports.PositionFilter) (ports.Page[domain.Position], error) {
	f.asked = fl.Wallet
	return ports.Page[domain.Position]{Items: f.items}, f.err
}

type fakeNotifications struct {
	on bool
}

func (f *fakeNotifications) Prefs(context.Context, auth.Principal) ([]notify.Pref, error) {
	return []notify.Pref{{Kind: notify.WorkflowFailed, Enabled: f.on}}, nil
}

func (f *fakeNotifications) SetPref(_ context.Context, _ auth.Principal, key string, enabled bool) (notify.Pref, error) {
	if key != notify.WorkflowFailed.Key {
		return notify.Pref{}, domain.ErrNotFound
	}

	f.on = enabled

	return notify.Pref{Kind: notify.WorkflowFailed, Enabled: enabled}, nil
}

type menuDeps struct {
	deps
	workflows     *fakeWorkflows
	positions     *fakePositions
	notifications *fakeNotifications
	trading       fakeTrading
}

func newMenu(t *testing.T) (*telegram.Service, menuDeps) {
	t.Helper()

	s, d := newService(t)
	require.NoError(t, d.links.Save(context.Background(), domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

	m := menuDeps{
		deps: d,
		workflows: &fakeWorkflows{
			items: []domain.Workflow{
				{ID: "wf-1", Name: "Spreads <tight>", Active: true},
				{ID: "wf-2", Name: "Big movers"},
			},
			runs: map[domain.WorkflowID][]domain.Execution{
				"wf-1": {
					{Status: domain.ExecutionFailed, StartedAt: now.Add(-5 * time.Minute), FinishedAt: now.Add(-5*time.Minute + 1200*time.Millisecond), Error: `node "hook": refused`},
					{Status: domain.ExecutionSucceeded, StartedAt: now.Add(-3 * time.Hour), FinishedAt: now.Add(-3*time.Hour + 300*time.Millisecond)},
					{Status: domain.ExecutionFailed, StartedAt: now.Add(-30 * time.Hour)},
				},
			},
		},
		positions:     &fakePositions{},
		notifications: &fakeNotifications{on: true},
		trading:       fakeTrading{},
	}

	s.UseMenu(telegram.Menu{
		Workflows:     m.workflows,
		Wallets:       fakeWallets{{ID: "w-1", Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", Label: "Main"}},
		Positions:     m.positions,
		Notifications: m.notifications,
		Trading:       m.trading,
	})

	return s, m
}

func press(data string) ports.TelegramUpdate {
	return ports.TelegramUpdate{Callback: &ports.TelegramCallback{ID: "cb", ChatID: 70, MessageID: 9, From: tgAlice, Data: data}}
}

func (b *fakeBot) lastEdit(t *testing.T) ports.TelegramMessage {
	t.Helper()
	require.NotEmpty(t, b.edits)

	return b.edits[len(b.edits)-1]
}

// buttons lists the data of every button, row by row.
func buttons(m ports.TelegramMessage) []string {
	var out []string

	for _, row := range m.Buttons {
		for _, b := range row {
			out = append(out, b.Text+"="+b.Data)
		}
	}

	return out
}

func TestMenuWorkflows(t *testing.T) {
	ctx := context.Background()

	t.Run("the keyboard opens the list, which pages", func(t *testing.T) {
		s, m := newMenu(t)
		m.workflows.pageSize = 1

		s.HandleUpdate(ctx, message("📋 Workflows"))
		list := m.bot.last(t)
		assert.Contains(t, list.HTML, "<b>Workflows</b>")
		assert.Equal(t, []string{"● Spreads <tight>=wo:wf-1", "More »=wl:wf-1"}, buttons(list))

		s.HandleUpdate(ctx, press("wl:wf-1"))
		assert.Equal(t, []string{"○ Big movers=wo:wf-2", "« First=wl:"}, buttons(m.bot.lastEdit(t)))
		assert.Equal(t, []string{""}, m.bot.answers)
	})

	t.Run("one workflow, with its last runs", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, press("wo:wf-1"))
		screen := m.bot.lastEdit(t)

		assert.Contains(t, screen.HTML, "<b>Spreads &lt;tight&gt;</b>\nOn: it runs by itself.")
		assert.Contains(t, screen.HTML, "❌ 5 min ago · 1.2s · node &#34;hook&#34;: refused")
		assert.Contains(t, screen.HTML, "✅ 3 h ago · 300 ms")
		assert.Equal(t, []string{"▶ Run now=wr:wf-1", "⏻ Switch off=wd:wf-1", "↻ Refresh=wo:wf-1", "« Workflows=wl:"}, buttons(screen))
	})

	t.Run("run now answers at once, then shows the outcome", func(t *testing.T) {
		s, m := newMenu(t)
		m.workflows.ran = domain.Execution{Status: domain.ExecutionSucceeded, StartedAt: now, FinishedAt: now.Add(2 * time.Second)}

		s.HandleUpdate(ctx, press("wr:wf-2"))
		assert.Equal(t, []string{"Running…"}, m.bot.answers)
		assert.True(t, strings.HasPrefix(m.bot.lastEdit(t).HTML, "✅ Ran in 2s.\n\n<b>Big movers</b>"))

		m.workflows.runErr = errors.New("invalid input: the workflow has no manual or schedule trigger to run from")
		s.HandleUpdate(ctx, press("wr:wf-2"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Could not run it: invalid input")
	})

	t.Run("switching on and off", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, press("wa:wf-2"))
		assert.True(t, m.workflows.items[1].Active)
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Switched on.")
		assert.Contains(t, buttons(m.bot.lastEdit(t)), "⏻ Switch off=wd:wf-2")

		m.workflows.toggleErr = errors.New("invalid input: at most 20 active workflows per user")
		s.HandleUpdate(ctx, press("wd:wf-2"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Could not switch it: invalid input: at most 20 active workflows per user")
	})

	t.Run("someone else's or a deleted workflow", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, press("wo:wf-9"))
		assert.Equal(t, "This workflow no longer exists.", m.bot.lastEdit(t).HTML)
	})
}

func TestMenuStatus(t *testing.T) {
	s, m := newMenu(t)

	s.HandleUpdate(context.Background(), message("status"))
	status := m.bot.last(t).HTML

	assert.Contains(t, status, "1 workflow on.")
	assert.Contains(t, status, "• Spreads &lt;tight&gt;: last run ❌ 5 min ago")
	assert.Contains(t, status, "1 failure in the last 24 hours, in 1 workflow.", "the older failure is not counted")
	assert.Equal(t, []string{"↻ Refresh=s:s"}, buttons(m.bot.last(t)))
}

func TestMenuPositions(t *testing.T) {
	ctx := context.Background()

	t.Run("pick a wallet, see its biggest positions", func(t *testing.T) {
		s, m := newMenu(t)
		m.positions.items = []domain.Position{
			{Title: "Will it rain?", Outcome: "Yes", Size: decimal.RequireFromString("10"), AvgPrice: decimal.RequireFromString("0.4"),
				CurrentPrice: decimal.RequireFromString("0.5"), Value: decimal.RequireFromString("5"), UnrealizedPnL: decimal.RequireFromString("1")},
			{Title: "Will it snow?", Outcome: "No", Size: decimal.RequireFromString("100"), AvgPrice: decimal.RequireFromString("0.7"),
				CurrentPrice: decimal.RequireFromString("0.6"), Value: decimal.RequireFromString("60"), UnrealizedPnL: decimal.RequireFromString("-10")},
		}

		s.HandleUpdate(ctx, message("💼 Positions"))
		assert.Equal(t, []string{"Main · 0x5aAe…eAed=pw:w-1"}, buttons(m.bot.last(t)))

		s.HandleUpdate(ctx, press("pw:w-1"))
		screen := m.bot.lastEdit(t).HTML

		assert.Equal(t, domain.WalletAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"), m.positions.asked)
		assert.Contains(t, screen, "Worth $65.00 in 2 positions.")
		assert.Less(t, strings.Index(screen, "snow"), strings.Index(screen, "rain"), "biggest first")
		assert.Contains(t, screen, "• Will it snow?: <b>No</b>\n  100 at 0.7 → 0.6, $60.00 (-10.00)")
		assert.Contains(t, screen, "(+1.00)")
	})

	t.Run("a wallet that is not yours", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, press("pw:w-9"))
		assert.Equal(t, "This wallet is no longer linked.", m.bot.lastEdit(t).HTML)
		assert.Empty(t, m.positions.asked)
	})

	t.Run("Polymarket not answering", func(t *testing.T) {
		s, m := newMenu(t)
		m.positions.err = domain.ErrUnavailable

		s.HandleUpdate(ctx, press("pw:w-1"))
		assert.Contains(t, m.bot.lastEdit(t).HTML, "Polymarket did not answer")
	})
}

func TestMenuNotifications(t *testing.T) {
	s, m := newMenu(t)

	s.HandleUpdate(context.Background(), message("🔔 Notifications"))
	assert.Equal(t, []string{"✅ A workflow fails=n:workflow_failed", "Unlink this chat=unlink"}, buttons(m.bot.last(t)))

	s.HandleUpdate(context.Background(), press("n:workflow_failed"))
	assert.False(t, m.notifications.on)
	assert.Equal(t, []string{"⬜ A workflow fails=n:workflow_failed", "Unlink this chat=unlink"}, buttons(m.bot.lastEdit(t)))
}

func TestMenuEdges(t *testing.T) {
	ctx := context.Background()

	t.Run("section words are not for workflows", func(t *testing.T) {
		s, m := newMenu(t)
		messages, cancel := s.Subscribe(alice.ID, func(string) bool { return true })
		defer cancel()

		s.HandleUpdate(ctx, message("Workflows"))
		assert.Empty(t, messages)
		assert.Contains(t, m.bot.last(t).HTML, "<b>Workflows</b>")
	})

	t.Run("anything else not taken points at the menu", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, message("hello"))
		assert.Contains(t, m.bot.last(t).HTML, "/menu")
		assert.NotEmpty(t, m.bot.last(t).Keyboard)
	})

	t.Run("disabled accounts press nothing", func(t *testing.T) {
		s, d := newService(t)
		bob := domain.User{ID: "bob", DisabledAt: now}
		d.users.EXPECT().ByID(mock.Anything, bob.ID).Return(bob, nil)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: bob.ID, TelegramUserID: 8, ChatID: 80}))

		s.HandleUpdate(ctx, ports.TelegramUpdate{Callback: &ports.TelegramCallback{ID: "cb", From: ports.TelegramUser{ID: 8}, Data: "s:w"}})
		assert.Equal(t, []string{"Your Polythought account is disabled."}, d.bot.answers)
	})

	t.Run("without a menu", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		s.HandleUpdate(ctx, message("Status"))
		assert.Equal(t, "This part of the menu is not available.", d.bot.last(t).HTML)

		s.HandleUpdate(ctx, press("s:w"))
		assert.Equal(t, []string{"This button no longer works."}, d.bot.answers)
	})

	t.Run("unknown buttons", func(t *testing.T) {
		s, m := newMenu(t)

		s.HandleUpdate(ctx, press("zz:1"))
		assert.Equal(t, []string{"This button no longer works."}, m.bot.answers)
	})
}
