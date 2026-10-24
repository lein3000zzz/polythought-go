package telegram_test

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/telegram"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	now     = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	alice   = domain.User{ID: "alice", Email: "alice@example.com", Role: domain.RoleUser}
	root    = domain.User{ID: "root", Email: "root@example.com", Role: domain.RoleAdmin}
	tgAlice = ports.TelegramUser{ID: 7, Username: "alice_tg", FirstName: "Alice"}
)

// fakeBot records what the bot says.
type fakeBot struct {
	status ports.TelegramStatus

	mu      sync.Mutex
	sent    []ports.TelegramMessage
	edits   []ports.TelegramMessage
	edited  []int // the IDs of the messages edited
	answers []string
	sendErr error
}

func (b *fakeBot) Status() ports.TelegramStatus { return b.status }

func (b *fakeBot) Send(_ context.Context, m ports.TelegramMessage) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sent = append(b.sent, m)

	return len(b.sent), b.sendErr
}

func (b *fakeBot) Edit(_ context.Context, id int, m ports.TelegramMessage) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.edits = append(b.edits, m)
	b.edited = append(b.edited, id)

	return nil
}

func (b *fakeBot) AnswerCallback(_ context.Context, _, text string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.answers = append(b.answers, text)

	return nil
}

func (b *fakeBot) last(t *testing.T) ports.TelegramMessage {
	t.Helper()
	require.NotEmpty(t, b.sent)

	return b.sent[len(b.sent)-1]
}

// memLinks keeps links and codes in memory.
type memLinks struct {
	links map[domain.UserID]domain.TelegramLink
	codes map[string]code
}

type code struct {
	user    domain.UserID
	expires time.Time
}

func newLinks() *memLinks {
	return &memLinks{links: make(map[domain.UserID]domain.TelegramLink), codes: make(map[string]code)}
}

func (m *memLinks) ForUser(_ context.Context, user domain.UserID) (domain.TelegramLink, error) {
	l, ok := m.links[user]
	if !ok {
		return domain.TelegramLink{}, domain.ErrNotFound
	}

	return l, nil
}

func (m *memLinks) ForTelegramUser(_ context.Context, id int64) (domain.TelegramLink, error) {
	for _, l := range m.links {
		if l.TelegramUserID == id {
			return l, nil
		}
	}

	return domain.TelegramLink{}, domain.ErrNotFound
}

func (m *memLinks) Save(_ context.Context, link domain.TelegramLink) error {
	for user, l := range m.links {
		if l.TelegramUserID == link.TelegramUserID {
			delete(m.links, user)
		}
	}

	m.links[link.UserID] = link

	return nil
}

func (m *memLinks) Delete(_ context.Context, user domain.UserID) error {
	if _, ok := m.links[user]; !ok {
		return domain.ErrNotFound
	}

	delete(m.links, user)

	return nil
}

func (m *memLinks) SaveCode(_ context.Context, user domain.UserID, hash []byte, expires time.Time) error {
	for k, c := range m.codes {
		if c.user == user {
			delete(m.codes, k)
		}
	}

	m.codes[string(hash)] = code{user: user, expires: expires}

	return nil
}

func (m *memLinks) ConsumeCode(_ context.Context, hash []byte, at time.Time) (domain.UserID, error) {
	c, ok := m.codes[string(hash)]
	delete(m.codes, string(hash))

	if !ok || !c.expires.After(at) {
		return "", domain.ErrNotFound
	}

	return c.user, nil
}

type deps struct {
	bot   *fakeBot
	links *memLinks
	users *portsmocks.MockUserRepository
	clock *time.Time
}

