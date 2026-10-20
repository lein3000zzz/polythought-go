package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/notify"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

// The bot's menu: sections on the keyboard under the input field, and inside each, a
// message whose buttons edit it in place. Button data names what to show by short
// prefixes and IDs; every press is checked against the linked account, so old buttons
// and other people's IDs lead nowhere.

// Workflows is what the menu needs from the workflow service.
type Workflows interface {
	List(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[domain.Workflow], error)
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Activate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Deactivate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	Run(ctx context.Context, p auth.Principal, id domain.WorkflowID, trigger domain.NodeID) (domain.Execution, error)
	Executions(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.Execution], error)
	Update(ctx context.Context, p auth.Principal, id domain.WorkflowID, version int, def workflows.Definition) (domain.Workflow, error)
}

// Wallets is what the menu needs from the wallet service.
type Wallets interface {
	List(ctx context.Context, p auth.Principal) ([]domain.Wallet, error)
}

// Notifications is what the menu needs from the notification service.
type Notifications interface {
	Prefs(ctx context.Context, p auth.Principal) ([]notify.Pref, error)
	SetPref(ctx context.Context, p auth.Principal, key string, enabled bool) (notify.Pref, error)
}

// KillSwitch is the trading service's kill switch, for the Status section (D225).
type KillSwitch interface {
	SetKillSwitch(ctx context.Context, p auth.Principal, on bool) (trading.KillSwitch, error)
}

// Menu is what the bot's menu reads and does for the linked user.
type Menu struct {
	Workflows     Workflows
	Wallets       Wallets
	Positions     ports.PositionReader
	Notifications Notifications
	Trading       Trading    // optional: without it, the Traders section is not available
	Live          KillSwitch // optional: without it, Status has no kill switch
}

// UseMenu gives the bot its menu. Call it before the bot runs.
func (s *Service) UseMenu(m Menu) {
	s.menu = &m
}

const (
	sectionWorkflows     = "📋 Workflows"
	sectionStatus        = "📊 Status"
	sectionPositions     = "💼 Positions"
	sectionNotifications = "🔔 Notifications"

	workflowsPerPage = 6
	lastRuns         = 5
	positionsShown   = 10
	statusWorkflows  = 15
	statusRuns       = 20
	maxNoteText      = 120
)

var mainKeyboard = [][]string{{sectionWorkflows, sectionTraders}, {sectionPositions, sectionStatus}, {sectionNotifications}}

// Button data prefixes.
const (
	dataSection    = "s:"  // s:w, s:t, s:s, s:p, s:n: a section's first screen
	dataWorkflows  = "wl:" // wl:<cursor>: a page of workflows
	dataWorkflow   = "wo:" // wo:<id>: one workflow
	dataRun        = "wr:" // wr:<id>: run it now
	dataActivate   = "wa:" // wa:<id>: switch it on
	dataDeactivate = "wd:" // wd:<id>: switch it off
	dataWallet     = "pw:" // pw:<wallet id>: its positions
	dataNotify     = "n:"  // n:<kind>: switch a notification
	dataKill       = "ks:" // ks:on stops live trading at once; ks:ask asks before ks:off resumes it
)

// screen is what a menu message shows.
type screen struct {
	html    string
	buttons [][]ports.TelegramButton
}

// sectionOf reads which section a message asks for, from the keyboard's text or the
// plain word.
func sectionOf(text string) string {
	word := strings.ToLower(strings.TrimLeftFunc(strings.TrimSpace(text), func(r rune) bool { return !unicode.IsLetter(r) }))

	switch word {
	case "workflows":
		return "w"
	case "traders":
		return "t"
	case "status":
		return "s"
	case "positions":
		return "p"
	case "notifications":
		return "n"
	}

	return ""
}

// showMenu puts the menu under the input field, with a word on who the chat is for.
func (s *Service) showMenu(ctx context.Context, chatID int64, user domain.User) {
	_, err := s.bot.Send(ctx, ports.TelegramMessage{ChatID: chatID, HTML: linkedText(user) + " Pick a section below.", Keyboard: mainKeyboard})
	s.ignored(ctx, "Telegram menu not sent", err)
}

