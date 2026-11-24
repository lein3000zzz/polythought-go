package triggers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// inbox hands the trigger the calls a test sends it.
type inbox struct {
	calls     chan ports.HookCall
	listening chan struct{}
	cancelled chan struct{}
	asked     domain.NodeID
}

func (i *inbox) Listen(_ domain.WorkflowID, node domain.NodeID) (<-chan ports.HookCall, func()) {
	i.asked = node
	close(i.listening)

	return i.calls, func() { close(i.cancelled) }
}

func TestWebhook(t *testing.T) {
	in := &inbox{calls: make(chan ports.HookCall, 1), listening: make(chan struct{}), cancelled: make(chan struct{})}
	node := triggers.NewWebhook(in)

	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.FixedZone("EST", -5*3600))
	in.calls <- ports.HookCall{Execution: "ex-1", Body: map[string]any{"price": json.Number("0.53")}, Query: map[string]any{"city": "nyc"}, At: at}

	got := make(chan engine.Ports, 1)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- node.Start(ctx, engine.TriggerContext{WorkflowID: "wf", NodeID: "hook"}, func(_ context.Context, out engine.Ports) error {
			got <- out
			return nil
		})
	}()

	select {
	case out := <-got:
		require.NoError(t, shape.Check(node.Type(), out))
		assert.Equal(t, map[string]any{
			"body": map[string]any{"price": json.Number("0.53")}, "query": map[string]any{"city": "nyc"}, "receivedAt": at.UTC(),
		}, out[engine.DefaultPort][0].JSON)
	case <-time.After(waitFor):
		t.Fatal("no run")
	}

	assert.Equal(t, domain.NodeID("hook"), in.asked)

	stop()
	require.NoError(t, <-done)

	select {
	case <-in.cancelled:
	case <-time.After(waitFor):
		t.Fatal("it went on listening")
	}

	assert.Equal(t, map[string]any{"body": nil, "query": map[string]any{}, "receivedAt": at.UTC()},
		triggers.WebhookOutput(ports.HookCall{At: at})[engine.DefaultPort][0].JSON, "a call with nothing in it")
}
