package engine

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// PluginGate says whether a user may use a plugin's nodes now: an admin may restrict a
// plugin to chosen users (D282).
type PluginGate interface {
	PluginAllowed(ctx context.Context, owner domain.UserID, plugin string) (bool, error)
}

// WithPluginGate fails executions of workflows with nodes of a plugin their owner may not
// use. Without it, everyone may.
func WithPluginGate(g PluginGate) ExecutorOption {
	return func(e *Executor) { e.plugins = g }
}

// PluginNodes are the nodes of w whose types come from plugins, by plugin.
func (r *Registry) PluginNodes(w domain.Workflow) map[string][]domain.NodeID {
	out := map[string][]domain.NodeID{}

	for _, n := range w.Nodes {
		if typ, _, ok := r.Type(n.Type); ok && typ.Plugin != "" {
			out[typ.Plugin] = append(out[typ.Plugin], n.ID)
		}
	}

	return out
}

// refusedPlugins are the plugins of w's nodes its owner may not use, with their nodes.
func (r *Registry) refusedPlugins(ctx context.Context, w domain.Workflow, gate PluginGate) (map[string][]domain.NodeID, error) {
	refused := map[string][]domain.NodeID{}

	for plugin, ids := range r.PluginNodes(w) {
		allowed, err := gate.PluginAllowed(ctx, w.OwnerID, plugin)
		if err != nil {
			return nil, err
		}

		if !allowed {
			refused[plugin] = ids
		}
	}

	return refused, nil
}

// CheckPluginAccess reports each node of a plugin w's owner may not use, as gate says.
// Saving asks this. An error from gate comes back as it is, not as a problem.
func (r *Registry) CheckPluginAccess(ctx context.Context, w domain.Workflow, gate PluginGate) error {
	refused, err := r.refusedPlugins(ctx, w, gate)
	if err != nil {
		return err
	}

	var v domain.ValidationError

	for _, plugin := range slices.Sorted(maps.Keys(refused)) {
		for _, id := range refused[plugin] {
			v.Add(domain.Problem{
				NodeID: id, Err: domain.ErrPluginNotAllowed,
				Message: fmt.Sprintf("node %q: %v %s", id, domain.ErrPluginNotAllowed, plugin),
			})
		}
	}

	return v.Err()
}

// checkPlugins fails an execution of a workflow with nodes of a plugin its owner may not
// use, before any node runs.
func (e *Executor) checkPlugins(ctx context.Context, reg *Registry, w domain.Workflow) error {
	if e.plugins == nil {
		return nil
	}

	refused, err := reg.refusedPlugins(ctx, w, e.plugins)
	if err != nil {
		return fmt.Errorf("could not tell whether the owner may use its plugins: %w", err)
	}

	if len(refused) == 0 {
		return nil
	}

	return fmt.Errorf("%w %s, so the workflow does not run", domain.ErrPluginNotAllowed, strings.Join(slices.Sorted(maps.Keys(refused)), ", "))
}
