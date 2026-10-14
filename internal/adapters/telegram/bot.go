// Package telegram runs the instance's Telegram bot on the Bot API, through
// go-telegram/bot: by long polling, or by webhook when the instance has public HTTPS.
//
// The bot can be reconfigured while the server runs. Configure hands Run a new
// configuration, the latest one wins, and Run stops the bot and starts it again with it,
// so a change on the Config page never waits on Telegram.
//
// The token is part of every Bot API URL. Calls are therefore traced and measured by
// method only, never through HTTP-level instrumentation that would record the URL.
package telegram

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.TelegramBot = (*Bot)(nil)

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/telegram"

	ModePolling = "polling"
	ModeWebhook = "webhook"

	// WebhookPath is where Telegram posts updates in webhook mode, under the public URL.
	WebhookPath = "/api/v1/telegram/webhook"

	pollTimeout = 50 * time.Second
	initTimeout = 15 * time.Second
	stopTimeout = 5 * time.Second

	// A 429 asks to wait; waits longer than this fail the call instead.
	maxRetryAfter = 30 * time.Second
	maxRetries    = 2

	firstBackoff = 5 * time.Second
	maxBackoff   = 5 * time.Minute

	// Telegram allows about one message a second per chat and thirty a second overall.
	perChatRate  = rate.Limit(1)
	perChatBurst = 3
	globalRate   = rate.Limit(30)
)

var allowedUpdates = bot.AllowedUpdates{"message", "callback_query"}

// Config is how the bot should run.
type Config struct {
	Enabled bool
	Token   string
	Mode    string // ModePolling or ModeWebhook

	// PublicURL is where Telegram reaches this server, for webhook mode. It must be https.
	PublicURL string
}

// Handler receives what Telegram users do. It runs on its own goroutine per update.
type Handler func(ctx context.Context, u ports.TelegramUpdate)

// errPermanent marks start failures that retrying cannot fix, such as a refused token.
var errPermanent = errors.New("permanent")

type Bot struct {
	kit       *redkit.Kit
	scope     tracing.Scope
	log       logging.Logger
	serverURL string
	client    *http.Client
	limits    *limits

	handler atomic.Pointer[Handler]

	wake chan struct{}

	mu      sync.Mutex
	pending *Config  // for Run to apply
	cur     *running // nil when the bot is not running
	status  ports.TelegramStatus
}

// running is a started bot.
type running struct {
	tg     *bot.Bot
	ctx    context.Context // ends when the bot stops
	secret string          // webhook mode: what Telegram sends to prove it is Telegram
}

type Option func(*Bot)

// WithServerURL replaces https://api.telegram.org, for tests.
func WithServerURL(u string) Option {
	return func(b *Bot) { b.serverURL = u }
}

// WithRates replaces the send rates, per chat and overall, for tests.
func WithRates(perChat, global rate.Limit) Option {
	return func(b *Bot) { b.limits = newLimits(perChat, perChatBurst, global) }
}

func New(util *observability.Utility, opts ...Option) (*Bot, error) {
	log := util.Logger.With("component", "telegram")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "telegram",
		Buckets:   redkit.MediumLongBuckets,
		Component: "telegram",
	})
	if err != nil {
		return nil, fmt.Errorf("telegram: register metrics: %w", err)
	}

	b := &Bot{
		kit:       kit,
		scope:     tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:       log,
		serverURL: "https://api.telegram.org",
		client:    &http.Client{Timeout: pollTimeout + 10*time.Second},
		limits:    newLimits(perChatRate, perChatBurst, globalRate),
		wake:      make(chan struct{}, 1),
	}

	for _, opt := range opts {
		opt(b)
	}

	return b, nil
}

// SetHandler sets what receives updates. Set it before Run.
func (b *Bot) SetHandler(h Handler) {
	b.handler.Store(&h)
}

// Configure asks Run to apply cfg. It never blocks; the latest configuration wins.
func (b *Bot) Configure(cfg Config) {
	b.mu.Lock()
	b.pending = &cfg
	b.mu.Unlock()

	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// Run applies configurations until ctx is done, then stops the bot. A start that fails
// for a reason that may pass, such as Telegram being unreachable, is retried with
// backoff.
func (b *Bot) Run(ctx context.Context) {
	var (
		applied Config
		have    bool
		stop    = func() {}
		retry   <-chan time.Time
		backoff time.Duration
	)

	defer func() { stop() }()

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.wake:
			cfg, ok := b.takePending()
			if !ok || have && cfg == applied {
				continue
			}

			applied, have, backoff = cfg, true, 0
		case <-retry:
		}

		stop()
		stop, retry = func() {}, nil

		s, err := b.start(ctx, applied)
		if err == nil {
			stop = s
			continue
		}

		if errors.Is(err, errPermanent) || ctx.Err() != nil {
			continue
		}

		backoff = min(max(2*backoff, firstBackoff), maxBackoff)
		retry = time.After(backoff)
	}
}