func newService(t *testing.T) (*telegram.Service, deps) {
	t.Helper()

	clock := now
	d := deps{
		bot:   &fakeBot{status: ports.TelegramStatus{Running: true, Username: "poly_bot", Mode: "polling"}},
		links: newLinks(),
		users: portsmocks.NewMockUserRepository(t),
		clock: &clock,
	}

	d.users.EXPECT().ByID(mock.Anything, alice.ID).Return(alice, nil).Maybe()

	return telegram.New(d.bot, d.links, d.users, logging.Noop(), telegram.WithClock(func() time.Time { return *d.clock })), d
}

func message(text string) ports.TelegramUpdate {
	return ports.TelegramUpdate{Message: &ports.TelegramIncoming{ChatID: 70, Private: true, From: tgAlice, Text: text}}
}

// linkCode makes a link for p and returns the code in it.
func linkCode(t *testing.T, s *telegram.Service, p auth.Principal) string {
	t.Helper()

	link, err := s.CreateLink(context.Background(), p)
	require.NoError(t, err)

	u, err := url.Parse(link.URL)
	require.NoError(t, err)

	return u.Query().Get("start")
}

func TestLinking(t *testing.T) {
	ctx := context.Background()
	p := auth.Principal{User: alice, SessionID: "s-1"}

	t.Run("a link opened in Telegram links that account", func(t *testing.T) {
		s, d := newService(t)

		link, err := s.CreateLink(ctx, p)
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(link.URL, "https://t.me/poly_bot?start="))
		assert.Equal(t, now.Add(10*time.Minute), link.ExpiresAt)

		code := linkCode(t, s, p)
		assert.Len(t, code, 32)
		assert.Len(t, d.links.codes, 1, "a new link replaces the last")

		for k := range d.links.codes {
			assert.NotContains(t, k, code, "only the hash is kept")
		}

		s.HandleUpdate(ctx, message("/start "+code))

		saved, err := d.links.ForUser(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70, Username: "alice_tg", FirstName: "Alice", LinkedAt: now}, saved)

		reply := d.bot.last(t)
		assert.Equal(t, int64(70), reply.ChatID)
		assert.Contains(t, reply.HTML, "Linked to <b>alice@example.com</b>")
		assert.Equal(t, [][]string{{"📋 Workflows", "👥 Traders"}, {"💼 Positions", "📊 Status"}, {"🔔 Notifications"}}, reply.Keyboard, "the menu")

		status, err := s.Status(ctx, p)
		require.NoError(t, err)
		assert.True(t, status.Available)
		assert.Equal(t, "poly_bot", status.BotUsername)
		assert.Equal(t, &saved, status.Link)

		// The code works once.
		s.HandleUpdate(ctx, message("/start "+code))
		assert.Contains(t, d.bot.last(t).HTML, "expired or was already used")
	})

	t.Run("codes expire", func(t *testing.T) {
		s, d := newService(t)
		code := linkCode(t, s, p)

		*d.clock = now.Add(10 * time.Minute)
		s.HandleUpdate(ctx, message("/start "+code))

		assert.Contains(t, d.bot.last(t).HTML, "expired or was already used")
		assert.Empty(t, d.links.links)
	})

	t.Run("a disabled account is not linked", func(t *testing.T) {
		s, d := newService(t)
		bob := domain.User{ID: "bob", Email: "bob@example.com", DisabledAt: now}
		d.users.EXPECT().ByID(mock.Anything, bob.ID).Return(bob, nil)

		s.HandleUpdate(ctx, message("/start "+linkCode(t, s, auth.Principal{User: bob})))

		assert.Equal(t, "Your Polythought account is disabled.", d.bot.last(t).HTML)
		assert.Empty(t, d.links.links)
	})

	t.Run("linked to another account, it moves only once asked, both named (R10)", func(t *testing.T) {
		s, d := newService(t)
		bob := domain.User{ID: "bob", Email: "bob@example.com"}
		d.users.EXPECT().ByID(mock.Anything, bob.ID).Return(bob, nil)

		s.HandleUpdate(ctx, message("/start "+linkCode(t, s, p)))
		require.Len(t, d.links.links, 1)

		// Someone else's link, opened by alice's Telegram.
		s.HandleUpdate(ctx, message("/start "+linkCode(t, s, auth.Principal{User: bob})))
		asked := d.bot.last(t)
		assert.Contains(t, asked.HTML, "This Telegram is linked to <b>alice@example.com</b>. Link it to <b>bob@example.com</b> instead?")
		assert.Equal(t, [][]string{{"Link to bob@example.com=lk:yes", "Keep alice@example.com=lk:no"}}, buttonsOf(asked))

		saved, err := d.links.ForTelegramUser(ctx, 7)
		require.NoError(t, err)
		assert.Equal(t, alice.ID, saved.UserID, "not moved yet")

		s.HandleUpdate(ctx, press("lk:no"))
		saved, err = d.links.ForTelegramUser(ctx, 7)
		require.NoError(t, err)
		assert.Equal(t, alice.ID, saved.UserID, "kept")

		s.HandleUpdate(ctx, message("/start "+linkCode(t, s, auth.Principal{User: bob})))
		s.HandleUpdate(ctx, press("lk:yes"))
		saved, err = d.links.ForTelegramUser(ctx, 7)
		require.NoError(t, err)
		assert.Equal(t, bob.ID, saved.UserID, "moved once asked")
		assert.Contains(t, d.bot.last(t).HTML, "Linked to <b>bob@example.com</b>")
	})

	t.Run("no link without a running bot", func(t *testing.T) {
		s, d := newService(t)
		d.bot.status = ports.TelegramStatus{Error: "Telegram refused the bot token"}

		_, err := s.CreateLink(ctx, p)
		require.ErrorIs(t, err, domain.ErrTelegramUnavailable)

		status, err := s.Status(ctx, p)
		require.NoError(t, err)
		assert.False(t, status.Available)
		assert.Empty(t, status.Problem, "only admins see why")

		status, err = s.Status(ctx, auth.Principal{User: root})
		require.NoError(t, err)
		assert.Equal(t, "Telegram refused the bot token", status.Problem)
	})
}

