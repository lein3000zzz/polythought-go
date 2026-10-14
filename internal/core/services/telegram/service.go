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
	"html"
	"strings"
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
}

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

// CreateLink makes a single-use link to the bot. A new one replaces the last.
func (s *Service) CreateLink(ctx context.Context, p auth.Principal) (LinkURL, error) {
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

// Unlink forgets the user's Telegram account, and tells its chat.
func (s *Service) Unlink(ctx context.Context, p auth.Principal) error {
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
	link, err := s.links.ForUser(ctx, user)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.ErrTelegramNotLinked
	}

	if err != nil {
		return err
	}

	_, err = s.bot.Send(ctx, ports.TelegramMessage{ChatID: link.ChatID, HTML: m.HTML, Buttons: m.Buttons, Silent: m.Silent})
	if errors.Is(err, domain.ErrTelegramBlocked) {
		if derr := s.links.Delete(ctx, user); derr != nil && !errors.Is(derr, domain.ErrNotFound) {
			s.log.Warn(ctx, "Blocked Telegram chat not unlinked", logging.Err(derr))
		} else {
			s.log.Info(ctx, "Telegram unlinked", "user_id", user, "from", "blocked")
		}
	}

	return err
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

	err = s.links.Save(ctx, domain.TelegramLink{
		UserID: user.ID, TelegramUserID: m.From.ID, ChatID: m.ChatID,
		Username: m.From.Username, FirstName: m.From.FirstName, LinkedAt: s.now(),
	})
	if err != nil {
		s.failed(ctx, m.ChatID, err)
		return
	}

	s.log.Info(ctx, "Telegram linked", "user_id", user.ID)

	_, err = s.bot.Send(ctx, ports.TelegramMessage{
		ChatID:   m.ChatID,
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
