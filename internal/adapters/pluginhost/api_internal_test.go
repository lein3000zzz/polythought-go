package pluginhost

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCount(t *testing.T) {
	t.Run("a node's run, past its calls, is over", func(t *testing.T) {
		s := &session{limits: Limits{HostCalls: 2}}

		require.NoError(t, s.count())
		require.NoError(t, s.count())
		assert.False(t, s.overLimit())

		require.EqualError(t, s.count(), "rpc error: code = ResourceExhausted desc = past the 2 calls a run may make")
		assert.True(t, s.overLimit())
	})

	t.Run("a trigger's, by the minute", func(t *testing.T) {
		now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
		s := &session{trigger: true, limits: Limits{HostCalls: 2}, now: func() time.Time { return now }}

		require.NoError(t, s.count())
		require.NoError(t, s.count())
		require.Error(t, s.count())
		assert.False(t, s.overLimit(), "refused, not failed: a trigger runs on")

		now = now.Add(59 * time.Second)
		require.Error(t, s.count(), "the same minute")

		now = now.Add(time.Second)
		require.NoError(t, s.count(), "the next")
	})

	t.Run("logs, metrics and spans apart, with a cap of their own", func(t *testing.T) {
		s := &session{limits: Limits{HostCalls: 1}}

		for range maxTelemetry {
			require.NoError(t, s.report())
		}

		require.Error(t, s.report())
		require.NoError(t, s.count(), "the calls untouched")
	})
}

func TestClip(t *testing.T) {
	assert.Equal(t, "short", clip("short"))

	long := clip(string(make([]byte, maxText-1)) + "éé")
	assert.LessOrEqual(t, len(long), maxText+len("…"))
	assert.True(t, len(long) > 0 && long[len(long)-len("…"):] == "…", "cut at a rune's start, marked")
}