func TestMessages(t *testing.T) {
	ctx := context.Background()

	t.Run("strangers get instructions", func(t *testing.T) {
		s, d := newService(t)

		s.HandleUpdate(ctx, message("hello"))
		assert.Contains(t, d.bot.last(t).HTML, "Settings → Telegram")
		assert.Nil(t, d.bot.last(t).Buttons)
	})

	t.Run("linked users are told who they are", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		s.HandleUpdate(ctx, message("/start@poly_bot"))
		assert.Contains(t, d.bot.last(t).HTML, "linked to <b>alice@example.com</b>")
	})

	t.Run("groups only hear about private chats", func(t *testing.T) {
		s, d := newService(t)

		s.HandleUpdate(ctx, ports.TelegramUpdate{Message: &ports.TelegramIncoming{ChatID: -100, From: tgAlice, Text: "chatter"}})
		assert.Empty(t, d.bot.sent)

		s.HandleUpdate(ctx, ports.TelegramUpdate{Message: &ports.TelegramIncoming{ChatID: -100, From: tgAlice, Text: "/start"}})
		assert.Equal(t, "Message me in a private chat to link your account.", d.bot.last(t).HTML)
	})

	t.Run("names are escaped", func(t *testing.T) {
		s, d := newService(t)
		odd := domain.User{ID: "odd", Email: "<b>@example.com"}
		d.users.EXPECT().ByID(mock.Anything, odd.ID).Return(odd, nil)

		s.HandleUpdate(ctx, message("/start "+linkCode(t, s, auth.Principal{User: odd})))
		assert.Contains(t, d.bot.last(t).HTML, "&lt;b&gt;@example.com")
	})
}

