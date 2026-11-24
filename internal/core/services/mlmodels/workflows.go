package mlmodels

import (
	"context"
	"errors"
	"fmt"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/usage"
)

// UseWorkflows has a model deleted switch off the workflows naming it, and models lost
// those of the user who lost them; params names each node type's model settings. Without
// it, workflows are left as they are, and fail as they run.
func (s *Service) UseWorkflows(w usage.Workflows, params usage.Params) {
	s.usage = usage.Finder{Workflows: w, Params: params}
}

// UsedBy are the owner's workflows naming each of their models, by its name.
func (s *Service) UsedBy(ctx context.Context, owner domain.UserID) (map[string][]domain.Workflow, error) {
	return s.usage.UsedBy(ctx, owner)
}

// CheckWorkflow refuses to switch on a workflow whose nodes name a model its owner does
// not have, or that uses models an admin keeps from them.
func (s *Service) CheckWorkflow(ctx context.Context, w domain.Workflow) error {
	uses := s.usage.Named(w)
	if len(uses) == 0 {
		return nil
	}

	if err := s.allowed(ctx, w.OwnerID); err != nil {
		if errors.Is(err, domain.ErrDataOff) {
			return fmt.Errorf("%w: node %q asks a model", err, uses[0].Node)
		}

		return err
	}

	have, err := s.deps.Repo.List(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	names := make(map[string]bool, len(have))
	for _, m := range have {
		names[m.Name] = true
	}

	for _, u := range uses {
		if !names[u.Name] {
			return fmt.Errorf("%w: node %q names the model %q, which you do not have: pick another, or make it",
				domain.ErrInvalidInput, u.Node, u.Name)
		}
	}

	return nil
}

// SwitchOffLost switches off the workflows naming models whose owners may no longer use
// them: an admin changed who may, or the user.
func (s *Service) SwitchOffLost(ctx context.Context) {
	if s.deps.Gate == nil {
		return
	}

	n, err := s.usage.SwitchOffLost(ctx, func(owner domain.UserID) bool {
		allowed, err := s.deps.Gate.DataAllowed(ctx, owner)
		return err != nil || allowed // not knowing, it stays on: its runs ask again
	}, "you may no longer use models")

	switch {
	case err != nil:
		s.log.Warn(ctx, "Workflows of users who lost models not all switched off", "switched_off", n, logging.Err(err))
	case n > 0:
		s.log.Info(ctx, "Workflows of users who lost models switched off", "switched_off", n)
	}
}
