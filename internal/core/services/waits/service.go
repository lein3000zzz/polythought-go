// Package waits carries on executions that wait: when their time is up, when someone
// answers them, and not at all when they are cancelled. The waits themselves are kept by
// the engine in a WaitRepository; taking one from it is what makes it resume once.
package waits

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	tick     = 2 * time.Second // how often to look for waits whose time is up
	batch    = 20
	parallel = 8 // executions carried on at once by the timer
)

// Executor carries on and cancels paused executions.
type Executor interface {
	Continue(ctx context.Context, rec ports.WaitRecord, r engine.Resumption) (engine.Result, error)
	Cancel(ctx context.Context, rec ports.WaitRecord, reason string) (domain.Execution, error)
}

type Service struct {
	repo     ports.WaitRepository
	executor Executor
	log      logging.Logger
	now      func() time.Time

	slots chan struct{}
	wg    sync.WaitGroup

	mu      sync.Mutex
	base    context.Context // what carried-on executions run under; Run sets it
	stopped bool            // Run returned: answers are not taken any more
}

// ErrStopped means the server is shutting down and takes no answers.
var ErrStopped = errors.New("waits: shutting down")

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(repo ports.WaitRepository, executor Executor, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		repo:     repo,
		executor: executor,
		log:      log.With("component", "waits"),
		now:      time.Now,
		slots:    make(chan struct{}, parallel),
		base:     context.Background(),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Run resumes waits whose time is up, including those that came due while the server was
// down, until ctx is done. Executions it carried on finish, or are cancelled with ctx,
// before it returns.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.base = ctx
	s.mu.Unlock()

	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	defer func() {
		s.mu.Lock()
		s.stopped = true
		s.mu.Unlock()

		s.wg.Wait()
	}()

	for {
		s.resumeDue(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) resumeDue(ctx context.Context) {
	for ctx.Err() == nil {
		due, err := s.repo.TakeDue(ctx, s.now(), batch)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn(ctx, "Waits not checked", logging.Err(err))
			}

			return
		}

		for _, rec := range due {
			s.carryOn(ctx, rec)
		}

		if len(due) < batch {
			return
		}
	}
}

// carryOn continues a timed-out execution on its own goroutine, a few at a time.
func (s *Service) carryOn(ctx context.Context, rec ports.WaitRecord) {
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return
	}

	s.wg.Add(1)

	go func() {
		defer func() {
			<-s.slots
			s.wg.Done()
		}()

		if _, err := s.executor.Continue(ctx, rec, engine.Resumption{TimedOut: true}); err != nil {
			s.log.Error(ctx, "Waiting execution lost", "execution_id", rec.ExecutionID, logging.Err(err))
		}
	}()
}

// Answer carries on one of owner's executions that waits for an answer with key. It
// returns once the wait is taken, so the answer counts, and carries on in the background.
// One that carried on already, is someone else's, or waits with another key is
// domain.ErrNotFound.
func (s *Service) Answer(ctx context.Context, owner domain.UserID, id domain.ExecutionID, key string, payload map[string]any) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return ErrStopped
	}

	s.wg.Add(1) // before Run waits for what it carried on
	base := s.base
	s.mu.Unlock()

	rec, err := s.repo.TakeWithKey(ctx, owner, id, key)
	if err != nil {
		s.wg.Done()
		return err
	}

	// Taken, it must carry on even if whoever answered goes away.
	go func() {
		defer s.wg.Done()

		if _, err := s.executor.Continue(base, rec, engine.Resumption{Payload: payload}); err != nil {
			s.log.Error(base, "Waiting execution lost", "execution_id", id, logging.Err(err))
		}
	}()

	return nil
}

// Waiting is one of owner's waits, left as it is: what it waits for, and what it asks.
func (s *Service) Waiting(ctx context.Context, owner domain.UserID, id domain.ExecutionID) (ports.WaitRecord, error) {
	return s.repo.Get(ctx, owner, id)
}

// Cancel ends one of owner's waiting executions as cancelled, for reason.
func (s *Service) Cancel(ctx context.Context, owner domain.UserID, id domain.ExecutionID, reason string) (domain.Execution, error) {
	rec, err := s.repo.Take(ctx, owner, id)
	if err != nil {
		return domain.Execution{}, err
	}

	return s.executor.Cancel(ctx, rec, reason)
}

// CancelWorkflow cancels every waiting execution of a workflow, for reason.
func (s *Service) CancelWorkflow(ctx context.Context, owner domain.UserID, workflowID domain.WorkflowID, reason string) error {
	recs, err := s.repo.TakeForWorkflow(ctx, owner, workflowID)
	if err != nil {
		return err
	}

	for _, rec := range recs {
		if _, err := s.executor.Cancel(ctx, rec, reason); err != nil {
			s.log.Error(ctx, "Waiting execution not cancelled", "execution_id", rec.ExecutionID, logging.Err(err))
		}
	}

	if len(recs) > 0 {
		s.log.Info(ctx, "Waiting executions cancelled", "workflow_id", workflowID, "count", len(recs), "reason", reason)
	}

	return nil
}
