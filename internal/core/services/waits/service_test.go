package waits_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/waits"
)

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

// repo keeps waits in memory.
type repo struct {
	mu   sync.Mutex
	recs []ports.WaitRecord
}

func (r *repo) Add(_ context.Context, w ports.WaitRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recs = append(r.recs, w)

	return nil
}

func (r *repo) CountForOwner(context.Context, domain.UserID) (int, error) { return len(r.recs), nil }

func (r *repo) takeWhere(keep func(ports.WaitRecord) bool) []ports.WaitRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	var taken []ports.WaitRecord

	r.recs = slices.DeleteFunc(r.recs, func(w ports.WaitRecord) bool {
		if keep(w) {
			taken = append(taken, w)
			return true
		}

		return false
	})

	return taken
}

func (r *repo) Get(_ context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, w := range r.recs {
		if w.ExecutionID == id && w.OwnerID == owner {
			return w, nil
		}
	}

	return ports.WaitRecord{}, domain.ErrNotFound
}

func (r *repo) Take(_ context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	got := r.takeWhere(func(w ports.WaitRecord) bool { return w.ExecutionID == id && w.OwnerID == owner })
	if len(got) == 0 {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return got[0], nil
}

func (r *repo) TakeWithKey(_ context.Context, owner domain.UserID, id domain.ExecutionID, key string) (ports.WaitRecord, error) {
	got := r.takeWhere(func(w ports.WaitRecord) bool {
		return w.ExecutionID == id && w.OwnerID == owner && w.Key != "" && w.Key == key
	})
	if len(got) == 0 {
		return ports.WaitRecord{}, domain.ErrNotFound
	}

	return got[0], nil
}

func (r *repo) TakeDue(_ context.Context, at time.Time, limit int) ([]ports.WaitRecord, error) {
	n := 0

	return r.takeWhere(func(w ports.WaitRecord) bool {
		if !w.Until.After(at) && n < limit {
			n++
			return true
		}

		return false
	}), nil
}

func (r *repo) TakeForWorkflow(_ context.Context, owner domain.UserID, id domain.WorkflowID) ([]ports.WaitRecord, error) {
	return r.takeWhere(func(w ports.WaitRecord) bool { return w.WorkflowID == id && w.OwnerID == owner }), nil
}

func (r *repo) left() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.recs)
}

// executor records what it was asked to do.
type executor struct {
	mu        sync.Mutex
	continued map[domain.ExecutionID]engine.Resumption
	cancelled map[domain.ExecutionID]string
}

func newExecutor() *executor {
	return &executor{continued: map[domain.ExecutionID]engine.Resumption{}, cancelled: map[domain.ExecutionID]string{}}
}

func (e *executor) Continue(_ context.Context, rec ports.WaitRecord, r engine.Resumption) (engine.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.continued[rec.ExecutionID] = r

	return engine.Result{Execution: domain.Execution{ID: rec.ExecutionID, Status: domain.ExecutionSucceeded}}, nil
}

func (e *executor) Cancel(_ context.Context, rec ports.WaitRecord, reason string) (domain.Execution, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.cancelled[rec.ExecutionID] = reason

	return domain.Execution{ID: rec.ExecutionID, Status: domain.ExecutionCancelled, Error: reason}, nil
}

func (e *executor) resumption(id domain.ExecutionID) (engine.Resumption, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, ok := e.continued[id]

	return r, ok
}

func (e *executor) continuedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()

	return len(e.continued)
}

func waitRec(id domain.ExecutionID, until time.Time) ports.WaitRecord {
	return ports.WaitRecord{ExecutionID: id, WorkflowID: "wf-1", OwnerID: "alice", Until: until}
}

func TestTimer(t *testing.T) {
	r := &repo{}
	for i := range 25 {
		require.NoError(t, r.Add(context.Background(), waitRec(domain.ExecutionID(rune('a'+i)), now.Add(-time.Minute))))
	}

	require.NoError(t, r.Add(context.Background(), waitRec("later", now.Add(time.Hour))))

	ex := newExecutor()
	s := waits.New(r, ex, logging.Noop(), waits.WithClock(func() time.Time { return now }))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		s.Run(ctx)
	}()

	require.Eventually(t, func() bool { return ex.continuedCount() == 25 }, 3*time.Second, 5*time.Millisecond,
		"every due wait, past a batch, right away")
	assert.Equal(t, 1, r.left(), "the one not due waits on")
	assert.True(t, ex.continued["a"].TimedOut)

	cancel()
	<-done
}

