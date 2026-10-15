package lru_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/lru"
)

func TestCache(t *testing.T) {
	t.Parallel()

	c := lru.New[string, string](2)
	c.Put("a", "A")
	c.Put("b", "B")

	_, ok := c.Get("a") // a is now the most recent
	assert.True(t, ok)

	c.Put("c", "C") // so b goes

	_, ok = c.Get("b")
	assert.False(t, ok)

	for key, want := range map[string]string{"a": "A", "c": "C"} {
		got, ok := c.Get(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got)
	}

	c.Put("a", "A2")
	assert.Equal(t, 2, c.Len(), "putting again does not add")

	got, _ := c.Get("a")
	assert.Equal(t, "A2", got, "but replaces")
}
