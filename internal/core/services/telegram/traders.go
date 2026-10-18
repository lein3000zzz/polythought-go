package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

// The Traders section (D109): the workflows that follow traders, each one's traders with
// what copying them made, and a trader's own screen, where they are switched on or off.

// Trading is what the menu needs from the trading service.
type Trading interface {
	Summary(ctx context.Context, p auth.Principal, id domain.WorkflowID) (trading.Summary, error)
}

const (
	sectionTraders = "👥 Traders"

	// tradingFlowsLooked is how many workflows the section looks through for those that
	// follow traders.
	tradingFlowsLooked = 100
	tradingFlowsShown  = 20

	dataTraderFlows  = "tf:" // tf: the workflows that follow traders
	dataTraderFlow   = "tw:" // tw:<workflow id>: its traders
	dataTrader       = "tt:" // tt:<workflow id>:<row>:<address start>: one trader
	dataTraderSwitch = "ts:" // ts:<workflow id>:<row>:<address start>:<1 or 0>: switch them

	// addressMark is how much of a trader's address a button carries, so a press finds
	// the same trader even if the table changed meanwhile.
	addressMark = 10
)

// followed is a row of a Wallet activity trigger's Traders table: the node, its row.
type followed struct {
	node    int // in the workflow's nodes
	row     int
	label   string
	address string
	on      bool
}

// followedIn lists the traders a workflow follows, every Wallet activity trigger's, a
// wallet once, as the trigger counts them.
func followedIn(w domain.Workflow) []followed {
	var (
		out  []followed
		seen = map[string]bool{}
	)

	for i, n := range w.Nodes {
		if n.Type != triggers.WalletActivityType {
			continue
		}

		rows, _ := n.Params["traders"].([]any)
		for r, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				continue
			}

			address, _ := row["address"].(string)
			key := strings.ToLower(address)

			if address == "" || seen[key] {
				continue
			}

			seen[key] = true

			label, _ := row["label"].(string)
			on, isBool := row["on"].(bool)

			out = append(out, followed{node: i, row: r, label: label, address: address, on: on || !isBool})
		}
	}

	return out
}

func (s *Service) traderFlows(ctx context.Context, user domain.User) screen {
	if s.menu.Trading == nil {
		return screen{html: "This part of the menu is not available."}
	}

	page, err := s.menu.Workflows.List(ctx, principal(user), ports.PageFilter{Limit: tradingFlowsLooked})
	if err != nil {
		return s.trouble(ctx, err)
	}

	var rows [][]ports.TelegramButton

	for _, w := range page.Items {
		traders := followedIn(w)
		if len(traders) == 0 || len(rows) == tradingFlowsShown {
			continue
		}

		on := 0
		for _, t := range traders {
			if t.on {
				on++
			}
		}

		mark := "○"
		if w.Active {
			mark = "●"
		}

		rows = append(rows, []ports.TelegramButton{{
			Text: fmt.Sprintf("%s %s · %d of %d on", mark, w.Name, on, len(traders)),
			Data: dataTraderFlow + string(w.ID),
		}})
	}

	if len(rows) == 0 {
		return screen{html: "<b>Traders</b>\n\nNo workflow follows traders yet: add Wallet activity to one in Polythought."}
	}

	return screen{
		html:    "<b>Traders</b>\nThe workflows that follow traders. Pick one to see each trader's results, and switch them on or off.",
		buttons: rows,
	}
}

func (s *Service) traderPress(ctx context.Context, user domain.User, kind, arg string) screen {
	switch kind {
	case dataTraderFlow:
		return s.traderFlow(ctx, user, domain.WorkflowID(arg), "")
	case dataTrader:
		return s.trader(ctx, user, arg, "")
	default:
		return s.switchTrader(ctx, user, arg)
	}
}