func (b *Bot) takePending() (Config, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.pending == nil {
		return Config{}, false
	}

	cfg := *b.pending
	b.pending = nil

	return cfg, true
}

// start starts the bot as cfg says and returns how to stop it. Its errors are also put
// in the status, for admins to see.
func (b *Bot) start(ctx context.Context, cfg Config) (stop func(), err error) {
	defer func() {
		if err != nil {
			b.log.Warn(ctx, "Telegram bot not started", logging.Err(err))
			b.setStatus(ports.TelegramStatus{Mode: cfg.Mode, Error: strings.TrimPrefix(err.Error(), errPermanent.Error()+": "), ErrorAt: time.Now()})
		}
	}()

	switch {
	case !cfg.Enabled:
		b.setStatus(ports.TelegramStatus{})
		return func() {}, nil
	case cfg.Token == "":
		return nil, fmt.Errorf("%w: there is no bot token; add one on the Config page", errPermanent)
	case cfg.Mode == ModeWebhook && !strings.HasPrefix(cfg.PublicURL, "https://"):
		return nil, fmt.Errorf("%w: webhook mode needs an https public URL; use polling", errPermanent)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	tg, err := bot.New(cfg.Token,
		bot.WithSkipGetMe(), // done below, instrumented
		bot.WithServerURL(b.serverURL),
		bot.WithHTTPClient(pollTimeout, b.client),
		bot.WithDefaultHandler(b.dispatch),
		bot.WithErrorsHandler(func(err error) { b.problem(runCtx, err) }),
		bot.WithAllowedUpdates(allowedUpdates),
		bot.WithWorkers(4),
	)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("%w: %w", errPermanent, err)
	}

	initCtx, done := context.WithTimeout(ctx, initTimeout)
	defer done()

	me, err := call(initCtx, b, "telegram.getMe", tg.GetMe)
	if err != nil {
		cancel()

		if errors.Is(err, bot.ErrorUnauthorized) || errors.Is(err, bot.ErrorNotFound) {
			return nil, fmt.Errorf("%w: Telegram refused the bot token; check it with @BotFather", errPermanent)
		}

		return nil, fmt.Errorf("reaching Telegram: %w", err)
	}

	secret := ""

	if cfg.Mode == ModeWebhook {
		secret = newSecret()
		_, err = call(initCtx, b, "telegram.setWebhook", func(ctx context.Context) (bool, error) {
			return tg.SetWebhook(ctx, &bot.SetWebhookParams{
				URL: strings.TrimRight(cfg.PublicURL, "/") + WebhookPath, AllowedUpdates: allowedUpdates, SecretToken: secret,
			})
		})
	} else {
		// Telegram sends nothing to getUpdates while a webhook is set.
		_, err = call(initCtx, b, "telegram.deleteWebhook", func(ctx context.Context) (bool, error) {
			return tg.DeleteWebhook(ctx, &bot.DeleteWebhookParams{})
		})
	}

	if err != nil {
		cancel()
		return nil, fmt.Errorf("setting up %s: %w", cfg.Mode, err)
	}

	b.mu.Lock()
	b.cur = &running{tg: tg, ctx: runCtx, secret: secret}
	b.status = ports.TelegramStatus{Running: true, Username: me.Username, Mode: cfg.Mode}
	b.mu.Unlock()

	polling := make(chan struct{})

	if cfg.Mode == ModeWebhook {
		close(polling) // updates arrive through HandleWebhook
	} else {
		go func() {
			defer close(polling)
			tg.Start(runCtx)
		}()
	}

	b.log.Info(ctx, "Telegram bot started", "bot", me.Username, "mode", cfg.Mode)

	return func() {
		b.mu.Lock()
		b.cur = nil
		b.status = ports.TelegramStatus{}
		b.mu.Unlock()

		cancel()
		<-polling

		if cfg.Mode == ModeWebhook {
			// So Telegram keeps updates for whichever way the bot runs next.
			stopCtx, done := context.WithTimeout(context.Background(), stopTimeout)
			defer done()

			_, _ = call(stopCtx, b, "telegram.deleteWebhook", func(ctx context.Context) (bool, error) {
				return tg.DeleteWebhook(ctx, &bot.DeleteWebhookParams{})
			})
		}

		b.log.Info(context.Background(), "Telegram bot stopped", "bot", me.Username)
	}, nil
}

