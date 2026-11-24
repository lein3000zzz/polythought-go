// Package usage finds the workflows whose nodes name one of a user's datasets or models,
// for those to be switched off when it goes, or when its owner may no longer use them.
package usage

import (
	"context"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Workflows are users' workflows: every one of an owner's, and switching them off.
type Workflows interface {
	// Of is every workflow of the owner.
	Of(ctx context.Context, owner domain.UserID) ([]domain.Workflow, error)
	// Pause switches off each active workflow why gives a reason for, and tells its owner
	// why. How many it switched off.
	Pause(ctx context.Context, why func(w domain.Workflow) string) (int, error)
}

// Params names a node type's settings that name what is looked for: its dataset settings,
// say.
type Params func(nodeType string) []string

// Use is a node naming one, by its name.
type Use struct {
	Node domain.NodeID
	Name string
}

// Finder finds the workflows naming one through Params' settings. Its zero value finds
// none and switches nothing off: without workflows, those naming one gone fail as they run.
type Finder struct {
	Workflows Workflows
	Params    Params
}

// Named are what w's nodes name.
func (f Finder) Named(w domain.Workflow) []Use {
	if f.Params == nil {
		return nil
	}

	var out []Use

	for _, n := range w.Nodes {
		for _, param := range f.Params(n.Type) {
			if name, ok := n.Params[param].(string); ok && strings.TrimSpace(name) != "" {
				out = append(out, Use{Node: n.ID, Name: strings.TrimSpace(name)})
			}
		}
	}

	return out
}

// UsedBy are the owner's workflows naming each, by its name, each workflow once a name.
func (f Finder) UsedBy(ctx context.Context, owner domain.UserID) (map[string][]domain.Workflow, error) {
	out := map[string][]domain.Workflow{}
	if f.Workflows == nil {
		return out, nil
	}

	list, err := f.Workflows.Of(ctx, owner)
	if err != nil {
		return nil, err
	}

	for _, w := range list {
		seen := map[string]bool{}

		for _, u := range f.Named(w) {
			if !seen[u.Name] {
				seen[u.Name] = true
				out[u.Name] = append(out[u.Name], w)
			}
		}
	}

	return out, nil
}

// SwitchOffNaming switches off the owner's running workflows naming name, saying why.
func (f Finder) SwitchOffNaming(ctx context.Context, owner domain.UserID, name, why string) (int, error) {
	if f.Workflows == nil {
		return 0, nil
	}

	return f.Workflows.Pause(ctx, func(w domain.Workflow) string {
		if w.OwnerID != owner {
			return ""
		}

		for _, u := range f.Named(w) {
			if u.Name == name {
				return why
			}
		}

		return ""
	})
}

// SwitchOffLost switches off the running workflows naming any whose owners may not, by
// may, saying why; may is asked once an owner.
func (f Finder) SwitchOffLost(ctx context.Context, may func(owner domain.UserID) bool, why string) (int, error) {
	if f.Workflows == nil {
		return 0, nil
	}

	asked := map[domain.UserID]bool{}

	return f.Workflows.Pause(ctx, func(w domain.Workflow) string {
		if len(f.Named(w)) == 0 {
			return ""
		}

		ok, seen := asked[w.OwnerID]
		if !seen {
			ok = may(w.OwnerID)
			asked[w.OwnerID] = ok
		}

		if ok {
			return ""
		}

		return why
	})
}
