package telegram_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/telegram"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/waits"
)

// memWaits keeps waits in memory.
type memWaits struct {
	mu   sync.Mutex
	recs map[domain.ExecutionID]ports.WaitRecord
}

func (m *memWaits) Add(_ context.Context, w ports.WaitRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recs[w.ExecutionID] = w

	return nil
}

func (m *memWaits) CountForOwner(context.Context, domain.UserID) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return len(m.recs), nil
}

func (m *memWaits) take(id domain.ExecutionID, ok func(ports.WaitRecord) bool) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, found := m.recs[id]
	if !found || !ok(w) {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	delete(m.recs, id)

	return w, nil
}

func (m *memWaits) Get(_ context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	w, ok := m.recs[id]
	if !ok || w.OwnerID != owner {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return w, nil
}

func (m *memWaits) Take(_ context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	return m.take(id, func(w ports.WaitRecord) bool { return w.OwnerID == owner })
}

func (m *memWaits) TakeWithKey(_ context.Context, owner domain.UserID, id domain.ExecutionID, key string) (ports.WaitRecord, error) {
	return m.take(id, func(w ports.WaitRecord) bool { return w.OwnerID == owner && key != "" && w.Key == key })
}

func (m *memWaits) TakeDue(context.Context, time.Time, int) ([]ports.WaitRecord, error) {
	return nil, nil
}

func (m *memWaits) TakeForWorkflow(context.Context, domain.UserID, domain.WorkflowID) ([]ports.WaitRecord, error) {
	return nil, nil
}

// capture keeps what reaches it, by node.
type capture struct {
	mu  sync.Mutex
	got map[domain.NodeID][]engine.Item
}

func (*capture) Type() engine.NodeType {
	return engine.NodeType{Name: "test.capture", Inputs: []string{engine.DefaultPort}, Outputs: []string{engine.DefaultPort}}
}

func (c *capture) Run(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.got[rc.NodeID] = append(c.got[rc.NodeID], in[engine.DefaultPort]...)

	return engine.Ports{}, nil
}

func (c *capture) at(id domain.NodeID) []engine.Item {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.got[id]
}

func TestAskFlow(t *testing.T) {
	ctx := context.Background()

	a := &asker{}
	captured := &capture{got: map[domain.NodeID][]engine.Item{}}

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(triggers.Manual{}))
	require.NoError(t, r.RegisterNode(telegram.NewAsk(a, nil, func() time.Time { return askedAt })))
	require.NoError(t, r.RegisterNode(captured))

	repo := &memWaits{recs: map[domain.ExecutionID]ports.WaitRecord{}}
	ex, err := engine.NewExecutor(observability.NoopUtility(), r,
		engine.WithClock(func() time.Time { return askedAt }),
		engine.WithWaits(repo, func() engine.WaitLimits { return engine.WaitLimits{} }),
	)
	require.NoError(t, err)

	w := domain.Workflow{
		ID: "wf-1", OwnerID: owner, Version: 1,
		Nodes: []domain.NodeSpec{
			{ID: "go", Type: triggers.ManualType},
			{ID: "ask", Type: telegram.AskType, Params: map[string]any{"message": "Buy {{question}}?", "buttons": []any{"Buy", "Skip"}}},
			{ID: "bought", Type: "test.capture"},
			{ID: "late", Type: "test.capture"},
		},
		Connections: []domain.Connection{
			{From: "go", FromPort: engine.DefaultPort, To: "ask", ToPort: engine.DefaultPort},
			{From: "ask", FromPort: "Buy", To: "bought", ToPort: engine.DefaultPort},
			{From: "ask", FromPort: "timeout", To: "late", ToPort: engine.DefaultPort},
		},
	}
	require.NoError(t, r.Validate(w), "a button's port can be connected")

	res, err := ex.Execute(ctx, w, "go", items("YES"))
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionWaiting, res.Execution.Status)

	require.Len(t, a.asked, 1)
	key := a.asked[0].Key
	assert.Equal(t, res.Execution.ID, a.asked[0].ExecutionID)
	assert.Equal(t, key, repo.recs[res.Execution.ID].Key, "the wait keeps the key the buttons carry")

	s := waits.New(repo, ex, logging.Noop())

	require.ErrorIs(t, s.Answer(ctx, owner, res.Execution.ID, "other", map[string]any{"option": 0}), domain.ErrNotFound)
	require.ErrorIs(t, s.Answer(ctx, "bob", res.Execution.ID, key, map[string]any{"option": 0}), domain.ErrNotFound)
	require.NoError(t, s.Answer(ctx, owner, res.Execution.ID, key, map[string]any{"option": 0}))

	require.Eventually(t, func() bool { return len(captured.at("bought")) == 1 }, time.Second, time.Millisecond)
	assert.Equal(t, "YES", captured.at("bought")[0].JSON["question"])
	assert.Empty(t, captured.at("late"))
	assert.Equal(t, map[int]string{12: "Buy YES?\n\n✅ <b>Buy</b>"}, a.closed)
}

// No tasks here.
func (*memWaits) LeaseTask(context.Context, domain.UserID, ports.TaskLease, time.Time) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}

func (*memWaits) ReleaseTask(context.Context, domain.UserID, domain.ExecutionID) error {
	return domain.ErrNotFound
}

func (*memWaits) RenewLeases(context.Context, domain.UserID, string, time.Time) error { return nil }

func (*memWaits) TakeTask(context.Context, domain.UserID, domain.ExecutionID, string) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}
