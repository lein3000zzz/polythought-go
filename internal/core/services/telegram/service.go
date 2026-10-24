// Package telegram links accounts to Telegram and answers people who message the
// instance's bot. The bot itself is an adapter; this is what it says and does.
//
// Linking: Settings shows a t.me/<bot>?start=<code> link. Opening it sends the bot
// "/start <code>", and the code, single use and short-lived, says which account the
// Telegram user is. Only the code's hash is stored.
package telegram

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	linkTTL = 10 * time.Minute

	cmdStart   = "/start"
	cmdMenu    = "/menu"
	dataUnlink = "unlink"
	dataRelink = "lk:" // lk:yes moves the chat to the account of the link opened; lk:no keeps it (R10)
)

// What the bot says.
const (
	textNotLinked = "Hi! I am Polythought's bot. To use me, link this chat to your account: " +
		"in Polythought, open <b>Settings → Telegram</b> and press <b>Link Telegram</b>."
	textCodeUsed = "This link has expired or was already used. Make a new one in Polythought: " +
		"<b>Settings → Telegram</b>."
	textDisabled  = "Your Polythought account is disabled."
	textPrivate   = "Message me in a private chat to link your account."
	textUnlinked  = "Unlinked. This chat no longer gets anything from Polythought; link it again from <b>Settings → Telegram</b>."
	textUnlinkedQ = "This chat is no longer linked to Polythought. Link it again from <b>Settings → Telegram</b>."
	textFailed    = "Something went wrong on our side. Try again in a minute."
	textStale     = "This button no longer works."
	textNoLink    = "This chat is not linked to Polythought."
	textUseMenu   = "Pick a section from the menu below, or send /menu to bring it back."
)

var unlinkButton = [][]ports.TelegramButton{{{Text: "Unlink this chat", Data: dataUnlink}}}

var (
	_ ports.UserMessenger = (*Service)(nil)
	_ ports.TelegramInbox = (*Service)(nil)
)

type Service struct {
	bot   ports.TelegramBot
	links ports.TelegramLinkRepository
	users ports.UserRepository
	inbox *inbox
	menu  *Menu // nil until UseMenu
	log   logging.Logger
	now   func() time.Time

	answers  Answers  // nil until UseAnswers
	confirms Confirms // nil until UseConfirmations

	mu      sync.Mutex
	relinks map[int64]relink // by Telegram user: a link to another account, waiting for a yes (R10)
}

// relink is a start link opened by a Telegram user linked to another account: the
// account it is for, until it is answered or too old.
type relink struct {
	user  domain.UserID
	until time.Time
}

