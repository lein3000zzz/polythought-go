package datasets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Workflows are users' workflows: those naming a dataset, and switching them off.
type Workflows interface {
	// Of is every workflow of the owner.
	Of(ctx context.Context, owner domain.UserID) ([]domain.Workflow, error)
	// Pause switches off each active workflow why gives a reason for, and tells its owner
	// why. How many it switched off.
	Pause(ctx context.Context, why func(w domain.Workflow) string) (int, error)
}

// Params names a node type's dataset settings.
type Params func(nodeType string) []string

// UseWorkflows has a dataset deleted switch off the workflows naming it, and datasets lost
// those of the user who lost them; params names each node type's dataset settings. Without
// it, workflows are left as they are, and fail as they run.
func (s *Service) UseWorkflows(w Workflows, params Params) {
	s.workflows, s.params = w, params
}

// use is a node naming a dataset.
type use struct {
	node domain.NodeID
	name string
}

// named are the datasets w's nodes name.
func (s *Service) named(w domain.Workflow) []use {
	if s.params == nil {
		return nil
	}

	var out []use

	for _, n := range w.Nodes {
		for _, param := range s.params(n.Type) {
			if name, ok := n.Params[param].(string); ok && strings.TrimSpace(name) != "" {
				out = append(out, use{node: n.ID, name: strings.TrimSpace(name)})
			}
		}
	}

	return out
}

// UsedBy are the owner's workflows naming each of their datasets, by its name.
func (s *Service) UsedBy(ctx context.Context, owner domain.UserID) (map[string][]domain.Workflow, error) {
	out := map[string][]domain.Workflow{}
	if s.workflows == nil {
		return out, nil
	}

	list, err := s.workflows.Of(ctx, owner)
	if err != nil {
		return nil, err
	}

	for _, w := range list {
		seen := map[string]bool{}

		for _, u := range s.named(w) {
			if !seen[u.name] {
				seen[u.name] = true
				out[u.name] = append(out[u.name], w)
			}
		}
	}

	return out, nil
}

// CheckWorkflow refuses to switch on a workflow whose nodes name a dataset its owner does
// not have, or that uses datasets an admin keeps from them.
func (s *Service) CheckWorkflow(ctx context.Context, w domain.Workflow) error {
	uses := s.named(w)
	if len(uses) == 0 {
		return nil
	}

	if err := s.allowed(ctx, w.OwnerID); err != nil {
		if errors.Is(err, domain.ErrDataOff) {
			return fmt.Errorf("%w: node %q uses a dataset", err, uses[0].node)
		}

		return err
	}

	have, err := s.deps.Repo.List(ctx, w.OwnerID)
	if err != nil {
		return err
	}

	names := make(map[string]bool, len(have))
	for _, d := range have {
		names[d.Name] = true
	}

	for _, u := range uses {
		if !names[u.name] {
			return fmt.Errorf("%w: node %q names the dataset %q, which you do not have: pick another, or make it",
				domain.ErrInvalidInput, u.node, u.name)
		}
	}

	return nil
}

// switchOffNaming switches off the owner's workflows naming a dataset deleted.
func (s *Service) switchOffNaming(ctx context.Context, owner domain.UserID, name string) {
	if s.workflows == nil {
		return
	}

	n, err := s.workflows.Pause(ctx, func(w domain.Workflow) string {
		if w.OwnerID != owner {
			return ""
		}

		for _, u := range s.named(w) {
			if u.name == name {
				return fmt.Sprintf("the dataset %s is deleted", name)
			}
		}

		return ""
	})
	if err != nil {
		s.log.Warn(ctx, "Workflows naming a dataset deleted not all switched off", "owner_id", owner, "dataset", name,
			"switched_off", n, logging.Err(err))
	}
}

// SwitchOffLost switches off the workflows naming datasets whose owners may no longer use
// them: an admin changed who may, or the user.
func (s *Service) SwitchOffLost(ctx context.Context) {
	if s.workflows == nil || s.deps.Gate == nil {
		return
	}

	may := map[domain.UserID]bool{}

	n, err := s.workflows.Pause(ctx, func(w domain.Workflow) string {
		if len(s.named(w)) == 0 {
			return ""
		}

		ok, asked := may[w.OwnerID]
		if !asked {
			allowed, err := s.deps.Gate.DataAllowed(ctx, w.OwnerID)
			ok = err != nil || allowed // not knowing, it stays on: its runs ask again
			may[w.OwnerID] = ok
		}

		if ok {
			return ""
		}

		return "you may no longer use datasets"
	})

	switch {
	case err != nil:
		s.log.Warn(ctx, "Workflows of users who lost datasets not all switched off", "switched_off", n, logging.Err(err))
	case n > 0:
		s.log.Info(ctx, "Workflows of users who lost datasets switched off", "switched_off", n)
	}
}
