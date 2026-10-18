// Package memory shows what nodes remember to their workflow's owner, forgets it on
// request, and sweeps away what has expired (D121, D137).
package memory

import (
	"context"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Workflows tells whose a workflow is.
type Workflows interface {
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
}

type Service struct {
	memory    ports.NodeMemory
	workflows Workflows
	log       logging.Logger
	now       func() time.Time
}

func New(memory ports.NodeMemory, workflows Workflows, log logging.Logger) *Service {
	return &Service{memory: memory, workflows: workflows, log: log.With("component", "node-memory"), now: time.Now}
}

// WithClock replaces time.Now, for tests.
func (s *Service) WithClock(now func() time.Time) *Service {
	s.now = now
	return s
}

// Count is how many keys a node of one of p's workflows remembers.
func (s *Service) Count(ctx context.Context, p auth.Principal, id domain.WorkflowID, node domain.NodeID) (int, error) {
	if _, err := s.workflows.Get(ctx, p, id); err != nil {
		return 0, err
	}

	return s.memory.Count(ctx, id, node, s.now())
}

// Forget clears what a node of one of p's workflows remembers.
func (s *Service) Forget(ctx context.Context, p auth.Principal, id domain.WorkflowID, node domain.NodeID) error {
	if _, err := s.workflows.Get(ctx, p, id); err != nil {
		return err
	}

	if err := s.memory.Forget(ctx, id, node); err != nil {
		return err
	}

	s.log.Info(ctx, "Node memory forgotten", "user_id", p.User.ID, "workflow_id", id, "node_id", node)

	return nil
}

// SweepEvery forgets what has expired now and then until ctx is done.
func (s *Service) SweepEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		n, err := s.memory.Sweep(ctx, s.now())

		switch {
		case err != nil && ctx.Err() == nil:
			s.log.Warn(ctx, "Node memory not swept", logging.Err(err))
		case n > 0:
			s.log.Debug(ctx, "Node memory swept", "forgotten", n)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