func newSecret() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf) // never fails

	return base64.RawURLEncoding.EncodeToString(buf)
}

// problem records a problem the running bot ran into, such as a lost connection or
// another server polling with the same token.
func (b *Bot) problem(ctx context.Context, err error) {
	b.log.Warn(ctx, "Telegram bot problem", logging.Err(err))

	b.mu.Lock()
	b.status.Error, b.status.ErrorAt = err.Error(), time.Now()
	b.mu.Unlock()
}

func (b *Bot) setStatus(s ports.TelegramStatus) {
	b.mu.Lock()
	b.status = s
	b.mu.Unlock()
}

func (b *Bot) Status() ports.TelegramStatus {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.status
}

func (b *Bot) current() *running {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.cur
}

// HandleWebhook passes on an update Telegram posted. secret must be the one the bot
// gave Telegram; anything else, or a bot not in webhook mode, is domain.ErrNotFound.
func (b *Bot) HandleWebhook(_ context.Context, secret string, body []byte) error {
	cur := b.current()
	if cur == nil || cur.secret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(cur.secret)) != 1 {
		return domain.ErrNotFound
	}

	var upd models.Update
	if err := json.Unmarshal(body, &upd); err != nil {
		return fmt.Errorf("%w: not a Telegram update", domain.ErrInvalidInput)
	}

	// The bot's context, not the request's: the handler outlives the request.
	cur.tg.ProcessUpdate(cur.ctx, &upd)

	return nil
}

// dispatch turns the library's update into ours and hands it to the handler.
func (b *Bot) dispatch(ctx context.Context, _ *bot.Bot, upd *models.Update) {
	u, ok := convert(upd)
	if !ok {
		return
	}

	h := b.handler.Load()
	if h == nil {
		return
	}

	defer func() {
		if p := recover(); p != nil {
			b.log.Error(ctx, "Telegram update handler panicked", "panic", fmt.Sprint(p))
		}
	}()

	(*h)(ctx, u)
}

func convert(upd *models.Update) (ports.TelegramUpdate, bool) {
	switch {
	case upd.Message != nil && upd.Message.From != nil && upd.Message.Text != "":
		m := upd.Message

		return ports.TelegramUpdate{Message: &ports.TelegramIncoming{
			ChatID:  m.Chat.ID,
			Private: m.Chat.Type == models.ChatTypePrivate,
			From:    user(*m.From),
			Text:    m.Text,
		}}, true

	case upd.CallbackQuery != nil:
		q := upd.CallbackQuery
		cb := &ports.TelegramCallback{ID: q.ID, From: user(q.From), Data: q.Data}

		switch {
		case q.Message.Message != nil:
			cb.ChatID, cb.MessageID = q.Message.Message.Chat.ID, q.Message.Message.ID
		case q.Message.InaccessibleMessage != nil:
			cb.ChatID, cb.MessageID = q.Message.InaccessibleMessage.Chat.ID, q.Message.InaccessibleMessage.MessageID
		}

		return ports.TelegramUpdate{Callback: cb}, true
	}

	return ports.TelegramUpdate{}, false
}

func user(u models.User) ports.TelegramUser {
	return ports.TelegramUser{ID: u.ID, Username: u.Username, FirstName: u.FirstName}
}

