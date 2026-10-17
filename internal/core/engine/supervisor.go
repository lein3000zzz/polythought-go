package engine

import (
	"context"
	"sync"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Supervisor keeps one Runner per active workflow. Starting a workflow that is already
// running restarts it with the new definition.
type Supervisor struct {
	runner *Runner
	log    logging.Logger
	root   context.Context

	mu      sync.Mutex
	running map[domain.WorkflowID]*run
	wg      sync.WaitGroup
}

type run struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSupervisor runs workflows under root; cancelling root stops all of them.
func NewSupervisor(root context.Context, runner *Runner, log logging.Logger) *Supervisor {
	return &Supervisor{
		runner:  runner,
		log:     log.With("component", "supervisor"),
		root:    root,
		running: make(map[domain.WorkflowID]*run),
	}
}

// Start runs w until Stop or shutdown, replacing a previous run of the same workflow.
// It returns once the old run has stopped, so two versions never run side by side.
func (s *Supervisor) Start(w domain.Workflow) {
	s.start(w, false)
}

// Resume starts a workflow that was running before, as at a server start or when it is
// saved while active, so its triggers may catch up on what they missed meanwhile.
func (s *Supervisor) Resume(w domain.Workflow) {
	s.start(w, true)
}

func (s *Supervisor) start(w domain.Workflow, resumed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopLocked(w.ID)

	ctx, cancel := context.WithCancel(s.root)
	r := &run{cancel: cancel, done: make(chan struct{})}
	s.running[w.ID] = r

	s.wg.Go(func() {
		defer close(r.done)

		if err := s.runner.Run(ctx, w, resumed); err != nil {
			s.log.Error(ctx, "Workflow stopped with an error", "workflow_id", w.ID, "owner_id", w.OwnerID, logging.Err(err))
		}
	})

	s.log.Info(s.root, "Workflow started", "workflow_id", w.ID, "version", w.Version, "resumed", resumed)
}

// Stop ends a workflow's run and waits for it. Stopping a workflow that is not running
// does nothing.
func (s *Supervisor) Stop(id domain.WorkflowID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.stopLocked(id)
}

func (s *Supervisor) stopLocked(id domain.WorkflowID) {
	r, ok := s.running[id]
	if !ok {
		return
	}

	r.cancel()
	<-r.done
	delete(s.running, id)

	s.log.Info(s.root, "Workflow stopped", "workflow_id", id)
}

// Running reports whether a workflow currently has a live run.
func (s *Supervisor) Running(id domain.WorkflowID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.running[id]
	if !ok {
		return false
	}

	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

// Shutdown stops every workflow and waits for them. It matches observability.ShutdownFunc.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	for _, r := range s.running {
		r.cancel()
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
