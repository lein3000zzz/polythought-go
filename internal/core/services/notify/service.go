// Package notify tells users about what happened while they were away, wherever they get
// messages: for now, their linked Telegram chat. Each kind of notification can be
// switched off.
package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Kind is a kind of notification a user can switch on or off, under a group of kinds.
type Kind struct {
	Key         string
	Group       string
	Label       string
	Description string
	Default     bool
}

// The groups Settings shows the kinds under.
const (
	groupWorkflows = "Workflows"
	groupWallets   = "Wallets"
	groupLive      = "Live orders"
	groupPaper     = "Paper orders"
	groupAgents    = "AI agents"
)

var WorkflowFailed = Kind{
	Key: "workflow_failed", Group: groupWorkflows,
	Label: "A workflow fails",
	Description: "A run of one of your workflows failed. At most once an hour for each workflow, " +
		"and never for runs you start by hand.",
	Default: true,
}

var ApprovalWaiting = Kind{
	Key: "approval_waiting", Group: groupWorkflows,
	Label: "A question waits for your answer",
	Description: "A Telegram question from one of your workflows is still unanswered with a quarter of its time left. " +
		"Once per question, for questions open 10 minutes or more.",
	Default: true,
}

var SessionKeyExpiring = Kind{
	Key: "session_key_expiring", Group: groupWallets,
	Label: "A session key is about to expire",
	Description: "A wallet's session key expires in 14 days or less: authorise a new one before then, so the wallet keeps " +
		"trading. Once per key.",
	Default: true,
}

var LiveTradeFailed = Kind{
	Key: "live_trade_failed", Group: groupLive,
	Label: "A live trade fails",
	Description: "A live order matched on Polymarket, but its trade failed on chain: the workflow's books undo it. " +
		"Once per order.",
	Default: true,
}

var AgentSpendWarned = Kind{
	Key: "agent_spend_80", Group: groupAgents,
	Label: "Agent spend reaches 80% of a monthly cap",
	Description: "Your AI agent nodes have used 80% of one of their caps this month, in dollars or tokens. " +
		"Once a month, and again only if the cap is raised.",
	Default: true,
}

var AgentSpendCapped = Kind{
	Key: "agent_spend_cap", Group: groupAgents,
	Label: "Agent spend reaches a monthly cap",
	Description: "A call of your AI agent nodes was refused: one of their caps this month is reached, so their " +
		"items fail until the month ends or an admin raises it. Once a month, and again only if the cap is raised.",
	Default: true,
}

// Kinds lists every kind, in the order Settings shows them.
var Kinds = []Kind{
	WorkflowFailed, ApprovalWaiting, SessionKeyExpiring, LiveTradeFailed,
	LiveOrderFilled, LiveOrderRefused, LiveOrderUnfilled, LiveOrderRests, LiveOrderEnded,
	PaperOrderFilled, PaperOrderRefused, PaperOrderUnfilled, PaperOrderRests, PaperOrderEnded,
	AgentSpendWarned, AgentSpendCapped,
}

const (
	failureCooldown = time.Hour
	queueSize       = 256
	maxErrorText    = 300
	maxCooldowns    = 4096
)

var (
	_ ports.ExecutionNotifier  = (*Service)(nil)
	_ ports.QuestionReminder   = (*Service)(nil)
	_ ports.SessionKeyReminder = (*Service)(nil)
	_ ports.LiveTradeNotifier  = (*Service)(nil)
)

type Service struct {
	messenger ports.UserMessenger
	mail      ports.NotificationMail // optional: without it, nothing goes by mail
	prefs     ports.NotificationPrefRepository
	workflows ports.WorkflowRepository
	publicURL string
	log       logging.Logger
	now       func() time.Time

	queue chan failure

	markets ports.MarketReader // optional: an order's market, for what it says
	orders  chan domain.OrderEvent
	spends  chan spendAlert
	tick    time.Duration // how often held order notifications are looked at

	mu        sync.Mutex
	cooldowns map[domain.WorkflowID]cooldown
	windows   map[windowKey]*window // order notifications sent and held this hour (D231)
}

