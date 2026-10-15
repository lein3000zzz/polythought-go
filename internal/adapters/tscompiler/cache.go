package tscompiler

import (
	"container/list"
	"sync"
)

// cache keeps the most recently used scripts by hash, up to a number of them.
type cache struct {
	mu    sync.Mutex
	size  int
	order *list.List // of entries, most recent first
	byKey map[string]*list.Element
}

type entry struct {
	key, script string
}

func newCache(size int) *cache {
	return &cache{size: size, order: list.New(), byKey: make(map[string]*list.Element, size)}
}

func (c *cache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.byKey[key]
	if !ok {
		return "", false
	}

	c.order.MoveToFront(el)

	return el.Value.(*entry).script, true
}

func (c *cache) put(key, script string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.byKey[key]; ok {
		c.order.MoveToFront(el)
		return
	}

	c.byKey[key] = c.order.PushFront(&entry{key: key, script: script})

	if c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.byKey, oldest.Value.(*entry).key)
	}
}
