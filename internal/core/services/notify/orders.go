package notify

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Order notifications (D229 to D232): what happens to a user's orders, live and paper, a
// kind each, as they chose; at most orderCap of a kind an hour, then one summary as the
// hour ends.

var _ ports.OrderNotifier = (*Service)(nil)

const (
	orderCap       = 20
	orderWindow    = time.Hour
	orderQueueSize = 1024
)

const capNote = " At most 20 an hour, then one message with the rest."

var (
	LiveOrderFilled = Kind{
		Key: "live_order_filled", Group: groupLive, Label: "A live order fills",
		Description: "A live order of one of your workflows filled, in part or wholly: as it matched, or later as it rested on the book." + capNote,
		Default:     true,
	}
	LiveOrderRefused = Kind{
		Key: "live_order_refused", Group: groupLive, Label: "A live order is refused",
		Description: "A live order was refused, before it was sent or by Polymarket: your kill switch, a cap, too few shares, Polymarket's reasons." + capNote,
		Default:     true,
	}
	LiveOrderUnfilled = Kind{
		Key: "live_order_unfilled", Group: groupLive, Label: "A live order goes unfilled",
		Description: "A live FAK or FOK the book could not fill within its price." + capNote,
	}
	LiveOrderRests = Kind{
		Key: "live_order_rests", Group: groupLive, Label: "A live order rests on the book",
		Description: "A live GTC or GTD came to rest on Polymarket's book; its fills tell you as A live order fills does." + capNote,
	}
	LiveOrderEnded = Kind{
		Key: "live_order_ended", Group: groupLive, Label: "A live order is cancelled or expires",
		Description: "What was left of a resting live order was cancelled, or a GTD's time came." + capNote,
	}

	PaperOrderFilled = Kind{
		Key: "paper_order_filled", Group: groupPaper, Label: "A paper order fills",
		Description: "A paper order of one of your workflows filled, in part or wholly." + capNote,
	}
	PaperOrderRefused = Kind{
		Key: "paper_order_refused", Group: groupPaper, Label: "A paper order is refused",
		Description: "A paper order was refused: too little paper cash, too few shares, the market's rules." + capNote,
	}
	PaperOrderUnfilled = Kind{
		Key: "paper_order_unfilled", Group: groupPaper, Label: "A paper order goes unfilled",
		Description: "A paper FAK or FOK the book could not fill within its price." + capNote,
	}
	PaperOrderRests = Kind{
		Key: "paper_order_rests", Group: groupPaper, Label: "A paper order rests on the book",
		Description: "A paper GTC or GTD came to rest, waiting for the market to trade through it." + capNote,
	}
	PaperOrderEnded = Kind{
		Key: "paper_order_ended", Group: groupPaper, Label: "A paper order is cancelled or expires",
		Description: "What was left of a resting paper order was cancelled, or a GTD's time came." + capNote,
	}
)

// orderKinds are the kinds of each event, live and paper.
var orderKinds = map[domain.OrderEventKind][2]Kind{
	domain.OrderEventFilled:   {LiveOrderFilled, PaperOrderFilled},
	domain.OrderEventRefused:  {LiveOrderRefused, PaperOrderRefused},
	domain.OrderEventUnfilled: {LiveOrderUnfilled, PaperOrderUnfilled},
	domain.OrderEventRested:   {LiveOrderRests, PaperOrderRests},
	domain.OrderEventCanceled: {LiveOrderEnded, PaperOrderEnded},
	domain.OrderEventExpired:  {LiveOrderEnded, PaperOrderEnded},
}

// held says what the summary of a kind's held notifications counts.
var held = map[string]string{
	LiveOrderFilled.Key: "live orders filled", PaperOrderFilled.Key: "paper orders filled",
	LiveOrderRefused.Key: "live orders were refused", PaperOrderRefused.Key: "paper orders were refused",
	LiveOrderUnfilled.Key: "live orders went unfilled", PaperOrderUnfilled.Key: "paper orders went unfilled",
	LiveOrderRests.Key: "live orders came to rest", PaperOrderRests.Key: "paper orders came to rest",
	LiveOrderEnded.Key: "live orders were cancelled or expired", PaperOrderEnded.Key: "paper orders were cancelled or expired",
}

func kindOf(e domain.OrderEvent) (Kind, bool) {
	kinds, ok := orderKinds[e.Kind]
	if !ok {
		return Kind{}, false
	}

	if e.Order.Mode == domain.ModeLive {
		return kinds[0], true
	}

	return kinds[1], true
}

type windowKey struct {
	user domain.UserID
	kind string
}

// window is an hour of a kind's notifications to a user: how many went, and those held
// past the cap, by workflow.
type window struct {
	start time.Time
	sent  int
	held  map[string]int
}