type failure struct {
	owner domain.UserID
	exec  domain.Execution
}

// cooldown is when a workflow's failure was last told, and how many were not since.
type cooldown struct {
	at     time.Time
	missed int
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithMarkets has order notifications name their market and outcome.
func WithMarkets(m ports.MarketReader) Option {
	return func(s *Service) { s.markets = m }
}

// WithTick replaces how often held order notifications are looked at, for tests.
func WithTick(every time.Duration) Option {
	return func(s *Service) { s.tick = every }
}

// WithMail sends notifications by mail too, to users who choose it per kind (D170).
func WithMail(m ports.NotificationMail) Option {
	return func(s *Service) { s.mail = m }
}

// emailPref is the key a kind's email choice is kept under, off unless set (D170).
func emailPref(k Kind) string {
	return k.Key + ".email"
}

// New makes the service. publicURL is where users open the app; messages link to it
// when it is https, which Telegram requires of link buttons.
func New(messenger ports.UserMessenger, prefs ports.NotificationPrefRepository, workflows ports.WorkflowRepository,
	publicURL string, log logging.Logger, opts ...Option,
) *Service {
	s := &Service{
		messenger: messenger,
		prefs:     prefs,
		workflows: workflows,
		publicURL: strings.TrimRight(publicURL, "/"),
		log:       log.With("component", "notify"),
		now:       time.Now,
		queue:     make(chan failure, queueSize),
		cooldowns: make(map[domain.WorkflowID]cooldown),
		orders:    make(chan domain.OrderEvent, orderQueueSize),
		spends:    make(chan spendAlert, queueSize),
		tick:      time.Minute,
		windows:   make(map[windowKey]*window),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Pref is a kind of notification and how the user gets it: Enabled on Telegram, Email by
// mail.
type Pref struct {
	Kind

	Enabled bool
	Email   bool
}

func (s *Service) Prefs(ctx context.Context, p auth.Principal) ([]Pref, error) {
	changed, err := s.prefs.Prefs(ctx, p.User.ID)
	if err != nil {
		return nil, err
	}

	out := make([]Pref, len(Kinds))
	for i, k := range Kinds {
		out[i] = prefOf(k, changed)
	}

	return out, nil
}

func prefOf(k Kind, changed map[string]bool) Pref {
	enabled, ok := changed[k.Key]
	if !ok {
		enabled = k.Default
	}

	return Pref{Kind: k, Enabled: enabled, Email: changed[emailPref(k)]}
}

// SetPref switches a kind on Telegram on or off.
func (s *Service) SetPref(ctx context.Context, p auth.Principal, key string, enabled bool) (Pref, error) {
	return s.set(ctx, p, key, func(k Kind) string { return k.Key }, enabled)
}

// SetEmailPref switches a kind by mail on or off.
func (s *Service) SetEmailPref(ctx context.Context, p auth.Principal, key string, enabled bool) (Pref, error) {
	return s.set(ctx, p, key, emailPref, enabled)
}

func (s *Service) set(ctx context.Context, p auth.Principal, key string, prefKey func(Kind) string, enabled bool) (Pref, error) {
	for _, k := range Kinds {
		if k.Key != key {
			continue
		}

		if err := s.prefs.SetPref(ctx, p.User.ID, prefKey(k), enabled); err != nil {
			return Pref{}, err
		}

		changed, err := s.prefs.Prefs(ctx, p.User.ID)
		if err != nil {
			return Pref{}, err
		}

		return prefOf(k, changed), nil
	}

	return Pref{}, fmt.Errorf("%w: no kind of notification %q", domain.ErrNotFound, key)
}

// MailOn says whether notifications can go by mail now: switched on, and mail set up
// (D184).
func (s *Service) MailOn() bool {
	return s.mail != nil && s.mail.On()
}

// channels says how user gets k: on Telegram, by mail.
func (s *Service) channels(ctx context.Context, user domain.UserID, k Kind) (telegram, mail bool, err error) {
	changed, err := s.prefs.Prefs(ctx, user)
	if err != nil {
		return false, false, err
	}

	p := prefOf(k, changed)

	return p.Enabled, p.Email && s.MailOn(), nil
}

// mailUser mails user a notification of k, saying where to turn it off, and logs what
// went wrong.
func (s *Service) mailUser(ctx context.Context, user domain.UserID, k Kind, m ports.UserMail) error {
	m.Paragraphs = append(m.Paragraphs, "You get this mail as “"+k.Label+"” is on by email in Settings → Notifications, "+
		"where you can turn it off: "+s.publicURL+"/settings.")

	if err := s.mail.MailUser(ctx, user, m); err != nil {
		s.log.Warn(ctx, "Notification not mailed", "user_id", user, "kind", k.Key, logging.Err(err))
		return err
	}

	return nil
}

// ExecutionFinished queues failed executions to tell their owners about. It never
// blocks the execution: when the queue is full, the failure goes untold.
func (s *Service) ExecutionFinished(owner domain.UserID, e domain.Execution) {
	if e.Status != domain.ExecutionFailed {
		return
	}

	select {
	case s.queue <- failure{owner: owner, exec: e}:
	default:
		s.log.Warn(context.Background(), "Failure notification dropped: too many at once", "workflow_id", e.WorkflowID)
	}
}

// Run sends the queued notifications until ctx is done, and the summaries of the order
// notifications held (D231) as their hour ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(s.tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case f := <-s.queue:
			s.workflowFailed(ctx, f)
		case e := <-s.orders:
			s.orderHappened(ctx, e)
		case a := <-s.spends:
			s.agentSpend(ctx, a)
		case <-t.C:
			s.summarise(ctx)
		}
	}
}

