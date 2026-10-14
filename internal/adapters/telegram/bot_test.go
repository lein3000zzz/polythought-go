package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/lein3000zzz/polythought-go/internal/adapters/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const token = "123:secret-token"

// fakeAPI is the Bot API as far as the bot uses it.
type fakeAPI struct {
	srv *httptest.Server

	mu       sync.Mutex
	calls    []call
	updates  []string // JSON updates for the next getUpdates
	failures map[string][]string
	nextID   int
}

type call struct {
	method string
	form   map[string]string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()

	f := &fakeAPI{failures: make(map[string][]string), nextID: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)

	return f
}

// fail makes the next call of method answer with body, e.g. a 429.
func (f *fakeAPI) fail(method, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failures[method] = append(f.failures[method], body)
}

func (f *fakeAPI) push(update string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.updates = append(f.updates, update)
}

func (f *fakeAPI) called(method string) []map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []map[string]string

	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c.form)
		}
	}

	return out
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/bot")
	tok, method, _ := strings.Cut(path, "/")

	form := map[string]string{}
	if err := r.ParseMultipartForm(1 << 20); err == nil {
		for k, v := range r.MultipartForm.Value {
			form[k] = v[0]
		}
	}

	f.mu.Lock()
	f.calls = append(f.calls, call{method: method, form: form})

	if fails := f.failures[method]; len(fails) > 0 {
		f.failures[method] = fails[1:]
		f.mu.Unlock()
		_, _ = w.Write([]byte(fails[0]))

		return
	}

	var result string

	switch method {
	case "getMe":
		if tok != token {
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))

			return
		}

		result = `{"id":1,"is_bot":true,"first_name":"Poly","username":"poly_bot"}`
	case "getUpdates":
		result = "[" + strings.Join(f.updates, ",") + "]"
		f.updates = nil
	case "sendMessage", "editMessageText":
		f.nextID++
		result = fmt.Sprintf(`{"message_id":%d,"date":1,"chat":{"id":%s,"type":"private"}}`, f.nextID, form["chat_id"])
	default:
		result = "true"
	}
	f.mu.Unlock()

	if method == "getUpdates" && result == "[]" {
		time.Sleep(20 * time.Millisecond) // a short long poll
	}

	_, _ = w.Write([]byte(`{"ok":true,"result":` + result + `}`))
}

