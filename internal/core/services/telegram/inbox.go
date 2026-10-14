package telegram

import (
	"sync"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// inboxBuffer is how many messages wait for a busy workflow; more are dropped.
const inboxBuffer = 16

// inbox hands linked users' messages to the workflows that subscribed to them.
type inbox struct {
	mu   sync.Mutex
	subs map[domain.UserID]map[*subscriber]struct{}
}

type subscriber struct {
	match func(text string) bool
	ch    chan ports.TelegramInboxMessage
}

func newInbox() *inbox {
	return &inbox{subs: make(map[domain.UserID]map[*subscriber]struct{})}
}

func (i *inbox) subscribe(owner domain.UserID, match func(string) bool) (<-chan ports.TelegramInboxMessage, func()) {
	sub := &subscriber{match: match, ch: make(chan ports.TelegramInboxMessage, inboxBuffer)}

	i.mu.Lock()
	if i.subs[owner] == nil {
		i.subs[owner] = make(map[*subscriber]struct{})
	}

	i.subs[owner][sub] = struct{}{}
	i.mu.Unlock()

	var once sync.Once

	return sub.ch, func() {
		once.Do(func() {
			i.mu.Lock()
			defer i.mu.Unlock()

			delete(i.subs[owner], sub)
			if len(i.subs[owner]) == 0 {
				delete(i.subs, owner)
			}

			close(sub.ch)
		})
	}
}

// deliver hands m to every subscriber of owner it matches, without waiting for any. It
// reports whether one matched, and how many were too busy to take it.
func (i *inbox) deliver(owner domain.UserID, m ports.TelegramInboxMessage) (matched bool, dropped int) {
	i.mu.Lock()
	defer i.mu.Unlock()

	for sub := range i.subs[owner] {
		if !sub.match(m.Text) {
			continue
		}

		matched = true

		select {
		case sub.ch <- m:
		default:
			dropped++
		}
	}

	return matched, dropped
}