// traderFlow is a workflow's traders, each with what copying them came to.
func (s *Service) traderFlow(ctx context.Context, user domain.User, id domain.WorkflowID, note string) screen {
	back := ports.TelegramButton{Text: "« Traders", Data: dataTraderFlows}

	w, sum, err := s.followedFlow(ctx, user, id)
	if errors.Is(err, domain.ErrNotFound) {
		return screen{html: "This workflow no longer exists.", buttons: [][]ports.TelegramButton{{back}}}
	}

	if err != nil {
		return s.trouble(ctx, err)
	}

	var b strings.Builder

	if note != "" {
		b.WriteString(note + "\n\n")
	}

	fmt.Fprintf(&b, "<b>%s</b>", html.EscapeString(w.Name))

	if a := sum.Account; a.Open {
		fmt.Fprintf(&b, " · paper, worth %s (%s)", dollars(a.Worth), signed(a.Profit))
	}

	traders := followedIn(w)
	results := byTrader(sum)

	if len(traders) == 0 {
		b.WriteString("\nIt follows no traders now.")
	}

	var buttons []ports.TelegramButton

	for i, t := range traders {
		fmt.Fprintf(&b, "\n%s %s  %s", onMark(t.on), html.EscapeString(traderLabel(t)), resultLine(results[strings.ToLower(t.address)]))
		buttons = append(buttons, ports.TelegramButton{Text: onMark(t.on) + " " + traderLabel(t), Data: traderData(dataTrader, id, i, t)})
	}

	rows := [][]ports.TelegramButton{}
	for i := 0; i < len(buttons); i += 2 {
		rows = append(rows, buttons[i:min(i+2, len(buttons))])
	}

	rows = append(rows, []ports.TelegramButton{{Text: "↻ Refresh", Data: dataTraderFlow + string(id)}, back})

	return screen{html: b.String(), buttons: rows}
}

// trader is one trader's screen in a workflow, with Switch on or off.
func (s *Service) trader(ctx context.Context, user domain.User, arg, note string) screen {
	id, i, mark, _, ok := traderArg(arg)
	if !ok {
		return screen{html: textStale}
	}

	back := ports.TelegramButton{Text: "« Back", Data: dataTraderFlow + string(id)}

	w, sum, err := s.followedFlow(ctx, user, id)
	if errors.Is(err, domain.ErrNotFound) {
		return screen{html: "This workflow no longer exists.", buttons: [][]ports.TelegramButton{{{Text: "« Traders", Data: dataTraderFlows}}}}
	}

	if err != nil {
		return s.trouble(ctx, err)
	}

	t, found := findFollowed(w, i, mark)
	if !found {
		return screen{html: "This trader is no longer in the workflow's table.", buttons: [][]ports.TelegramButton{{back}}}
	}

	r := byTrader(sum)[strings.ToLower(t.address)]

	var b strings.Builder

	if note != "" {
		b.WriteString(note + "\n\n")
	}

	state := "off"
	if t.on {
		state = "on"
	}

	fmt.Fprintf(&b, "<b>%s</b> (%s) in %s: %s\n", html.EscapeString(traderLabel(t)), shortAddress(t.address),
		html.EscapeString(w.Name), state)

	if r.Copies == 0 && r.Closed == 0 && r.Unrealized.IsZero() {
		b.WriteString("No copies yet.")
	} else {
		fmt.Fprintf(&b, "%d %s, %s traded\n%s realized, %s open", r.Copies, plural(r.Copies, "copy", "copies"),
			dollars(r.Volume), signed(r.Realized), signed(r.Unrealized))

		if r.Closed > 0 {
			fmt.Fprintf(&b, ", %d of %d won", r.Wins, r.Closed)
		}
	}

	toggle := ports.TelegramButton{Text: "⏻ Switch on", Data: traderData(dataTraderSwitch, id, i, t) + ":1"}
	if t.on {
		toggle = ports.TelegramButton{Text: "⏻ Switch off", Data: traderData(dataTraderSwitch, id, i, t) + ":0"}
	}

	back.Text = "« " + w.Name

	return screen{html: b.String(), buttons: [][]ports.TelegramButton{{toggle, back}}}
}