func TestUnlinking(t *testing.T) {
	ctx := context.Background()
	p := auth.Principal{User: alice, SessionID: "s-1"}

	t.Run("from the app, telling the chat", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		require.NoError(t, s.Unlink(ctx, p))
		assert.Empty(t, d.links.links)
		assert.Contains(t, d.bot.last(t).HTML, "no longer linked")
		assert.Equal(t, int64(70), d.bot.last(t).ChatID)
		assert.True(t, d.bot.last(t).RemoveKeyboard, "the menu goes too")

		require.ErrorIs(t, s.Unlink(ctx, p), domain.ErrNotFound)
	})

	t.Run("from the app, when the chat blocked the bot", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))
		d.bot.sendErr = domain.ErrTelegramBlocked

		require.NoError(t, s.Unlink(ctx, p))
		assert.Empty(t, d.links.links)
	})

	t.Run("from Telegram, with the button", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		s.HandleUpdate(ctx, ports.TelegramUpdate{Callback: &ports.TelegramCallback{ID: "cb", ChatID: 70, MessageID: 3, From: tgAlice, Data: "unlink"}})

		assert.Empty(t, d.links.links)
		require.Len(t, d.bot.edits, 1)
		assert.Contains(t, d.bot.edits[0].HTML, "Unlinked.")
		assert.Nil(t, d.bot.edits[0].Buttons)
		assert.Equal(t, []string{"Unlinked"}, d.bot.answers)
		assert.True(t, d.bot.last(t).RemoveKeyboard, "the menu goes too")

		// Pressing it again, or anyone else pressing anything, gets a short answer.
		s.HandleUpdate(ctx, ports.TelegramUpdate{Callback: &ports.TelegramCallback{ID: "cb2", From: tgAlice, Data: "unlink"}})
		assert.Equal(t, "This chat is not linked to Polythought.", d.bot.answers[1])
	})

	t.Run("unknown buttons", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		s.HandleUpdate(ctx, ports.TelegramUpdate{Callback: &ports.TelegramCallback{ID: "cb", From: tgAlice, Data: "w:old"}})
		assert.Equal(t, []string{"This button no longer works."}, d.bot.answers)
		assert.Len(t, d.links.links, 1)
	})
}

func TestMessageUser(t *testing.T) {
	ctx := context.Background()

	t.Run("to the linked chat", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		require.NoError(t, s.MessageUser(ctx, alice.ID, ports.UserMessage{HTML: "<b>Hi</b>", Silent: true}))
		assert.Equal(t, ports.TelegramMessage{ChatID: 70, HTML: "<b>Hi</b>", Silent: true}, d.bot.last(t))

		require.NoError(t, s.MessageUser(ctx, alice.ID, ports.UserMessage{HTML: "Still open", ReplyTo: 3}))
		assert.Equal(t, ports.TelegramMessage{ChatID: 70, HTML: "Still open", ReplyTo: 3}, d.bot.last(t))
	})

	t.Run("nowhere to write to", func(t *testing.T) {
		s, _ := newService(t)

		require.ErrorIs(t, s.MessageUser(ctx, alice.ID, ports.UserMessage{HTML: "hi"}), domain.ErrTelegramNotLinked)
	})

	t.Run("a chat that blocked the bot is unlinked", func(t *testing.T) {
		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))
		d.bot.sendErr = domain.ErrTelegramBlocked

		require.ErrorIs(t, s.MessageUser(ctx, alice.ID, ports.UserMessage{HTML: "hi"}), domain.ErrTelegramBlocked)
		assert.Empty(t, d.links.links)
	})
}