// openSection sends a section's first screen as a new message.
func (s *Service) openSection(ctx context.Context, chatID int64, user domain.User, section string) {
	sc := s.section(ctx, user, section)
	s.reply(ctx, chatID, sc.html, sc.buttons)
}

func (s *Service) section(ctx context.Context, user domain.User, section string) screen {
	if s.menu == nil {
		return screen{html: "This part of the menu is not available."}
	}

	switch section {
	case "w":
		return s.workflowList(ctx, user, "")
	case "t":
		return s.traderFlows(ctx, user)
	case "s":
		return s.status(ctx, user)
	case "p":
		return s.wallets(ctx, user)
	case "n":
		return s.notifications(ctx, user, "")
	}

	return screen{html: textStale}
}

// menuPress handles the menu's buttons: it answers the press and shows the next screen
// in place of the message pressed.
func (s *Service) menuPress(ctx context.Context, cb ports.TelegramCallback, user domain.User) {
	if s.menu == nil {
		s.answer(ctx, cb.ID, textStale)
		return
	}

	kind, arg, _ := strings.Cut(cb.Data, ":")
	kind += ":"

	var (
		sc   screen
		note string
	)

	switch kind {
	case dataSection:
		sc = s.section(ctx, user, arg)
	case dataWorkflows:
		sc = s.workflowList(ctx, user, arg)
	case dataWorkflow:
		sc = s.workflow(ctx, user, domain.WorkflowID(arg), "")
	case dataRun:
		// Runs can take a while; the press is answered first so Telegram stops waiting.
		s.answer(ctx, cb.ID, "Running…")
		sc = s.workflow(ctx, user, domain.WorkflowID(arg), s.run(ctx, user, domain.WorkflowID(arg)))
		s.show(ctx, cb, sc)

		return
	case dataActivate, dataDeactivate:
		note = s.setActive(ctx, user, domain.WorkflowID(arg), kind == dataActivate)
		sc = s.workflow(ctx, user, domain.WorkflowID(arg), note)
	case dataWallet:
		sc = s.positions(ctx, user, domain.WalletID(arg))
	case dataNotify:
		sc = s.notifications(ctx, user, arg)
	case dataKill:
		sc = s.killSwitch(ctx, user, arg)
	case dataTraderFlows:
		sc = s.traderFlows(ctx, user)
	case dataTraderFlow, dataTrader, dataTraderSwitch:
		if s.menu.Trading == nil {
			s.answer(ctx, cb.ID, textStale)
			return
		}

		sc = s.traderPress(ctx, user, kind, arg)
	default:
		s.answer(ctx, cb.ID, textStale)
		return
	}

	s.answer(ctx, cb.ID, "")
	s.show(ctx, cb, sc)
}

func (s *Service) show(ctx context.Context, cb ports.TelegramCallback, sc screen) {
	err := s.bot.Edit(ctx, cb.MessageID, ports.TelegramMessage{ChatID: cb.ChatID, HTML: sc.html, Buttons: sc.buttons})
	s.ignored(ctx, "Telegram menu not updated", err)
}

func principal(u domain.User) auth.Principal {
	return auth.Principal{User: u}
}

// Workflows.

