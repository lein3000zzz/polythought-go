// Package lru keeps the most recently used values, up to a number of them.
package lru

import (
	"container/list"
	"sync"
)

// Cache is safe to use from several goroutines.
type Cache[K comparable, V any] struct {
	mu    sync.Mutex
	size  int
	order *list.List // of entries, most recent first
	byKey map[K]*list.Element
}

type entry[K comparable, V any] struct {
	key   K
	value V
}

func New[K comparable, V any](size int) *Cache[K, V] {
	return &Cache[K, V]{size: size, order: list.New(), byKey: make(map[K]*list.Element, size)}
}

func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.byKey[key]
	if !ok {
		var zero V
		return zero, false
	}

	c.order.MoveToFront(el)

	return el.Value.(*entry[K, V]).value, true
}

// Put keeps value under key, forgetting the least recently used one past the size.
func (c *Cache[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.byKey[key]; ok {
		el.Value.(*entry[K, V]).value = value
		c.order.MoveToFront(el)

		return
	}

	c.byKey[key] = c.order.PushFront(&entry[K, V]{key: key, value: value})

	if c.order.Len() > c.size {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.byKey, oldest.Value.(*entry[K, V]).key)
	}
}

// Len is how many values it keeps.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.order.Len()
}