func TestInbox(t *testing.T) {
	ctx := context.Background()
	reports := func(text string) bool { return strings.HasPrefix(text, "/report") }

	linked := func(t *testing.T) (*telegram.Service, deps) {
		t.Helper()

		s, d := newService(t)
		require.NoError(t, d.links.Save(ctx, domain.TelegramLink{UserID: alice.ID, TelegramUserID: 7, ChatID: 70}))

		return s, d
	}

	t.Run("matching messages go to the workflow, and the bot keeps quiet", func(t *testing.T) {
		s, d := linked(t)
		messages, cancel := s.Subscribe(alice.ID, reports)
		defer cancel()

		s.HandleUpdate(ctx, message("/report weekly"))

		got := <-messages
		assert.Equal(t, ports.TelegramInboxMessage{Text: "/report weekly", Username: "alice_tg", FirstName: "Alice", At: now}, got)
		assert.Empty(t, d.bot.sent)
	})

	t.Run("other messages get the usual answer", func(t *testing.T) {
		s, d := linked(t)
		messages, cancel := s.Subscribe(alice.ID, reports)
		defer cancel()

		s.HandleUpdate(ctx, message("hello"))
		s.HandleUpdate(ctx, message("/start"))
		s.HandleUpdate(ctx, message("/menu"))

		assert.Len(t, d.bot.sent, 3)
		assert.Empty(t, messages)
	})

	t.Run("only the owner's messages, only from linked users", func(t *testing.T) {
		s, _ := newService(t) // nobody linked
		everything := func(string) bool { return true }
		messages, cancel := s.Subscribe(alice.ID, everything)
		defer cancel()

		s.HandleUpdate(ctx, message("/report"))
		assert.Empty(t, messages)
	})

	t.Run("a busy workflow misses what does not fit", func(t *testing.T) {
		s, d := linked(t)
		messages, cancel := s.Subscribe(alice.ID, reports)

		for range 20 {
			s.HandleUpdate(ctx, message("/report"))
		}

		assert.Len(t, messages, 16)
		assert.Empty(t, d.bot.sent, "matched, though dropped")

		cancel()
		cancel() // twice is fine

		for range messages {
		}
	})
}

// buttonsOf lists a message's buttons row by row, as text=data.
func buttonsOf(m ports.TelegramMessage) [][]string {
	out := make([][]string, len(m.Buttons))
	for i, row := range m.Buttons {
		for _, b := range row {
			out[i] = append(out[i], b.Text+"="+b.Data)
		}
	}

	return out
}

// The linked chat confirms what agents and tokens ask for, so linking it is the user's, at
// a browser, and the chat it replaces hears of the move (A1).
func TestLinkingIsTheUsers(t *testing.T) {
	ctx := context.Background()
	user := auth.Principal{User: alice, SessionID: "s-1"}

	t.Run("not a token's, nor an agent's", func(t *testing.T) {
		s, d := newService(t)

		for _, p := range []auth.Principal{
			{User: alice, TokenID: "t-1", Scopes: []domain.Scope{domain.ScopeSettings}},
			{User: alice, TokenID: "t-0"}, // made before scopes: every one
			{User: alice, GrantID: "g-1", Agent: "Claude Code", Scopes: []domain.Scope{domain.ScopeSettings}},
		} {
			_, err := s.CreateLink(ctx, p)
			require.ErrorIs(t, err, domain.ErrForbidden)
			require.ErrorIs(t, s.Unlink(ctx, p), domain.ErrForbidden)
		}

		assert.Empty(t, d.links.codes)
	})

	t.Run("the chat it replaces is told", func(t *testing.T) {
		s, d := newService(t)
		s.HandleUpdate(ctx, message("/start "+linkCode(t, s, user)))

		mallory := ports.TelegramUser{ID: 9, Username: "mallory", FirstName: "M"}
		s.HandleUpdate(ctx, ports.TelegramUpdate{Message: &ports.TelegramIncoming{
			ChatID: 90, Private: true, From: mallory, Text: "/start " + linkCode(t, s, user),
		}})

		saved, err := d.links.ForUser(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(90), saved.ChatID)

		d.bot.mu.Lock()
		defer d.bot.mu.Unlock()

		var told bool
		for _, m := range d.bot.sent {
			if m.ChatID == 70 && strings.Contains(m.HTML, "is now linked to another Telegram chat") {
				told = true
			}
		}

		assert.True(t, told, "the old chat hears of it")
	})
}