func (s *Service) workflowList(ctx context.Context, user domain.User, cursor string) screen {
	page, err := s.menu.Workflows.List(ctx, principal(user), ports.PageFilter{Limit: workflowsPerPage, Cursor: cursor})
	if err != nil {
		return s.trouble(ctx, err)
	}

	if len(page.Items) == 0 && cursor == "" {
		return screen{html: "<b>Workflows</b>\n\nYou have none yet. Make one in Polythought, or start from its library."}
	}

	var rows [][]ports.TelegramButton

	for _, w := range page.Items {
		mark := "○"
		if w.Active {
			mark = "●"
		}

		rows = append(rows, []ports.TelegramButton{{Text: mark + " " + w.Name, Data: dataWorkflow + string(w.ID)}})
	}

	var nav []ports.TelegramButton
	if cursor != "" {
		nav = append(nav, ports.TelegramButton{Text: "« First", Data: dataWorkflows})
	}

	if page.NextCursor != "" {
		nav = append(nav, ports.TelegramButton{Text: "More »", Data: dataWorkflows + page.NextCursor})
	}

	if len(nav) > 0 {
		rows = append(rows, nav)
	}

	return screen{
		html:    "<b>Workflows</b>\n● on, ○ off. Pick one to run it, switch it on or off, or see its last runs.",
		buttons: rows,
	}
}

// workflow shows one workflow and its last runs, with note on top, such as the outcome
// of a run.
func (s *Service) workflow(ctx context.Context, user domain.User, id domain.WorkflowID, note string) screen {
	back := []ports.TelegramButton{{Text: "« Workflows", Data: dataWorkflows}}

	w, err := s.menu.Workflows.Get(ctx, principal(user), id)
	if errors.Is(err, domain.ErrNotFound) {
		return screen{html: "This workflow no longer exists.", buttons: [][]ports.TelegramButton{back}}
	}

	if err != nil {
		return s.trouble(ctx, err)
	}

	var b strings.Builder

	if note != "" {
		b.WriteString(note + "\n\n")
	}

	b.WriteString("<b>" + html.EscapeString(w.Name) + "</b>\n")

	if w.Active {
		b.WriteString("On: it runs by itself.\n")
	} else {
		b.WriteString("Off.\n")
	}

	runs, err := s.menu.Workflows.Executions(ctx, principal(user), id, ports.PageFilter{Limit: lastRuns})
	if err != nil {
		return s.trouble(ctx, err)
	}

	if len(runs.Items) == 0 {
		b.WriteString("\nNo runs yet.")
	} else {
		b.WriteString("\nLast runs:")

		for _, e := range runs.Items {
			b.WriteString("\n" + s.runLine(e))
		}
	}

	toggle := ports.TelegramButton{Text: "⏻ Switch on", Data: dataActivate + string(id)}
	if w.Active {
		toggle = ports.TelegramButton{Text: "⏻ Switch off", Data: dataDeactivate + string(id)}
	}

	return screen{html: b.String(), buttons: [][]ports.TelegramButton{
		{{Text: "▶ Run now", Data: dataRun + string(id)}, toggle},
		{{Text: "↻ Refresh", Data: dataWorkflow + string(id)}, back[0]},
	}}
}

func (s *Service) runLine(e domain.Execution) string {
	line := statusMark(e.Status) + " " + s.ago(e.StartedAt)
	if !e.FinishedAt.IsZero() {
		line += " · " + roundDuration(e.FinishedAt.Sub(e.StartedAt))
	}

	if e.Error != "" {
		line += " · " + html.EscapeString(truncate(e.Error, maxNoteText))
	}

	return line
}

func (s *Service) run(ctx context.Context, user domain.User, id domain.WorkflowID) string {
	e, err := s.menu.Workflows.Run(ctx, principal(user), id, "")
	if err != nil {
		return "Could not run it: " + html.EscapeString(truncate(err.Error(), maxNoteText))
	}

	if e.Status == domain.ExecutionSucceeded {
		return "✅ Ran in " + roundDuration(e.FinishedAt.Sub(e.StartedAt)) + "."
	}

	note := statusMark(e.Status) + " The run " + string(e.Status)
	if e.Error != "" {
		note += ": " + html.EscapeString(truncate(e.Error, maxNoteText))
	}

	return note + "."
}