// relinkFor is how long a move to another account waits for its yes.
const relinkFor = 10 * time.Minute

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(bot ports.TelegramBot, links ports.TelegramLinkRepository, users ports.UserRepository, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		bot:   bot,
		links: links,
		users: users,
		inbox: newInbox(),
		log:   log.With("component", "telegram"),
		now:   func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Status is Telegram as one user sees it.
type Status struct {
	Available   bool // the bot runs, so the user can link
	BotUsername string

	// Problem says why the bot does not run, or what last went wrong. Admins only.
	Problem   string
	ProblemAt time.Time

	Link *domain.TelegramLink // nil when the user has not linked
}

func (s *Service) Status(ctx context.Context, p auth.Principal) (Status, error) {
	bot := s.bot.Status()
	out := Status{Available: bot.Running, BotUsername: bot.Username}

	if p.User.IsAdmin() {
		out.Problem, out.ProblemAt = bot.Error, bot.ErrorAt
	}

	link, err := s.links.ForUser(ctx, p.User.ID)

	switch {
	case err == nil:
		out.Link = &link
	case !errors.Is(err, domain.ErrNotFound):
		return Status{}, err
	}

	return out, nil
}

// LinkURL links whichever Telegram account opens it to the account that asked for it.
type LinkURL struct {
	URL       string
	ExpiresAt time.Time
}

// CreateLink makes a single-use link to the bot. A new one replaces the last. It is the
// user's, at a browser: the chat it links confirms the live actions their agents and tokens
// ask for, and its menu acts as them (A1).
func (s *Service) CreateLink(ctx context.Context, p auth.Principal) (LinkURL, error) {
	if !p.Itself() {
		return LinkURL{}, errAtBrowser
	}

	bot := s.bot.Status()
	if !bot.Running || bot.Username == "" {
		return LinkURL{}, domain.ErrTelegramUnavailable
	}

	code := newCode()
	expires := s.now().Add(linkTTL)

	if err := s.links.SaveCode(ctx, p.User.ID, hashCode(code), expires); err != nil {
		return LinkURL{}, err
	}

	return LinkURL{URL: "https://t.me/" + bot.Username + "?start=" + code, ExpiresAt: expires}, nil
}

// errAtBrowser refuses a token's or an agent's change to the user's Telegram.
var errAtBrowser = fmt.Errorf("%w: linking Telegram is the user's, at a browser: its chat confirms what agents and tokens ask for",
	domain.ErrForbidden)

// Unlink forgets the user's Telegram account, and tells its chat. It is the user's, at a
// browser, as linking is.
func (s *Service) Unlink(ctx context.Context, p auth.Principal) error {
	if !p.Itself() {
		return errAtBrowser
	}

	link, err := s.links.ForUser(ctx, p.User.ID)
	if err != nil {
		return err
	}

	if err := s.links.Delete(ctx, p.User.ID); err != nil {
		return err
	}

	s.log.Info(ctx, "Telegram unlinked", "user_id", p.User.ID, "from", "app")

	_, err = s.bot.Send(ctx, ports.TelegramMessage{ChatID: link.ChatID, HTML: textUnlinkedQ, RemoveKeyboard: true})
	s.ignored(ctx, "Unlink notice not sent", err)

	return nil
}

// HandleUpdate answers what a Telegram user did. People who have not linked get only
// instructions on how to.
func (s *Service) HandleUpdate(ctx context.Context, u ports.TelegramUpdate) {
	switch {
	case u.Message != nil:
		s.message(ctx, *u.Message)
	case u.Callback != nil:
		s.callback(ctx, *u.Callback)
	}
}

func (s *Service) message(ctx context.Context, m ports.TelegramIncoming) {
	cmd, arg := command(m.Text)

	if !m.Private {
		// Groups see only commands; answer the one meant to link.
		if cmd == cmdStart {
			s.reply(ctx, m.ChatID, textPrivate, nil)
		}

		return
	}

	if cmd == cmdStart && arg != "" {
		s.link(ctx, m, arg)
		return
	}

	link, err := s.links.ForTelegramUser(ctx, m.From.ID)
	if errors.Is(err, domain.ErrNotFound) {
		s.reply(ctx, m.ChatID, textNotLinked, nil)
		return
	}

	if err != nil {
		s.failed(ctx, m.ChatID, err)
		return
	}

	user, err := s.users.ByID(ctx, link.UserID)
	if err != nil {
		s.failed(ctx, m.ChatID, err)
		return
	}

	if user.Disabled() {
		s.reply(ctx, m.ChatID, textDisabled, nil)
		return
	}

	if cmd == cmdStart || cmd == cmdMenu {
		s.showMenu(ctx, m.ChatID, user)
		return
	}

	if section := sectionOf(m.Text); section != "" {
		s.openSection(ctx, m.ChatID, user, section)
		return
	}

	// Anything else may be for a workflow's Telegram trigger.
	matched, dropped := s.inbox.deliver(user.ID, ports.TelegramInboxMessage{
		Text: m.Text, Username: m.From.Username, FirstName: m.From.FirstName, At: s.now(),
	})

	if dropped > 0 {
		s.log.Warn(ctx, "Telegram message dropped: the workflow is busy", "user_id", user.ID, "workflows", dropped)
	}

	if !matched {
		_, err := s.bot.Send(ctx, ports.TelegramMessage{ChatID: m.ChatID, HTML: textUseMenu, Keyboard: mainKeyboard})
		s.ignored(ctx, "Telegram reply not sent", err)
	}
}

// Subscribe hands the owner's messages that match to a workflow's Telegram trigger.
func (s *Service) Subscribe(owner domain.UserID, match func(text string) bool) (<-chan ports.TelegramInboxMessage, func()) {
	return s.inbox.subscribe(owner, match)
}

// MessageUser writes to the user's linked chat. A chat that blocked the bot is unlinked,
// as if the user had unlinked it.
func (s *Service) MessageUser(ctx context.Context, user domain.UserID, m ports.UserMessage) error {
	_, err := s.send(ctx, user, ports.TelegramMessage{HTML: m.HTML, Buttons: m.Buttons, Silent: m.Silent, ReplyTo: m.ReplyTo})
	return err
}

// chatOf is the user's link, or domain.ErrTelegramNotLinked.
func (s *Service) chatOf(ctx context.Context, user domain.UserID) (domain.TelegramLink, error) {
	link, err := s.links.ForUser(ctx, user)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.TelegramLink{}, domain.ErrTelegramNotLinked
	}

	return link, err
}

