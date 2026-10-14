package telegram

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

func TestLimits(t *testing.T) {
	ctx := context.Background()

	t.Run("each chat gets a burst, then its rate", func(t *testing.T) {
		l := newLimits(rate.Limit(20), 2, rate.Inf) // a send every 50 ms per chat after two

		begun := time.Now()
		for range 3 {
			require.NoError(t, l.wait(ctx, 1))
		}

		assert.GreaterOrEqual(t, time.Since(begun), 40*time.Millisecond, "the third waited")

		begun = time.Now()
		require.NoError(t, l.wait(ctx, 2))
		assert.Less(t, time.Since(begun), 20*time.Millisecond, "another chat did not")
	})

	t.Run("a cancelled wait gives up", func(t *testing.T) {
		l := newLimits(rate.Every(time.Hour), 1, rate.Inf)
		require.NoError(t, l.wait(ctx, 1))

		short, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
		defer cancel()

		require.Error(t, l.wait(short, 1))
	})

	t.Run("idle chats are forgotten when there are many", func(t *testing.T) {
		l := newLimits(rate.Inf, 1, rate.Inf)

		for id := range int64(maxChats) {
			l.chat(id)
		}

		for _, c := range l.chats {
			c.used = time.Now().Add(-2 * idleChat)
		}

		l.chat(maxChats + 1)
		assert.Len(t, l.chats, 1)
	})
}