func (s *Service) setActive(ctx context.Context, user domain.User, id domain.WorkflowID, on bool) string {
	var err error
	if on {
		_, err = s.menu.Workflows.Activate(ctx, principal(user), id)
	} else {
		_, err = s.menu.Workflows.Deactivate(ctx, principal(user), id)
	}

	switch {
	case err != nil:
		return "Could not switch it: " + html.EscapeString(truncate(err.Error(), maxNoteText))
	case on:
		return "Switched on."
	}

	// Switched off here, its live orders keep resting (D217): the app cancels them.
	if setup, err := s.menu.Trading.Setup(ctx, principal(user), id); err == nil && setup.Resting > 0 {
		return fmt.Sprintf("Switched off. Its %d live %s still %s on the book: cancel %s in its Trading view if you want.",
			setup.Resting, plural(setup.Resting, "order", "orders"), plural(setup.Resting, "rests", "rest"),
			plural(setup.Resting, "it", "them"))
	}

	return "Switched off."
}

// Status.

func (s *Service) status(ctx context.Context, user domain.User) screen {
	return s.statusWith(ctx, user, "")
}

// statusWith is Status, with a note on what was just done above it.
func (s *Service) statusWith(ctx context.Context, user domain.User, note string) screen {
	refresh := [][]ports.TelegramButton{{{Text: "↻ Refresh", Data: dataSection + "s"}}}
	if b, ok := s.killButton(ctx, user); ok {
		refresh = append([][]ports.TelegramButton{{b}}, refresh...)
	}

	var active []domain.Workflow

	cursor := ""

	for range 20 { // pages of 50: as many workflows as anyone may have
		page, err := s.menu.Workflows.List(ctx, principal(user), ports.PageFilter{Limit: 50, Cursor: cursor})
		if err != nil {
			return s.trouble(ctx, err)
		}

		for _, w := range page.Items {
			if w.Active {
				active = append(active, w)
			}
		}

		if cursor = page.NextCursor; cursor == "" {
			break
		}
	}

	var b strings.Builder

	b.WriteString("<b>Status</b>\n")

	if note != "" {
		b.WriteString("\n" + note + "\n")
	}

	if user.LiveStopped() {
		b.WriteString("\n🛑 Live trading is stopped (" + s.ago(user.LiveStoppedAt) + "): your workflows place no live orders.\n")
	}

	if len(active) == 0 {
		b.WriteString("\nNo workflow is on.")
		return screen{html: b.String(), buttons: refresh}
	}

	fmt.Fprintf(&b, "\n%d %s on.", len(active), plural(len(active), "workflow", "workflows"))

	failures, failing := 0, 0
	since := s.now().Add(-24 * time.Hour)

	for i, w := range active {
		runs, err := s.menu.Workflows.Executions(ctx, principal(user), w.ID, ports.PageFilter{Limit: statusRuns})
		if err != nil {
			return s.trouble(ctx, err)
		}

		failed := 0

		for _, e := range runs.Items {
			if e.Status == domain.ExecutionFailed && e.StartedAt.After(since) {
				failed++
			}
		}

		if failed > 0 {
			failures += failed
			failing++
		}

		if i >= statusWorkflows {
			continue
		}

		line := "\n• " + html.EscapeString(w.Name)
		if len(runs.Items) > 0 {
			line += ": last run " + statusMark(runs.Items[0].Status) + " " + s.ago(runs.Items[0].StartedAt)
		}

		b.WriteString(line)
	}

	if len(active) > statusWorkflows {
		fmt.Fprintf(&b, "\n…and %d more.", len(active)-statusWorkflows)
	}

	if failures == 0 {
		b.WriteString("\n\nNo failures in the last 24 hours.")
	} else {
		fmt.Fprintf(&b, "\n\n%d %s in the last 24 hours, in %d %s.",
			failures, plural(failures, "failure", "failures"), failing, plural(failing, "workflow", "workflows"))
	}

	return screen{html: b.String(), buttons: refresh}
}

