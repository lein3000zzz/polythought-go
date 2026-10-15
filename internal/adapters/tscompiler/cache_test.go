package tscompiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCache(t *testing.T) {
	t.Parallel()

	c := newCache(2)
	c.put("a", "A")
	c.put("b", "B")

	_, ok := c.get("a") // a is now the most recent
	assert.True(t, ok)

	c.put("c", "C") // so b goes

	_, ok = c.get("b")
	assert.False(t, ok)

	for key, want := range map[string]string{"a": "A", "c": "C"} {
		got, ok := c.get(key)
		assert.True(t, ok, key)
		assert.Equal(t, want, got)
	}

	c.put("a", "A")
	assert.Equal(t, 2, c.order.Len(), "putting again does not add")
}
