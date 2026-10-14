package telegram

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// maxChats bounds the per-chat limiters kept; beyond it, idle ones are dropped.
const (
	maxChats = 1024
	idleChat = time.Minute
)

// limits keeps sends under Telegram's rates. A send waits its turn: per chat, then
// overall, so a burst to one chat does not hold up the others for long.
type limits struct {
	global  *rate.Limiter
	perChat rate.Limit
	burst   int

	mu    sync.Mutex
	chats map[int64]*chatLimit
}

type chatLimit struct {
	*rate.Limiter
	used time.Time
}

func newLimits(perChat rate.Limit, burst int, global rate.Limit) *limits {
	return &limits{
		global:  rate.NewLimiter(global, int(global)),
		perChat: perChat,
		burst:   burst,
		chats:   make(map[int64]*chatLimit),
	}
}

func (l *limits) wait(ctx context.Context, chatID int64) error {
	if err := l.chat(chatID).Wait(ctx); err != nil {
		return err
	}

	return l.global.Wait(ctx)
}

func (l *limits) chat(id int64) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()

	c, ok := l.chats[id]
	if !ok {
		if len(l.chats) >= maxChats {
			for chat, c := range l.chats {
				if now.Sub(c.used) > idleChat {
					delete(l.chats, chat)
				}
			}
		}

		c = &chatLimit{Limiter: rate.NewLimiter(l.perChat, l.burst)}
		l.chats[id] = c
	}

	c.used = now

	return c.Limiter
}
