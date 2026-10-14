package logic_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
)

func TestWait(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	node := logic.NewWait(func() time.Time { return now })

	wait := func(t *testing.T, params map[string]any) (*engine.Wait, error) {
		t.Helper()

		_, err := node.Run(context.Background(), engine.RunContext{Params: engine.LiteralParams(node.Type(), params)}, engine.Ports{})

		var w *engine.Wait
		if assert.ErrorAs(t, err, &w) {
			return w, nil
		}

		return nil, err
	}

	cases := []struct {
		name   string
		params map[string]any
		want   time.Time
	}{
		{name: "ten minutes by default", want: now.Add(10 * time.Minute)},
		{name: "a while", params: map[string]any{"for": "90s"}, want: now.Add(90 * time.Second)},
		{name: "later today", params: map[string]any{"mode": "until", "until": "18:30"}, want: now.Add(6*time.Hour + 30*time.Minute)},
		{name: "tomorrow, past today's", params: map[string]any{"mode": "until", "until": "09:00"}, want: now.Add(21 * time.Hour)},
		{name: "this very minute is tomorrow's", params: map[string]any{"mode": "until", "until": "12:00"}, want: now.Add(24 * time.Hour)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := wait(t, tc.params)
			require.NoError(t, err)
			assert.Equal(t, tc.want, w.Until)
		})
	}

	t.Run("a time that is not one", func(t *testing.T) {
		_, err := node.Run(context.Background(), engine.RunContext{
			Params: engine.LiteralParams(node.Type(), map[string]any{"mode": "until", "until": "noon"}),
		}, engine.Ports{})
		require.ErrorIs(t, err, engine.ErrInvalidParam)
		assert.ErrorContains(t, err, `"noon" is not a time of day like 09:30`)
	})

	t.Run("passes the items on when the wait ends", func(t *testing.T) {
		in := engine.Ports{engine.DefaultPort: {{JSON: map[string]any{"q": "A"}}, {JSON: map[string]any{"q": "B"}}}}

		out, err := node.Resume(context.Background(), engine.RunContext{}, in, engine.Resumption{TimedOut: true})
		require.NoError(t, err)
		require.Len(t, out[engine.DefaultPort], 2)
		assert.Equal(t, "B", out[engine.DefaultPort][1].JSON["q"])
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, out[engine.DefaultPort][1].Source)
	})
}