func (s *Service) workflowFailed(ctx context.Context, f failure) {
	w, err := s.workflows.Get(ctx, f.owner, f.exec.WorkflowID)
	if errors.Is(err, domain.ErrNotFound) {
		return // deleted since
	}

	if err != nil {
		s.log.Warn(ctx, "Failure notification skipped", "workflow_id", f.exec.WorkflowID, logging.Err(err))
		return
	}

	// Whoever started it by hand is looking at the result.
	if trigger, ok := w.Node(f.exec.TriggeredBy); ok && trigger.Type == triggers.ManualType {
		return
	}

	telegram, mail, err := s.channels(ctx, f.owner, WorkflowFailed)
	if err != nil || (!telegram && !mail) {
		return
	}

	// One cooldown for both channels.
	missed, tell := s.due(w.ID)
	if !tell {
		return
	}

	var again string
	if missed > 0 {
		again = fmt.Sprintf(" It failed %d more %s since the last message.", missed, plural(missed, "time", "times"))
	}

	if mail {
		m := ports.UserMail{
			Subject:    w.Name + " failed",
			Paragraphs: []string{w.Name + " failed." + again},
			LinkText:   "Open workflow", LinkURL: s.publicURL + "/workflows/" + string(w.ID),
		}
		if f.exec.Error != "" {
			m.Paragraphs = append(m.Paragraphs, "The error: "+truncate(f.exec.Error, maxErrorText))
		}

		_ = s.mailUser(ctx, f.owner, WorkflowFailed, m)
	}

	if !telegram {
		return
	}

	text := "⚠️ <b>" + html.EscapeString(w.Name) + "</b> failed." + again

	if f.exec.Error != "" {
		text += "\n<code>" + html.EscapeString(truncate(f.exec.Error, maxErrorText)) + "</code>"
	}

	var buttons [][]ports.TelegramButton
	if strings.HasPrefix(s.publicURL, "https://") {
		buttons = [][]ports.TelegramButton{{{Text: "Open workflow", URL: s.publicURL + "/workflows/" + string(w.ID)}}}
	}

	err = s.messenger.MessageUser(ctx, f.owner, ports.UserMessage{HTML: text, Buttons: buttons})

	switch {
	case err == nil:
	case errors.Is(err, domain.ErrTelegramNotLinked), errors.Is(err, domain.ErrTelegramUnavailable):
		s.log.Debug(ctx, "Failure not told: nowhere to send it", "user_id", f.owner)
	default:
		s.log.Warn(ctx, "Failure notification not sent", "user_id", f.owner, logging.Err(err))
	}
}