// OrderHappened queues what happened to an order, to tell its owner. It never blocks:
// when the queue is full, it goes untold.
func (s *Service) OrderHappened(e domain.OrderEvent) {
	select {
	case s.orders <- e:
	default:
		s.log.Warn(context.Background(), "Order notification dropped: too many at once", "order_id", e.Order.ID, "kind", e.Kind)
	}
}

func (s *Service) orderHappened(ctx context.Context, e domain.OrderEvent) {
	k, ok := kindOf(e)
	if !ok {
		return
	}

	user := e.Order.OwnerID

	telegram, mail, err := s.channels(ctx, user, k)
	if err != nil {
		s.log.Warn(ctx, "Order notification skipped", "order_id", e.Order.ID, logging.Err(err))
		return
	}

	if !telegram && !mail {
		return
	}

	// An hour gone by, what was held of it is told first.
	s.summarise(ctx)

	name, gone := s.workflowName(ctx, user, e.Order.WorkflowID)
	if !s.admit(user, k, name) {
		return
	}

	m := s.describe(ctx, e, name)
	link, linkText := s.publicURL+"/workflows/"+string(e.Order.WorkflowID), "Open the workflow"

	if gone {
		link, linkText = s.publicURL+"/orders", "Open live orders"
	}

	if mail {
		_ = s.mailUser(ctx, user, k, ports.UserMail{Subject: m.subject, Paragraphs: []string{m.plain}, LinkText: linkText, LinkURL: link})
	}

	if telegram {
		var buttons [][]ports.TelegramButton
		if strings.HasPrefix(s.publicURL, "https://") {
			buttons = [][]ports.TelegramButton{{{Text: linkText, URL: link}}}
		}

		s.message(ctx, user, ports.UserMessage{HTML: m.html, Buttons: buttons})
	}
}

// admit counts a notification of k to user in its hour: past the cap, it is held, under
// its workflow's name.
func (s *Service) admit(user domain.UserID, k Kind, workflow string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := windowKey{user: user, kind: k.Key}

	w, ok := s.windows[key]
	if !ok {
		w = &window{start: s.now(), held: map[string]int{}}
		s.windows[key] = w
	}

	if w.sent < orderCap {
		w.sent++
		return true
	}

	w.held[workflow]++

	return false
}

// summarise tells what was held of the hours that ended (D231): how many, for which
// workflows.
func (s *Service) summarise(ctx context.Context) {
	type due struct {
		key  windowKey
		held map[string]int
	}

	var ended []due

	s.mu.Lock()
	for key, w := range s.windows {
		if s.now().Sub(w.start) < orderWindow {
			continue
		}

		delete(s.windows, key)

		if len(w.held) > 0 {
			ended = append(ended, due{key: key, held: w.held})
		}
	}
	s.mu.Unlock()

	for _, d := range ended {
		k, ok := kindByKey(d.key.kind)
		if !ok {
			continue
		}

		telegram, mail, err := s.channels(ctx, d.key.user, k)
		if err != nil || (!telegram && !mail) {
			continue
		}

		total := 0
		names := slices.Sorted(maps.Keys(d.held))
		parts := make([]string, len(names))

		for i, n := range names {
			total += d.held[n]
			parts[i] = fmt.Sprintf("%s (%d)", n, d.held[n])
		}

		text := fmt.Sprintf("%d more %s in the last hour, past the %d told one by one: %s.", total, held[k.Key], orderCap, strings.Join(parts, ", "))

		if mail {
			_ = s.mailUser(ctx, d.key.user, k, ports.UserMail{
				Subject: fmt.Sprintf("%d more %s", total, held[k.Key]), Paragraphs: []string{text},
				LinkText: "Open live orders", LinkURL: s.publicURL + "/orders",
			})
		}

		if telegram {
			s.message(ctx, d.key.user, ports.UserMessage{HTML: "🔔 " + escape(text)})
		}
	}
}

func kindByKey(key string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Key == key {
			return k, true
		}
	}

	return Kind{}, false
}

func (s *Service) message(ctx context.Context, user domain.UserID, m ports.UserMessage) {
	err := s.messenger.MessageUser(ctx, user, m)

	switch {
	case err == nil:
	case errors.Is(err, domain.ErrTelegramNotLinked), errors.Is(err, domain.ErrTelegramUnavailable):
		s.log.Debug(ctx, "Order notification not sent: nowhere to send it", "user_id", user)
	default:
		s.log.Warn(ctx, "Order notification not sent", "user_id", user, logging.Err(err))
	}
}