// switchTrader sets a trader's On in the workflow's table and saves it as a new version
// (D108); the trigger picks it up at once.
func (s *Service) switchTrader(ctx context.Context, user domain.User, arg string) screen {
	id, i, mark, on, ok := traderArg(arg)
	if !ok || on == nil {
		return screen{html: textStale}
	}

	short := strings.Join(strings.Split(arg, ":")[:3], ":") // the trader's screen

	// A save in between is retried once, on the version it made.
	for try := 0; ; try++ {
		w, err := s.menu.Workflows.Get(ctx, principal(user), id)
		if err != nil {
			return s.trader(ctx, user, short, "Could not switch them: "+html.EscapeString(truncate(err.Error(), maxNoteText)))
		}

		t, found := findFollowed(w, i, mark)
		if !found {
			return s.trader(ctx, user, short, "")
		}

		if t.on == *on {
			return s.trader(ctx, user, short, "")
		}

		_, err = s.menu.Workflows.Update(ctx, principal(user), id, w.Version,
			workflows.Definition{Name: w.Name, Nodes: switched(w.Nodes, t, *on), Connections: w.Connections})
		if errors.Is(err, domain.ErrConflict) && try == 0 {
			continue
		}

		if errors.Is(err, domain.ErrConflict) {
			return s.trader(ctx, user, short, "Could not switch them: the workflow was being saved meanwhile. Try again.")
		}

		if err != nil {
			return s.trader(ctx, user, short, "Could not switch them: "+html.EscapeString(truncate(err.Error(), maxNoteText)))
		}

		s.log.Info(ctx, "Trader switched from Telegram", "user_id", user.ID, "workflow_id", id, "trader", t.address, "on", *on)

		note := "Switched on: their trades are copied again."
		if !*on {
			note = "Switched off: their trades are no longer copied."
		}

		return s.trader(ctx, user, short, note)
	}
}

// switched is the nodes with the trader's On set, copied where it changes.
func switched(nodes []domain.NodeSpec, t followed, on bool) []domain.NodeSpec {
	nodes = slices.Clone(nodes)
	n := &nodes[t.node]
	n.Params = maps.Clone(n.Params)

	rows := slices.Clone(n.Params["traders"].([]any))
	row := maps.Clone(rows[t.row].(map[string]any))
	row["on"] = on
	rows[t.row] = row
	n.Params["traders"] = rows

	return nodes
}

func (s *Service) followedFlow(ctx context.Context, user domain.User, id domain.WorkflowID) (domain.Workflow, trading.Summary, error) {
	w, err := s.menu.Workflows.Get(ctx, principal(user), id)
	if err != nil {
		return domain.Workflow{}, trading.Summary{}, err
	}

	sum, err := s.menu.Trading.Summary(ctx, principal(user), id)

	return w, sum, err
}

// findFollowed is the trader at i, if their address still starts as the button said.
func findFollowed(w domain.Workflow, i int, mark string) (followed, bool) {
	traders := followedIn(w)
	if i < 0 || i >= len(traders) || !strings.EqualFold(addressStart(traders[i].address), mark) {
		return followed{}, false
	}

	return traders[i], true
}

func byTrader(sum trading.Summary) map[string]trading.TraderSummary {
	out := make(map[string]trading.TraderSummary, len(sum.Traders))
	for _, t := range sum.Traders {
		out[strings.ToLower(string(t.Trader))] = t
	}

	return out
}

func resultLine(r trading.TraderSummary) string {
	if r.Copies == 0 && r.Closed == 0 && r.Unrealized.IsZero() {
		return "no copies yet"
	}

	line := signed(r.Realized) + " realized, " + signed(r.Unrealized) + " open"
	if r.Closed > 0 {
		line += fmt.Sprintf(", %d of %d", r.Wins, r.Closed)
	}

	return line
}

func traderData(prefix string, id domain.WorkflowID, i int, t followed) string {
	return prefix + string(id) + ":" + strconv.Itoa(i) + ":" + addressStart(t.address)
}

// traderArg reads <workflow id>:<row>:<address start>[:<1 or 0>].
func traderArg(arg string) (id domain.WorkflowID, i int, mark string, on *bool, ok bool) {
	parts := strings.Split(arg, ":")
	if len(parts) < 3 || len(parts) > 4 {
		return "", 0, "", nil, false
	}

	i, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, "", nil, false
	}

	if len(parts) == 4 {
		v := parts[3] == "1"
		on = &v
	}

	return domain.WorkflowID(parts[0]), i, parts[2], on, true
}

func addressStart(address string) string {
	return strings.ToLower(address[:min(addressMark, len(address))])
}

func traderLabel(t followed) string {
	if t.label != "" {
		return t.label
	}

	return shortAddress(t.address)
}

func onMark(on bool) string {
	if on {
		return "●"
	}

	return "○"
}

func shortAddress(a string) string {
	if len(a) < 10 {
		return a
	}

	return a[:6] + "…" + a[len(a)-4:]
}

func dollars(d decimal.Decimal) string {
	return "$" + d.StringFixed(2)
}

// signed is a gain or a loss: +$8.10, −$1.20.
func signed(d decimal.Decimal) string {
	switch {
	case d.IsPositive():
		return "+" + dollars(d)
	case d.IsNegative():
		return "−" + dollars(d.Neg())
	default:
		return dollars(d)
	}
}