// send writes m to the user's linked chat.
func (s *Service) send(ctx context.Context, user domain.UserID, m ports.TelegramMessage) (int, error) {
	link, err := s.chatOf(ctx, user)
	if err != nil {
		return 0, err
	}

	m.ChatID = link.ChatID

	id, err := s.bot.Send(ctx, m)
	if errors.Is(err, domain.ErrTelegramBlocked) {
		if derr := s.links.Delete(ctx, user); derr != nil && !errors.Is(derr, domain.ErrNotFound) {
			s.log.Warn(ctx, "Blocked Telegram chat not unlinked", logging.Err(derr))
		} else {
			s.log.Info(ctx, "Telegram unlinked", "user_id", user, "from", "blocked")
		}
	}

	return id, err
}

// link binds the Telegram user to the account whose code they sent.
func (s *Service) link(ctx context.Context, m ports.TelegramIncoming, code string) {
	userID, err := s.links.ConsumeCode(ctx, hashCode(code), s.now())
	if errors.Is(err, domain.ErrNotFound) {
		s.reply(ctx, m.ChatID, textCodeUsed, nil)
		return
	}

	if err != nil {
		s.failed(ctx, m.ChatID, err)
		return
	}

	user, err := s.users.ByID(ctx, userID)
	if err != nil {
		s.failed(ctx, m.ChatID, err)
		return
	}

	if user.Disabled() {
		s.reply(ctx, m.ChatID, textDisabled, nil)
		return
	}

	// Linked to another account already: moved only once its owner says so, both named,
	// so a link someone else made cannot take the chat over unseen (R10).
	if current, err := s.links.ForTelegramUser(ctx, m.From.ID); err == nil && current.UserID != user.ID {
		s.askRelink(ctx, m, current, user)
		return
	}

	s.save(ctx, m.ChatID, m.From, user)
}

// askRelink asks a Telegram user linked to one account whether to move to another.
func (s *Service) askRelink(ctx context.Context, m ports.TelegramIncoming, current domain.TelegramLink, to domain.User) {
	from, err := s.users.ByID(ctx, current.UserID)
	if err != nil {
		s.failed(ctx, m.ChatID, err)
		return
	}

	s.mu.Lock()
	if s.relinks == nil {
		s.relinks = map[int64]relink{}
	}

	s.relinks[m.From.ID] = relink{user: to.ID, until: s.now().Add(relinkFor)}
	s.mu.Unlock()

	s.reply(ctx, m.ChatID, "This Telegram is linked to <b>"+html.EscapeString(from.Email)+"</b>. Link it to <b>"+
		html.EscapeString(to.Email)+"</b> instead? Only if that account is yours: messages and buttons here would be its.",
		[][]ports.TelegramButton{{
			{Text: "Link to " + to.Email, Data: dataRelink + "yes"}, {Text: "Keep " + from.Email, Data: dataRelink + "no"},
		}})
}

// relinked answers askRelink's question.
func (s *Service) relinked(ctx context.Context, cb ports.TelegramCallback) {
	s.mu.Lock()
	r, ok := s.relinks[cb.From.ID]
	delete(s.relinks, cb.From.ID)
	s.mu.Unlock()

	if cb.Data != dataRelink+"yes" {
		s.answer(ctx, cb.ID, "Kept")
		s.ignored(ctx, "Relink message not edited", s.bot.Edit(ctx, cb.MessageID, ports.TelegramMessage{ChatID: cb.ChatID, HTML: "Kept as it was."}))

		return
	}

	if !ok || !r.until.After(s.now()) {
		s.answer(ctx, cb.ID, "")
		s.reply(ctx, cb.ChatID, textCodeUsed, nil)

		return
	}

	user, err := s.users.ByID(ctx, r.user)
	if err != nil || user.Disabled() {
		s.answer(ctx, cb.ID, textDisabled)
		return
	}

	s.answer(ctx, cb.ID, "")
	s.save(ctx, cb.ChatID, cb.From, user)
}

// save links the Telegram user, in this chat, to user's account.
func (s *Service) save(ctx context.Context, chatID int64, from ports.TelegramUser, user domain.User) {
	// The chat it replaces hears of it, so a link made elsewhere cannot take the account's
	// confirmations over unseen (A1).
	before, err := s.links.ForUser(ctx, user.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.failed(ctx, chatID, err)
		return
	}

	err = s.links.Save(ctx, domain.TelegramLink{
		UserID: user.ID, TelegramUserID: from.ID, ChatID: chatID,
		Username: from.Username, FirstName: from.FirstName, LinkedAt: s.now(),
	})
	if err != nil {
		s.failed(ctx, chatID, err)
		return
	}

	s.log.Info(ctx, "Telegram linked", "user_id", user.ID)

	if before.ChatID != 0 && before.ChatID != chatID {
		_, err := s.bot.Send(ctx, ports.TelegramMessage{
			ChatID: before.ChatID,
			HTML: "Polythought account <b>" + html.EscapeString(user.Email) + "</b> is now linked to another Telegram chat, " +
				"which gets its messages and confirms what its agents ask for. If that was not you, sign in, open Settings, " +
				"Telegram, and link this chat again; then check Settings, API tokens.",
			RemoveKeyboard: true,
		})
		s.ignored(ctx, "Telegram move notice not sent", err)
	}

	_, err = s.bot.Send(ctx, ports.TelegramMessage{
		ChatID:   chatID,
		HTML:     "Linked to <b>" + html.EscapeString(user.Email) + "</b>. Messages from Polythought will arrive here. Pick a section below.",
		Keyboard: mainKeyboard,
	})
	s.ignored(ctx, "Telegram reply not sent", err)
}

