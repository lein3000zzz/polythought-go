package customnodes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Confirmer holds a token's or an agent's action for its user to confirm (D406).
type Confirmer interface {
	Hold(ctx context.Context, p auth.Principal, a domain.Ask) error
}

// TradesLive says whether a workflow places live orders.
type TradesLive func(ctx context.Context, w domain.Workflow) (bool, error)

// UseConfirmations holds a token's or an agent's change to a custom node, or its deleting,
// for its user where a workflow that trades live and is on uses it (A5): its next run
// runs the new code, which may change what it buys. Without it, nothing waits.
func (s *Service) UseConfirmations(c Confirmer, live TradesLive) {
	s.confirm, s.live = c, live
}

// NodeAsked is what a held change to a custom node carries out: the definition as it was
// given, on the version it was given for, or the node's deleting at that version.
type NodeAsked struct {
	ID         domain.CustomNodeID `json:"id"`
	Version    int                 `json:"version"`
	Definition *Definition         `json:"definition,omitempty"` // nil: delete it
	Confirm    bool                `json:"confirm,omitempty"`    // even where it breaks workflows (D50)
}

// hold keeps a token's or an agent's change to n, edited, or its deleting when edited is
// nil, for its user where a live workflow that is on uses it.
func (s *Service) hold(ctx context.Context, p auth.Principal, n domain.CustomNode, edited *domain.CustomNode, asked NodeAsked) error {
	if s.confirm == nil || s.live == nil || p.Itself() {
		return nil
	}

	names, err := s.liveUsers(ctx, n)
	if err != nil || len(names) == 0 {
		return err
	}

	used := strings.Join(names, ", ")

	what := fmt.Sprintf("Delete the custom node %q, which %s uses, trading live and on", n.Name, used)
	detail := "Their runs fail from then on: the node's type is gone."

	if edited != nil {
		what = fmt.Sprintf("Change the custom node %q, which %s uses, trading live and on", n.Name, used)
		detail = nodeChanges(n, *edited)
	}

	return s.confirm.Hold(ctx, p, domain.Ask{Kind: domain.ConfirmNodeChange, What: what, Detail: detail, Payload: asked})
}

// liveUsers names the owner's workflows that use n, are on and trade live.
func (s *Service) liveUsers(ctx context.Context, n domain.CustomNode) ([]string, error) {
	typ := engine.CustomTypePrefix + n.Name
	on := true

	var names []string

	for cursor := ""; ; {
		page, err := s.workflows.List(ctx, n.OwnerID, ports.WorkflowFilter{
			PageFilter: ports.PageFilter{Limit: 200, Cursor: cursor}, Active: &on,
		})
		if err != nil {
			return nil, err
		}

		for _, w := range page.Items {
			if !w.Active || !slices.ContainsFunc(w.Nodes, func(spec domain.NodeSpec) bool { return spec.Type == typ }) {
				continue
			}

			live, err := s.live(ctx, w)
			if err != nil {
				return nil, err
			}

			if live {
				names = append(names, fmt.Sprintf("%q", w.Name))
			}
		}

		if page.NextCursor == "" {
			return names, nil
		}

		cursor = page.NextCursor
	}
}

// asAsked refuses to carry out a confirmed deleting of a node that changed since it was
// asked for: the user confirmed that version, not one saved since (A4).
func asAsked(p auth.Principal, n domain.CustomNode) error {
	c := p.Confirming
	if !p.Confirmed || c == nil || c.Kind != domain.ConfirmNodeChange {
		return nil
	}

	var at NodeAsked
	if err := json.Unmarshal(c.Payload, &at); err != nil || at.ID != n.ID || at.Version != n.Version {
		return fmt.Errorf("%w: the custom node %q changed since it was asked for: ask again", domain.ErrConflict, n.Name)
	}

	return nil
}

// nodeChanges says what edited changes of n, a line each, for its user to read (A3); the
// new code in full, where it changed.
func nodeChanges(n, edited domain.CustomNode) string {
	var lines []string

	field := func(name string, before, after any) {
		was, now := shown(before), shown(after)
		if was != now {
			lines = append(lines, fmt.Sprintf("%s: %s → %s", name, was, now))
		}
	}

	field("Label", n.Label, edited.Label)
	field("Description", n.Description, edited.Description)
	field("Settings", n.Settings, edited.Settings)
	field("Outputs", n.Outputs, edited.Outputs)
	field("Items", n.Items, edited.Items)

	if n.Source != edited.Source {
		lines = append(lines, "Code, now:\n"+edited.Source)
	}

	if len(lines) == 0 {
		return "Nothing: it stays as it is."
	}

	return strings.Join(lines, "\n")
}

func shown(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	return string(data)
}