// workflowName is the workflow's name, and whether it is gone.
func (s *Service) workflowName(ctx context.Context, user domain.UserID, id domain.WorkflowID) (string, bool) {
	w, err := s.workflows.Get(ctx, user, id)
	if err != nil {
		return "a deleted workflow", errors.Is(err, domain.ErrNotFound)
	}

	return w.Name, false
}

// told is a notification as each channel has it.
type told struct {
	subject, plain, html string
}

// describe says what happened to the order, in a sentence: Live: Copy A bought 10 shares
// of Yes in “Will it rain?” at 52¢ for $5.20.
func (s *Service) describe(ctx context.Context, e domain.OrderEvent, workflow string) told {
	o := e.Order

	mode := "Paper"
	if o.Mode == domain.ModeLive {
		mode = "Live"
	}

	market := s.marketOf(ctx, o)
	copying := ""

	if o.TraderLabel != "" {
		copying = " Copying " + o.TraderLabel + "."
	}

	// say writes the sentence for each channel, the workflow's name in bold on Telegram.
	say := func(icon, subject string, sentence func(who string) string) told {
		return told{
			subject: mode + " order of " + workflow + " " + subject,
			plain:   mode + ": " + sentence(workflow) + copying,
			html:    icon + " " + mode + ": " + escapedWith(sentence, workflow) + escape(copying),
		}
	}

	why := strings.TrimSuffix(o.Reason, ".")

	switch e.Kind {
	case domain.OrderEventFilled:
		shares, dollars, fees := totals(e.Fills)
		verb := "bought"
		if o.Side == domain.SideSell {
			verb = "sold"
		}

		return say("✅", "filled", func(who string) string {
			line := fmt.Sprintf("%s %s %s shares%s at %s for $%s", who, verb, shares, market, cents(dollars.Div(shares)), dollars.StringFixed(2))
			if fees.IsPositive() {
				line += fmt.Sprintf(", $%s in fees", fees.StringFixed(2))
			}

			return line + "."
		})
	case domain.OrderEventRefused:
		return say("⛔", "refused", func(who string) string { return fmt.Sprintf("%s's %s was refused: %s.", who, what(o, market), why) })
	case domain.OrderEventUnfilled:
		return say("◽", "unfilled", func(who string) string { return fmt.Sprintf("%s's %s went unfilled: %s.", who, what(o, market), why) })
	case domain.OrderEventRested:
		return say("📌", "rests on the book", func(who string) string { return fmt.Sprintf("%s's %s rests on the book.", who, what(o, market)) })
	case domain.OrderEventExpired:
		return say("⌛", "expired", func(who string) string { return fmt.Sprintf("%s's %s expired: %s.", who, what(o, market), why) })
	default:
		return say("🚫", "cancelled", func(who string) string { return fmt.Sprintf("%s's %s was cancelled: %s.", who, what(o, market), why) })
	}
}

// escape is text as Telegram's HTML takes it: only &, < and > need it.
var escape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace

// escapedWith escapes the sentence, with who in bold.
func escapedWith(sentence func(who string) string, who string) string {
	const mark = "\x00"

	return strings.Replace(escape(sentence(mark)), mark, "<b>"+escape(who)+"</b>", 1)
}

// what an order is: a buy of 10 shares of Yes in “Will it rain?” at 52¢, a buy of $5.00.
func what(o domain.PlacedOrder, market string) string {
	size := o.Shares.String() + " shares"
	if o.Dollars.IsPositive() {
		size = "$" + o.Dollars.StringFixed(2)
	}

	return fmt.Sprintf("%s of %s%s at %s", o.Side, size, market, cents(o.Price))
}

// marketOf names the order's outcome and market, " of Yes in “Will it rain?”", or
// nothing when it is not known.
func (s *Service) marketOf(ctx context.Context, o domain.PlacedOrder) string {
	if s.markets == nil || o.MarketID == "" {
		return ""
	}

	m, err := s.markets.GetMarket(ctx, o.MarketID)
	if err != nil {
		s.log.Debug(ctx, "Order's market not named", "market_id", o.MarketID, logging.Err(err))
		return ""
	}

	if out, ok := m.Outcome(o.TokenID); ok && out.Label != "" {
		return " of " + out.Label + " in “" + m.Question + "”"
	}

	return " in “" + m.Question + "”"
}

func totals(fills []domain.OrderFill) (shares, dollars, fees decimal.Decimal) {
	for _, f := range fills {
		shares, dollars, fees = shares.Add(f.Shares), dollars.Add(f.Dollars), fees.Add(f.Fee)
	}

	return shares, dollars, fees
}

// cents writes a price per share as Polymarket does: 52¢, 52.5¢.
func cents(p decimal.Decimal) string {
	return p.Mul(decimal.NewFromInt(100)).Round(2).String() + "¢"
}
