package triggers_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

const waitFor = 3 * time.Second

// recorder collects emitted ports and can fail every emit.
type recorder struct {
	mu      sync.Mutex
	emitted []engine.Ports
	err     error
}

func (r *recorder) emit(_ context.Context, out engine.Ports) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.emitted = append(r.emitted, out)

	return r.err
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.emitted)
}

func (r *recorder) first() engine.Ports {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.emitted[0]
}

func triggerContext(t *testing.T, typ engine.NodeType, params map[string]any) engine.TriggerContext {
	t.Helper()

	p, err := engine.LiteralParams(typ, params).Resolve(engine.ItemRef{})
	require.NoError(t, err)

	return engine.TriggerContext{WorkflowID: "wf", NodeID: "every", Params: p}
}

func TestScheduleConfig(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr error
	}{
		{name: "missing interval", params: nil, wantErr: engine.ErrMissingParam},
		{name: "not a duration", params: map[string]any{"interval": "soon"}, wantErr: engine.ErrInvalidParam},
		{name: "below the minimum", params: map[string]any{"interval": "1ms"}, wantErr: engine.ErrInvalidParam},
		{name: "runOnStart not a bool", params: map[string]any{"interval": "1h", "runOnStart": "yes"}, wantErr: engine.ErrInvalidParam},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := triggers.NewSchedule(10 * time.Millisecond)
			rec := &recorder{}

			err := s.Start(context.Background(), triggerContext(t, s.Type(), tt.params), rec.emit)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Zero(t, rec.count(), "a misconfigured schedule never fires")
		})
	}
}

func TestScheduleFires(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]any
		emitErr   error
		wantFires int
	}{
		{name: "runs on start before the first tick", params: map[string]any{"interval": "1h"}, wantFires: 1},
		{name: "fires every interval", params: map[string]any{"interval": "20ms", "runOnStart": false}, wantFires: 3},
		{name: "keeps firing after a failed emit", params: map[string]any{"interval": "20ms"}, emitErr: errors.New("invalid workflow"), wantFires: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := triggers.NewSchedule(10 * time.Millisecond)
			rec := &recorder{err: tt.emitErr}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)

			go func() { done <- s.Start(ctx, triggerContext(t, s.Type(), tt.params), rec.emit) }()

			require.Eventually(t, func() bool { return rec.count() >= tt.wantFires }, waitFor, time.Millisecond)
			cancel()
			require.NoError(t, <-done)

			items := rec.first()[engine.DefaultPort]
			require.Len(t, items, 1)
			assert.IsType(t, time.Time{}, items[0].JSON["firedAt"])
		})
	}
}

func TestManual(t *testing.T) {
	m := triggers.Manual{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- m.Start(ctx, engine.TriggerContext{}, nil) }()

	select {
	case <-done:
		require.FailNow(t, "manual trigger returned before cancellation")
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	require.NoError(t, <-done)

	out := triggers.ManualOutput()
	assert.Equal(t, 1, out.Len())
	assert.Equal(t, triggers.ManualType, m.Type().Name)
	require.NoError(t, shape.Check(m.Type(), out), "items as declared")
	require.NoError(t, shape.Check(triggers.NewSchedule(time.Second).Type(), triggers.ScheduleOutput(time.Now())))
}