func (s *Service) callback(ctx context.Context, cb ports.TelegramCallback) {
	link, err := s.links.ForTelegramUser(ctx, cb.From.ID)
	if err != nil {
		s.ignored(ctx, "Telegram link lookup failed", ignoreNotFound(err))
		s.answer(ctx, cb.ID, textNoLink)

		return
	}

	if cb.Data == dataUnlink {
		s.unlinkFromChat(ctx, cb, link)
		return
	}

	if strings.HasPrefix(cb.Data, dataRelink) {
		s.relinked(ctx, cb)
		return
	}

	user, err := s.users.ByID(ctx, link.UserID)
	if err != nil {
		s.log.Error(ctx, "Telegram button failed", logging.Err(err))
		s.answer(ctx, cb.ID, textFailed)

		return
	}

	if user.Disabled() {
		s.answer(ctx, cb.ID, textDisabled)
		return
	}

	if strings.HasPrefix(cb.Data, dataAsk) {
		s.answerQuestion(ctx, cb, user)
		return
	}

	if strings.HasPrefix(cb.Data, dataConfirm) {
		s.confirmPress(ctx, cb, user)
		return
	}

	s.menuPress(ctx, cb, user)
}

// unlinkFromChat unlinks on the chat's own button, and takes the menu away.
func (s *Service) unlinkFromChat(ctx context.Context, cb ports.TelegramCallback, link domain.TelegramLink) {
	if err := s.links.Delete(ctx, link.UserID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.log.Error(ctx, "Telegram unlink failed", logging.Err(err))
		s.answer(ctx, cb.ID, textFailed)

		return
	}

	s.log.Info(ctx, "Telegram unlinked", "user_id", link.UserID, "from", "telegram")
	s.answer(ctx, cb.ID, "Unlinked")
	s.ignored(ctx, "Unlink message not edited", s.bot.Edit(ctx, cb.MessageID, ports.TelegramMessage{ChatID: cb.ChatID, HTML: "Unlinked."}))

	_, err := s.bot.Send(ctx, ports.TelegramMessage{ChatID: cb.ChatID, HTML: textUnlinked, RemoveKeyboard: true})
	s.ignored(ctx, "Unlink message not sent", err)
}

func linkedText(u domain.User) string {
	return "This chat is linked to <b>" + html.EscapeString(u.Email) + "</b>. Messages from Polythought arrive here."
}

func (s *Service) reply(ctx context.Context, chatID int64, text string, buttons [][]ports.TelegramButton) {
	_, err := s.bot.Send(ctx, ports.TelegramMessage{ChatID: chatID, HTML: text, Buttons: buttons})
	s.ignored(ctx, "Telegram reply not sent", err)
}

func (s *Service) answer(ctx context.Context, callbackID, text string) {
	s.ignored(ctx, "Telegram button not answered", s.bot.AnswerCallback(ctx, callbackID, text))
}

func (s *Service) failed(ctx context.Context, chatID int64, err error) {
	s.log.Error(ctx, "Telegram update failed", logging.Err(err))
	s.reply(ctx, chatID, textFailed, nil)
}

// ignored logs an error nobody waits for. A chat that blocked the bot is no news.
func (s *Service) ignored(ctx context.Context, msg string, err error) {
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrTelegramBlocked), errors.Is(err, domain.ErrTelegramUnavailable):
		s.log.Debug(ctx, msg, logging.Err(err))
	default:
		s.log.Warn(ctx, msg, logging.Err(err))
	}
}

func ignoreNotFound(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}

	return err
}

// command splits "/start@poly_bot abc" into "/start" and "abc".
func command(text string) (cmd, arg string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}

	cmd, arg, _ = strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(cmd, "@")

	return strings.ToLower(cmd), strings.TrimSpace(arg)
}

// newCode is a link code: 192 random bits, in the characters a t.me start link allows.
func newCode() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf) // never fails

	return base64.RawURLEncoding.EncodeToString(buf)
}

func hashCode(code string) []byte {
	sum := sha256.Sum256([]byte(code))
	return sum[:]
}