// RemindQuestion replies to a question of workflow still open, and mails it, pointing to
// Telegram, where it is answered (D185); each as the user chose.
func (s *Service) RemindQuestion(ctx context.Context, user domain.UserID, workflow domain.WorkflowID, messageID int, left time.Duration) error {
	telegram, mail, err := s.channels(ctx, user, ApprovalWaiting)
	if err != nil {
		return err
	}

	if mail {
		name := "one of your workflows"
		if w, err := s.workflows.Get(ctx, user, workflow); err == nil {
			name = w.Name
		}

		_ = s.mailUser(ctx, user, ApprovalWaiting, ports.UserMail{
			Subject:    "A question of " + name + " waits for your answer",
			Paragraphs: []string{"A question of " + name + " is still waiting for your answer in Telegram, for " + about(left) + " more."},
			LinkText:   "Open workflow", LinkURL: s.publicURL + "/workflows/" + string(workflow),
		})
	}

	if !telegram {
		return nil
	}

	return s.messenger.MessageUser(ctx, user, ports.UserMessage{
		HTML:    "⏳ This question is still waiting for your answer, for " + about(left) + " more.",
		ReplyTo: messageID,
	})
}

// RemindSessionKey tells the owner that a wallet's session key expires at until, or has,
// each way they chose. With nowhere to send it, nobody is told, and that is no error: the
// key is not asked about again. It fails only when nothing went and a send can be tried
// again.
func (s *Service) RemindSessionKey(ctx context.Context, user domain.UserID, w domain.Wallet, until time.Time) error {
	telegram, mail, err := s.channels(ctx, user, SessionKeyExpiring)
	if err != nil || (!telegram && !mail) {
		return err
	}

	name := w.Label
	if name == "" {
		name = string(w.Address)
	}

	left := until.Sub(s.now())

	mailed := false
	if mail {
		m := ports.UserMail{
			Subject:    "The session key of " + name + " has expired",
			Paragraphs: []string{"The session key of " + name + " has expired. Authorise a new one on the Wallets page, so the wallet can trade again."},
			LinkText:   "Open wallets", LinkURL: s.publicURL + "/wallets",
		}
		if left > 0 {
			m.Subject = "The session key of " + name + " expires in " + about(left)
			m.Paragraphs = []string{"The session key of " + name + " expires in " + about(left) + ", on " +
				until.UTC().Format("2 January 2006") + " (UTC). Authorise a new one on the Wallets page before then, so the wallet keeps trading."}
		}

		mailed = s.mailUser(ctx, user, SessionKeyExpiring, m) == nil
	}

	if !telegram {
		return nil // a failed mail is not tried again: the owner sees the date on the card
	}

	bold := "<b>" + html.EscapeString(name) + "</b>"

	text := "🔑 The session key of " + bold + " has expired. Authorise a new one on the Wallets page, so the wallet can trade again."
	if left > 0 {
		text = "🔑 The session key of " + bold + " expires in " + about(left) + ", on " + until.UTC().Format("2 January 2006") +
			" (UTC). Authorise a new one on the Wallets page before then, so the wallet keeps trading."
	}

	var buttons [][]ports.TelegramButton
	if strings.HasPrefix(s.publicURL, "https://") {
		buttons = [][]ports.TelegramButton{{{Text: "Open wallets", URL: s.publicURL + "/wallets"}}}
	}

	err = s.messenger.MessageUser(ctx, user, ports.UserMessage{HTML: text, Buttons: buttons})
	if err == nil || mailed || errors.Is(err, domain.ErrTelegramNotLinked) || errors.Is(err, domain.ErrTelegramUnavailable) {
		return nil
	}

	return err
}