// killButton stops live trading, or offers to resume it (D225): for users with a wallet
// that can trade, or whose kill switch is on.
func (s *Service) killButton(ctx context.Context, user domain.User) (ports.TelegramButton, bool) {
	if s.menu.Live == nil {
		return ports.TelegramButton{}, false
	}

	if user.LiveStopped() {
		return ports.TelegramButton{Text: "▶️ Resume live trading", Data: dataKill + "ask"}, true
	}

	wallets, err := s.menu.Wallets.List(ctx, principal(user))
	if err != nil {
		s.ignored(ctx, "Wallets not read for the kill switch", err)
		return ports.TelegramButton{}, false
	}

	if !slices.ContainsFunc(wallets, func(w domain.Wallet) bool { return w.HasKey || w.HasSessionKey }) {
		return ports.TelegramButton{}, false
	}

	return ports.TelegramButton{Text: "🛑 Stop live trading", Data: dataKill + "on"}, true
}

// killSwitch stops live trading at once, or resumes it once asked (D225).
func (s *Service) killSwitch(ctx context.Context, user domain.User, arg string) screen {
	if s.menu.Live == nil {
		return screen{html: textStale}
	}

	switch arg {
	case "ask":
		return screen{
			html: "<b>Resume live trading?</b>\n\nYour workflows trading live place orders again, as before.",
			buttons: [][]ports.TelegramButton{{
				{Text: "▶️ Resume", Data: dataKill + "off"}, {Text: "Keep stopped", Data: dataSection + "s"},
			}},
		}
	case "on", "off":
		ks, err := s.menu.Live.SetKillSwitch(ctx, principal(user), arg == "on")
		if err != nil {
			return s.trouble(ctx, err)
		}

		user.LiveStoppedAt = ks.Since

		if !ks.On {
			return s.statusWith(ctx, user, "▶️ Live trading resumed.")
		}

		note := "🛑 Live trading stopped."
		if ks.Cancelled > 0 {
			note += fmt.Sprintf(" %d resting %s cancelled.", ks.Cancelled, plural(ks.Cancelled, "order", "orders"))
		}

		if ks.Left > 0 {
			note += fmt.Sprintf(" %d still %s: Polymarket did not cancel %s; see their workflows' Trading view.",
				ks.Left, plural(ks.Left, "rests", "rest"), plural(ks.Left, "it", "them"))
		}

		return s.statusWith(ctx, user, note)
	}

	return screen{html: textStale}
}

// Positions.

func (s *Service) wallets(ctx context.Context, user domain.User) screen {
	wallets, err := s.menu.Wallets.List(ctx, principal(user))
	if err != nil {
		return s.trouble(ctx, err)
	}

	if len(wallets) == 0 {
		return screen{html: "<b>Positions</b>\n\nYou have no wallets linked. Link one in Polythought, on its Wallets page."}
	}

	rows := make([][]ports.TelegramButton, len(wallets))
	for i, w := range wallets {
		rows[i] = []ports.TelegramButton{{Text: walletName(w), Data: dataWallet + string(w.ID)}}
	}

	return screen{html: "<b>Positions</b>\nWhich wallet?", buttons: rows}
}

func (s *Service) positions(ctx context.Context, user domain.User, id domain.WalletID) screen {
	back := []ports.TelegramButton{{Text: "« Wallets", Data: dataSection + "p"}}

	wallets, err := s.menu.Wallets.List(ctx, principal(user))
	if err != nil {
		return s.trouble(ctx, err)
	}

	i := slices.IndexFunc(wallets, func(w domain.Wallet) bool { return w.ID == id })
	if i < 0 {
		return screen{html: "This wallet is no longer linked.", buttons: [][]ports.TelegramButton{back}}
	}

	w := wallets[i]

	page, err := s.menu.Positions.Positions(ctx, ports.PositionFilter{Wallet: w.Address, Limit: 100})
	if err != nil {
		s.log.Warn(ctx, "Positions not read for Telegram", "wallet_id", w.ID, "error", err.Error())

		return screen{html: "Polymarket did not answer. Try again in a minute.", buttons: [][]ports.TelegramButton{{
			{Text: "↻ Try again", Data: dataWallet + string(id)}, back[0],
		}}}
	}

	positions := slices.SortedFunc(slices.Values(page.Items), func(a, b domain.Position) int { return b.Value.Cmp(a.Value) })

	var b strings.Builder

	b.WriteString("<b>Positions of " + html.EscapeString(walletName(w)) + "</b>\n")

	if len(positions) == 0 {
		b.WriteString("\nNo open positions.")
	} else {
		total := decimal.Zero
		for _, p := range positions {
			total = total.Add(p.Value)
		}

		fmt.Fprintf(&b, "Worth $%s in %d %s.\n", total.StringFixed(2), len(positions), plural(len(positions), "position", "positions"))

		for _, p := range positions[:min(len(positions), positionsShown)] {
			b.WriteString("\n" + positionLine(p))
		}

		if len(positions) > positionsShown {
			fmt.Fprintf(&b, "\n\n…and %d smaller ones.", len(positions)-positionsShown)
		}
	}

	return screen{html: b.String(), buttons: [][]ports.TelegramButton{{
		{Text: "↻ Refresh", Data: dataWallet + string(id)}, back[0],
	}}}
}

