package polymarket

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

// A caller asking for many waits behind another's few, rather than ahead of them (A11).
func TestFairTurns(t *testing.T) {
	f := newFair(rate.NewLimiter(rate.Every(5*time.Millisecond), 1))
	ctx := context.Background()

	var (
		mu    sync.Mutex
		order []string
		wg    sync.WaitGroup
	)

	ask := func(caller string) {
		defer wg.Done()

		require.NoError(t, f.Wait(ctx, caller))

		mu.Lock()
		order = append(order, caller)
		mu.Unlock()
	}

	wg.Add(10)
	for range 10 {
		go ask("busy")
	}

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()

		return len(f.queues["busy"]) >= 8
	}, time.Second, time.Millisecond)

	wg.Add(1)
	go ask("quiet")
	wg.Wait()

	at := -1
	for i, c := range order {
		if c == "quiet" {
			at = i
		}
	}

	assert.LessOrEqual(t, at, 3, "its one went in turn, not after the busy one's ten: %v", order)
}

// A request whose caller gave up is not let through, and says why.
func TestFairGivenUp(t *testing.T) {
	f := newFair(rate.NewLimiter(rate.Every(time.Hour), 1))
	require.NoError(t, f.Wait(context.Background(), "a"), "the burst")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	require.Error(t, f.Wait(ctx, "a"), "it would wait an hour")

	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()

		return !f.running && len(f.queues) == 0
	}, time.Second, time.Millisecond, "nothing left waiting")
}