// LiveTradeFailed tells the owner a live order's trade failed on chain, undone in the
// workflow's books (D193), each way they chose. With nowhere to send it, nobody is told.
func (s *Service) LiveTradeFailed(ctx context.Context, user domain.UserID, o domain.PlacedOrder) error {
	telegram, mail, err := s.channels(ctx, user, LiveTradeFailed)
	if err != nil || (!telegram && !mail) {
		return err
	}

	name := "a deleted workflow"
	if w, err := s.workflows.Get(ctx, user, o.WorkflowID); err == nil {
		name = w.Name
	}

	shares, dollars, _ := o.Totals()
	what := func(name string) string { return fmt.Sprintf("A live %s of %s's order", o.Side, name) }
	undone := "Its trade failed on chain, so the shares never changed hands; the workflow's books undo it."
	if o.Status != domain.OrderFailed {
		undone = fmt.Sprintf("Part of its trade failed on chain; the books undo that part and keep %s shares for $%s.",
			shares.String(), dollars.StringFixed(2))
	}

	mailed := false
	if mail {
		mailed = s.mailUser(ctx, user, LiveTradeFailed, ports.UserMail{
			Subject:    "A live trade of " + name + " failed",
			Paragraphs: []string{what(name) + " matched on Polymarket, but " + strings.ToLower(undone[:1]) + undone[1:]},
			LinkText:   "Open the workflow", LinkURL: s.publicURL + "/workflows/" + string(o.WorkflowID),
		}) == nil
	}

	if !telegram {
		return nil
	}

	text := "⚠️ " + what("<b>"+html.EscapeString(name)+"</b>") + " matched on Polymarket. " + undone

	var buttons [][]ports.TelegramButton
	if strings.HasPrefix(s.publicURL, "https://") {
		buttons = [][]ports.TelegramButton{{{Text: "Open the workflow", URL: s.publicURL + "/workflows/" + string(o.WorkflowID)}}}
	}

	err = s.messenger.MessageUser(ctx, user, ports.UserMessage{HTML: text, Buttons: buttons})
	if err == nil || mailed || errors.Is(err, domain.ErrTelegramNotLinked) || errors.Is(err, domain.ErrTelegramUnavailable) {
		return nil
	}

	return err
}

// about rounds d for people: about 15 minutes, about 2 hours, about 3 days.
func about(d time.Duration) string {
	switch {
	case d < 90*time.Minute:
		n := max(1, int(d.Round(time.Minute)/time.Minute))
		return fmt.Sprintf("about %d %s", n, plural(n, "minute", "minutes"))
	case d < 48*time.Hour:
		n := int(d.Round(time.Hour) / time.Hour)
		return fmt.Sprintf("about %d %s", n, plural(n, "hour", "hours"))
	default:
		n := int(d.Round(24*time.Hour) / (24 * time.Hour))
		return fmt.Sprintf("about %d days", n)
	}
}

// due reports whether a failure of the workflow is told now, and how many were not since
// the last one that was.
func (s *Service) due(id domain.WorkflowID) (missed int, tell bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()

	c, seen := s.cooldowns[id]
	if seen && now.Sub(c.at) < failureCooldown {
		c.missed++
		s.cooldowns[id] = c

		return 0, false
	}

	if len(s.cooldowns) >= maxCooldowns {
		for wid, c := range s.cooldowns {
			if now.Sub(c.at) >= failureCooldown {
				delete(s.cooldowns, wid)
			}
		}
	}

	s.cooldowns[id] = cooldown{at: now}

	return c.missed, true
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