// start runs a bot against api with cfg until the test ends.
func start(t *testing.T, api *fakeAPI, cfg telegram.Config, h telegram.Handler) *telegram.Bot {
	t.Helper()

	b, err := telegram.New(observability.NoopUtility(), telegram.WithServerURL(api.srv.URL), telegram.WithRates(rate.Inf, rate.Inf))
	require.NoError(t, err)

	if h != nil {
		b.SetHandler(h)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		b.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	b.Configure(cfg)

	return b
}

func waitStatus(t *testing.T, b *telegram.Bot, ok func(ports.TelegramStatus) bool) ports.TelegramStatus {
	t.Helper()

	require.Eventually(t, func() bool { return ok(b.Status()) }, 3*time.Second, 5*time.Millisecond, "status: %+v", b.Status())

	return b.Status()
}

func running(s ports.TelegramStatus) bool { return s.Running }

func TestPolling(t *testing.T) {
	api := newFakeAPI(t)
	got := make(chan ports.TelegramUpdate, 4)

	b := start(t, api, telegram.Config{Enabled: true, Token: token, Mode: telegram.ModePolling},
		func(_ context.Context, u ports.TelegramUpdate) { got <- u })

	status := waitStatus(t, b, running)
	assert.Equal(t, "poly_bot", status.Username)
	assert.Equal(t, telegram.ModePolling, status.Mode)
	assert.Len(t, api.called("deleteWebhook"), 1, "a webhook would keep updates from getUpdates")

	api.push(`{"update_id":1,"message":{"message_id":5,"date":1,"chat":{"id":42,"type":"private"},
		"from":{"id":7,"is_bot":false,"first_name":"Alice","username":"alice_tg"},"text":"/start abc"}}`)
	api.push(`{"update_id":2,"callback_query":{"id":"cb-1","chat_instance":"x","data":"unlink",
		"from":{"id":7,"is_bot":false,"first_name":"Alice"},
		"message":{"message_id":9,"date":1,"chat":{"id":42,"type":"private"},"text":"hi"}}}`)
	api.push(`{"update_id":3,"message":{"message_id":6,"date":1,"chat":{"id":42,"type":"private"},
		"from":{"id":7,"is_bot":false,"first_name":"Alice"},"sticker":{"file_id":"s"}}}`)

	var updates []ports.TelegramUpdate

	for range 2 {
		select {
		case u := <-got:
			updates = append(updates, u)
		case <-time.After(3 * time.Second):
			require.FailNow(t, "no update")
		}
	}

	var message, callback ports.TelegramUpdate

	for _, u := range updates {
		if u.Message != nil {
			message = u
		} else {
			callback = u
		}
	}

	assert.Equal(t, &ports.TelegramIncoming{
		ChatID: 42, Private: true, Text: "/start abc",
		From: ports.TelegramUser{ID: 7, Username: "alice_tg", FirstName: "Alice"},
	}, message.Message)
	assert.Equal(t, &ports.TelegramCallback{
		ID: "cb-1", ChatID: 42, MessageID: 9, Data: "unlink", From: ports.TelegramUser{ID: 7, FirstName: "Alice"},
	}, callback.Callback)

	select {
	case u := <-got:
		assert.Fail(t, "a message without text is not passed on", "%+v", u)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSend(t *testing.T) {
	api := newFakeAPI(t)
	b := start(t, api, telegram.Config{Enabled: true, Token: token, Mode: telegram.ModePolling}, nil)
	waitStatus(t, b, running)
	ctx := context.Background()

	id, err := b.Send(ctx, ports.TelegramMessage{
		ChatID: 42, HTML: "<b>Hi</b>",
		Buttons: [][]ports.TelegramButton{{{Text: "Unlink", Data: "unlink"}, {Text: "Open", URL: "https://poly.example"}}},
	})
	require.NoError(t, err)
	assert.Positive(t, id)

	sent := api.called("sendMessage")[0]
	assert.Equal(t, "42", sent["chat_id"])
	assert.Equal(t, "<b>Hi</b>", sent["text"])
	assert.Equal(t, "HTML", sent["parse_mode"])
	assert.JSONEq(t, `{"inline_keyboard":[[{"text":"Unlink","callback_data":"unlink"},{"text":"Open","url":"https://poly.example"}]]}`, sent["reply_markup"])

	t.Run("a reply keyboard, and taking it away", func(t *testing.T) {
		_, err := b.Send(ctx, ports.TelegramMessage{ChatID: 42, HTML: "Menu", Keyboard: [][]string{{"Workflows", "Status"}}})
		require.NoError(t, err)

		sent := api.called("sendMessage")
		assert.JSONEq(t, `{"keyboard":[[{"text":"Workflows"},{"text":"Status"}]],"is_persistent":true,"resize_keyboard":true}`, sent[len(sent)-1]["reply_markup"])

		_, err = b.Send(ctx, ports.TelegramMessage{ChatID: 42, HTML: "Bye", RemoveKeyboard: true})
		require.NoError(t, err)

		sent = api.called("sendMessage")
		assert.JSONEq(t, `{"remove_keyboard":true}`, sent[len(sent)-1]["reply_markup"])
	})

	t.Run("waits when asked to", func(t *testing.T) {
		api.fail("sendMessage", `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)

		begun := time.Now()
		_, err := b.Send(ctx, ports.TelegramMessage{ChatID: 42, HTML: "again"})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(begun), time.Second)
	})

	t.Run("a blocked bot", func(t *testing.T) {
		api.fail("sendMessage", `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)

		_, err := b.Send(ctx, ports.TelegramMessage{ChatID: 42, HTML: "hello?"})
		require.ErrorIs(t, err, domain.ErrTelegramBlocked)
	})

	t.Run("edits that change nothing are fine", func(t *testing.T) {
		api.fail("editMessageText", `{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`)
		require.NoError(t, b.Edit(ctx, 5, ports.TelegramMessage{ChatID: 42, HTML: "same"}))

		require.NoError(t, b.Edit(ctx, 5, ports.TelegramMessage{ChatID: 42, HTML: "new", Buttons: [][]ports.TelegramButton{{{Text: "x", Data: "y"}}}}))
		edits := api.called("editMessageText")
		assert.Equal(t, "5", edits[len(edits)-1]["message_id"])
		assert.Equal(t, "new", edits[len(edits)-1]["text"])
	})

	t.Run("answering a button", func(t *testing.T) {
		require.NoError(t, b.AnswerCallback(ctx, "cb-1", "Done"))
		answer := api.called("answerCallbackQuery")[0]
		assert.Equal(t, "cb-1", answer["callback_query_id"])
		assert.Equal(t, "Done", answer["text"])
	})
}

func TestStatusAndReconfiguring(t *testing.T) {
	api := newFakeAPI(t)
	b := start(t, api, telegram.Config{Enabled: true, Token: "999:wrong", Mode: telegram.ModePolling}, nil)

	status := waitStatus(t, b, func(s ports.TelegramStatus) bool { return s.Error != "" })
	assert.False(t, status.Running)
	assert.Equal(t, "Telegram refused the bot token; check it with @BotFather", status.Error)

	_, err := b.Send(context.Background(), ports.TelegramMessage{ChatID: 1, HTML: "x"})
	require.ErrorIs(t, err, domain.ErrTelegramUnavailable)

	// Fixing the token starts it, without a restart of anything else.
	b.Configure(telegram.Config{Enabled: true, Token: token, Mode: telegram.ModePolling})
	status = waitStatus(t, b, running)
	assert.Empty(t, status.Error)

	b.Configure(telegram.Config{Enabled: false, Token: token, Mode: telegram.ModePolling})
	status = waitStatus(t, b, func(s ports.TelegramStatus) bool { return !s.Running })
	assert.Equal(t, ports.TelegramStatus{}, status, "switched off is no error")

	b.Configure(telegram.Config{Enabled: true, Mode: telegram.ModePolling})
	status = waitStatus(t, b, func(s ports.TelegramStatus) bool { return s.Error != "" })
	assert.Equal(t, "there is no bot token; add one on the Config page", status.Error)

	b.Configure(telegram.Config{Enabled: true, Token: token, Mode: telegram.ModeWebhook, PublicURL: "http://localhost:3000"})
	status = waitStatus(t, b, func(s ports.TelegramStatus) bool { return strings.Contains(s.Error, "https") })
	assert.Equal(t, "webhook mode needs an https public URL; use polling", status.Error)
}

func TestWebhook(t *testing.T) {
	api := newFakeAPI(t)
	got := make(chan ports.TelegramUpdate, 1)

	b := start(t, api, telegram.Config{Enabled: true, Token: token, Mode: telegram.ModeWebhook, PublicURL: "https://poly.example/"},
		func(_ context.Context, u ports.TelegramUpdate) { got <- u })
	waitStatus(t, b, running)

	set := api.called("setWebhook")
	require.Len(t, set, 1)
	assert.Equal(t, "https://poly.example/api/v1/telegram/webhook", set[0]["url"])
	assert.JSONEq(t, `["message","callback_query"]`, set[0]["allowed_updates"])

	secret := set[0]["secret_token"]
	assert.Len(t, secret, 43)
	assert.Empty(t, api.called("getUpdates"), "no polling in webhook mode")

	update := []byte(`{"update_id":1,"message":{"message_id":5,"date":1,"chat":{"id":42,"type":"private"},
		"from":{"id":7,"is_bot":false,"first_name":"Alice"},"text":"hello"}}`)

	require.ErrorIs(t, b.HandleWebhook(context.Background(), "guess", update), domain.ErrNotFound)
	require.ErrorIs(t, b.HandleWebhook(context.Background(), secret, []byte(`nope`)), domain.ErrInvalidInput)
	require.NoError(t, b.HandleWebhook(context.Background(), secret, update))

	select {
	case u := <-got:
		assert.Equal(t, "hello", u.Message.Text)
	case <-time.After(3 * time.Second):
		require.FailNow(t, "no update")
	}

	// Back to polling: the webhook is removed, and the old secret no longer works.
	b.Configure(telegram.Config{Enabled: true, Token: token, Mode: telegram.ModePolling})
	waitStatus(t, b, func(s ports.TelegramStatus) bool { return s.Running && s.Mode == telegram.ModePolling })
	require.ErrorIs(t, b.HandleWebhook(context.Background(), secret, update), domain.ErrNotFound)
	assert.NotEmpty(t, api.called("deleteWebhook"))

	var check map[string]any
	require.NoError(t, json.Unmarshal(update, &check), "the test's update is valid JSON")
}