func positionLine(p domain.Position) string {
	title := p.Title
	if title == "" {
		title = string(p.MarketID)
	}

	pnl := p.UnrealizedPnL.StringFixed(2)
	if p.UnrealizedPnL.IsPositive() {
		pnl = "+" + pnl
	}

	return fmt.Sprintf("• %s: <b>%s</b>\n  %s at %s → %s, $%s (%s)",
		html.EscapeString(truncate(title, 80)), html.EscapeString(p.Outcome),
		p.Size.Round(2).String(), p.AvgPrice.Round(3).String(), p.CurrentPrice.Round(3).String(),
		p.Value.StringFixed(2), pnl)
}

func walletName(w domain.Wallet) string {
	addr := string(w.Address)
	short := addr
	if len(addr) > 10 {
		short = addr[:6] + "…" + addr[len(addr)-4:]
	}

	if w.Label == "" {
		return short
	}

	return w.Label + " · " + short
}

// Notifications.

// notifications shows the switches, after flipping toggle if it names one.
func (s *Service) notifications(ctx context.Context, user domain.User, toggle string) screen {
	prefs, err := s.menu.Notifications.Prefs(ctx, principal(user))
	if err != nil {
		return s.trouble(ctx, err)
	}

	if toggle != "" {
		for i, p := range prefs {
			if p.Key != toggle {
				continue
			}

			if prefs[i], err = s.menu.Notifications.SetPref(ctx, principal(user), p.Key, !p.Enabled); err != nil {
				return s.trouble(ctx, err)
			}
		}
	}

	rows := make([][]ports.TelegramButton, 0, len(prefs)+1)

	for _, p := range prefs {
		mark := "⬜"
		if p.Enabled {
			mark = "✅"
		}

		rows = append(rows, []ports.TelegramButton{{Text: mark + " " + p.Label, Data: dataNotify + p.Key}})
	}

	rows = append(rows, unlinkButton[0])

	return screen{html: "<b>Notifications</b>\nWhat Polythought tells you about here. Tap one to switch it.", buttons: rows}
}

// Helpers.

func (s *Service) trouble(ctx context.Context, err error) screen {
	s.log.Warn(ctx, "Telegram menu failed", "error", err.Error())
	return screen{html: textFailed}
}

func (s *Service) ago(t time.Time) string {
	d := s.now().Sub(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	}
}

func statusMark(st domain.ExecutionStatus) string {
	switch st {
	case domain.ExecutionSucceeded:
		return "✅"
	case domain.ExecutionFailed:
		return "❌"
	case domain.ExecutionCancelled:
		return "⏹"
	default:
		return "⏳"
	}
}

func roundDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}

	return d.Round(100 * time.Millisecond).String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}

	return many
}

func truncate(s string, most int) string {
	if utf8.RuneCountInString(s) <= most {
		return s
	}

	return string([]rune(s)[:most-1]) + "…"
}