// Send sends a message, waiting its turn under Telegram's rate limits.
func (b *Bot) Send(ctx context.Context, m ports.TelegramMessage) (int, error) {
	cur := b.current()
	if cur == nil {
		return 0, domain.ErrTelegramUnavailable
	}

	if err := b.limits.wait(ctx, m.ChatID); err != nil {
		return 0, err
	}

	params := &bot.SendMessageParams{
		ChatID: m.ChatID, Text: m.HTML, ParseMode: models.ParseModeHTML, LinkPreviewOptions: noPreview(),
		DisableNotification: m.Silent,
	}

	switch {
	case len(m.Buttons) > 0:
		params.ReplyMarkup = keyboard(m.Buttons)
	case len(m.Keyboard) > 0:
		params.ReplyMarkup = replyKeyboard(m.Keyboard)
	case m.RemoveKeyboard:
		params.ReplyMarkup = &models.ReplyKeyboardRemove{RemoveKeyboard: true}
	}

	msg, err := retrying(ctx, func(ctx context.Context) (*models.Message, error) {
		return call(ctx, b, "telegram.sendMessage", func(ctx context.Context) (*models.Message, error) {
			return cur.tg.SendMessage(ctx, params)
		})
	})
	if err != nil {
		return 0, mapError(err)
	}

	return msg.ID, nil
}

// Edit replaces the text and buttons of a message the bot sent.
func (b *Bot) Edit(ctx context.Context, messageID int, m ports.TelegramMessage) error {
	cur := b.current()
	if cur == nil {
		return domain.ErrTelegramUnavailable
	}

	if err := b.limits.global.Wait(ctx); err != nil {
		return err
	}

	params := &bot.EditMessageTextParams{
		ChatID: m.ChatID, MessageID: messageID, Text: m.HTML, ParseMode: models.ParseModeHTML, LinkPreviewOptions: noPreview(),
	}
	if len(m.Buttons) > 0 {
		params.ReplyMarkup = keyboard(m.Buttons)
	}

	_, err := retrying(ctx, func(ctx context.Context) (*models.Message, error) {
		return call(ctx, b, "telegram.editMessageText", func(ctx context.Context) (*models.Message, error) {
			return cur.tg.EditMessageText(ctx, params)
		})
	})

	// Pressing a button that leads to what is already shown is not a failure.
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}

	return mapError(err)
}

// AnswerCallback acknowledges a button press, optionally with a short note.
func (b *Bot) AnswerCallback(ctx context.Context, callbackID, text string) error {
	cur := b.current()
	if cur == nil {
		return domain.ErrTelegramUnavailable
	}

	_, err := call(ctx, b, "telegram.answerCallbackQuery", func(ctx context.Context) (bool, error) {
		return cur.tg.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: callbackID, Text: text})
	})

	return mapError(err)
}

func noPreview() *models.LinkPreviewOptions {
	off := true
	return &models.LinkPreviewOptions{IsDisabled: &off}
}

func keyboard(rows [][]ports.TelegramButton) *models.InlineKeyboardMarkup {
	out := &models.InlineKeyboardMarkup{InlineKeyboard: make([][]models.InlineKeyboardButton, len(rows))}

	for i, row := range rows {
		out.InlineKeyboard[i] = make([]models.InlineKeyboardButton, len(row))
		for j, btn := range row {
			out.InlineKeyboard[i][j] = models.InlineKeyboardButton{Text: btn.Text, CallbackData: btn.Data, URL: btn.URL}
		}
	}

	return out
}

// replyKeyboard stays under the input field, sized to its buttons, until removed.
func replyKeyboard(rows [][]string) *models.ReplyKeyboardMarkup {
	out := &models.ReplyKeyboardMarkup{
		Keyboard: make([][]models.KeyboardButton, len(rows)), IsPersistent: true, ResizeKeyboard: true,
	}

	for i, row := range rows {
		out.Keyboard[i] = make([]models.KeyboardButton, len(row))
		for j, text := range row {
			out.Keyboard[i][j] = models.KeyboardButton{Text: text}
		}
	}

	return out
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bot.ErrorForbidden):
		return fmt.Errorf("%w: %w", domain.ErrTelegramBlocked, err)
	default:
		return fmt.Errorf("telegram: %w", err)
	}
}

// retrying calls fn again when Telegram asks to wait, if the wait is short.
func retrying[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	for attempt := 0; ; attempt++ {
		out, err := fn(ctx)

		var tooMany *bot.TooManyRequestsError
		if attempt >= maxRetries || !errors.As(err, &tooMany) {
			return out, err
		}

		wait := time.Duration(tooMany.RetryAfter) * time.Second
		if wait > maxRetryAfter {
			return out, err
		}

		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// call runs one Bot API method, measured and traced by its name.
func call[T any](ctx context.Context, b *Bot, op string, fn func(context.Context) (T, error)) (out T, err error) {
	done := b.kit.Start(ctx, op)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, b.scope, op, func(ctx context.Context, _ trace.Span) (T, error) {
		return fn(ctx)
	})
}