func TestAnswersAndCancels(t *testing.T) {
	ctx := context.Background()

	t.Run("an answer with the key carries on the owner's execution, once", func(t *testing.T) {
		r := &repo{}
		asked := waitRec("ex-1", now.Add(time.Hour))
		asked.Key = "k1"
		require.NoError(t, r.Add(ctx, asked))
		require.NoError(t, r.Add(ctx, waitRec("ex-2", now.Add(time.Hour))))

		ex := newExecutor()
		s := waits.New(r, ex, logging.Noop())

		require.ErrorIs(t, s.Answer(ctx, "bob", "ex-1", "k1", nil), domain.ErrNotFound, "someone else's")
		require.ErrorIs(t, s.Answer(ctx, "alice", "ex-1", "k0", nil), domain.ErrNotFound, "an older question's")
		require.ErrorIs(t, s.Answer(ctx, "alice", "ex-2", "", nil), domain.ErrNotFound, "one that takes no answers")

		require.NoError(t, s.Answer(ctx, "alice", "ex-1", "k1", map[string]any{"option": 0}))
		require.Eventually(t, func() bool {
			got, ok := ex.resumption("ex-1")
			return ok && assert.ObjectsAreEqual(engine.Resumption{Payload: map[string]any{"option": 0}}, got)
		}, time.Second, time.Millisecond)

		require.ErrorIs(t, s.Answer(ctx, "alice", "ex-1", "k1", nil), domain.ErrNotFound, "answered already")
		assert.Equal(t, 1, r.left())
	})

	t.Run("no answers once stopped", func(t *testing.T) {
		r := &repo{}
		asked := waitRec("ex-1", now.Add(time.Hour))
		asked.Key = "k1"
		require.NoError(t, r.Add(ctx, asked))

		s := waits.New(r, newExecutor(), logging.Noop(), waits.WithClock(func() time.Time { return now }))

		stop, cancel := context.WithCancel(ctx)
		cancel()
		s.Run(stop)

		require.ErrorIs(t, s.Answer(ctx, "alice", "ex-1", "k1", nil), waits.ErrStopped)
		assert.Equal(t, 1, r.left(), "left for after the restart")
	})

	t.Run("cancelling one, or a workflow's", func(t *testing.T) {
		r := &repo{}
		require.NoError(t, r.Add(ctx, waitRec("ex-1", now.Add(time.Hour))))
		require.NoError(t, r.Add(ctx, waitRec("ex-2", now.Add(time.Hour))))

		other := waitRec("ex-3", now.Add(time.Hour))
		other.WorkflowID = "wf-2"
		require.NoError(t, r.Add(ctx, other))

		ex := newExecutor()
		s := waits.New(r, ex, logging.Noop())

		e, err := s.Cancel(ctx, "alice", "ex-1", "cancelled by you")
		require.NoError(t, err)
		assert.Equal(t, domain.ExecutionCancelled, e.Status)

		require.NoError(t, s.CancelWorkflow(ctx, "alice", "wf-1", "switched off"))
		assert.Equal(t, map[domain.ExecutionID]string{"ex-1": "cancelled by you", "ex-2": "switched off"}, ex.cancelled)
		assert.Equal(t, 1, r.left())

		_, err = s.Cancel(ctx, "alice", "ex-1", "again")
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

// No tasks here.
func (*repo) LeaseTask(context.Context, domain.UserID, ports.TaskLease, time.Time) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}

func (*repo) ReleaseTask(context.Context, domain.UserID, domain.ExecutionID) error {
	return domain.ErrNotFound
}

func (*repo) RenewLeases(context.Context, domain.UserID, string, time.Time) error { return nil }

func (*repo) TakeTask(context.Context, domain.UserID, domain.ExecutionID, string) (ports.WaitRecord, error) {
	return ports.WaitRecord{}, domain.ErrNotFound
}
