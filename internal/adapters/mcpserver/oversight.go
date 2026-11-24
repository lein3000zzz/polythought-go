package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// An admin's agent's oversight of every user's workflows (D435): the busiest first, and
// switching one off, holding it off or releasing it, which waits for the admin where they
// have such work wait (D410). Users go by ID, never their emails; other users' workflow
// names are their words, under untrusted.

// Oversight is an admin's work on users' workflows: the workflows service.
type Oversight interface {
	AdminSwitchOff(ctx context.Context, p auth.Principal, owner domain.UserID, id domain.WorkflowID, reason string, hold bool) (domain.Workflow, error)
	AdminRelease(ctx context.Context, p auth.Principal, owner domain.UserID, id domain.WorkflowID) (domain.Workflow, error)
}

type instanceWorkflowOut struct {
	ID        string       `json:"id"`
	Owner     string       `json:"owner" jsonschema:"the owner's user ID, as list_users gives it"`
	Active    bool         `json:"active"`
	Held      string       `json:"held,omitempty" jsonschema:"held off by an admin, saying why; absent while not"`
	Triggers  []string     `json:"triggers" jsonschema:"its triggers' node types"`
	Hour      runCountsOut `json:"runsHour" jsonschema:"its runs started in the last hour, and of them those that failed"`
	Day       runCountsOut `json:"runsDay"`
	Week      runCountsOut `json:"runsWeek"`
	Untrusted Untrusted    `json:"untrusted" jsonschema:"name: the workflow's, as its owner gave it"`
}

type instanceWorkflowsOut struct {
	Workflows []instanceWorkflowOut `json:"workflows"`
	Notice    string                `json:"notice,omitempty"`
	Next      string                `json:"next,omitempty" jsonschema:"the cursor for the page after; absent at the end"`
	At        time.Time             `json:"at"`
}

type userWorkflowIn struct {
	User     string `json:"user" jsonschema:"the owner's user ID, as list_instance_workflows or list_users gives it"`
	Workflow string `json:"workflow" jsonschema:"the workflow's ID"`
}

type adminSwitchOffIn struct {
	userWorkflowIn
	Reason string `json:"reason" jsonschema:"why, for its owner, who is told: a sentence, as the admin gave it to you"`
	Hold   bool   `json:"hold,omitempty" jsonschema:"keep it off until an admin releases it"`
}

type userWorkflowOut struct {
	ID     string `json:"id"`
	Active bool   `json:"active"`
	Held   bool   `json:"held"`
}

func (s *Server) addOversightTools(srv *mcp.Server) {
	add(s, srv, Tool[page, instanceWorkflowsOut]{
		Name: "list_instance_workflows", Title: "List every user's workflows", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "For an admin, as Admin → Stats → Workflows shows it: every user's workflows, the busiest first, by " +
			"their runs in the last hour, day and week, with their owners by ID, their triggers and whether an admin holds " +
			"them off. Their names are their owners' words, under untrusted. A page at a time.",
		Handle: s.listInstanceWorkflows,
	})

	add(s, srv, Tool[adminSwitchOffIn, userWorkflowOut]{
		Name: "switch_off_user_workflow", Title: "Switch off a user's workflow", Scope: domain.ScopeAdmin, Destructive: true,
		Description: "For an admin: switch off another user's workflow, saying why, and with hold keep it off until an admin " +
			"releases it. Its owner is told. It waits for the admin to confirm it where they have such work wait: the " +
			"answer then gives a confirmation ID for get_confirmation.",
		Handle: s.switchOffUserWorkflow,
	})

	add(s, srv, Tool[userWorkflowIn, userWorkflowOut]{
		Name: "release_user_workflow", Title: "Release a user's workflow", Scope: domain.ScopeAdmin,
		Description: "For an admin: let another user's workflow that an admin held off be switched on again; its owner is " +
			"told. It waits for the admin to confirm it where they have such work wait.",
		Handle: s.releaseUserWorkflow,
	})
}

func (s *Server) listInstanceWorkflows(ctx context.Context, p auth.Principal, in page) (instanceWorkflowsOut, error) {
	all, at, err := s.stats.Workflows(ctx, p)
	if err != nil {
		return instanceWorkflowsOut{}, err
	}

	shown, next := pageOf(all, in)
	out := instanceWorkflowsOut{Workflows: []instanceWorkflowOut{}, Next: next, At: at}

	for _, w := range shown {
		o := instanceWorkflowOut{
			ID: string(w.ID), Owner: string(w.Owner.ID), Active: w.Active, Triggers: w.Triggers,
			Hour: runCountsOut(w.Runs.Hour), Day: runCountsOut(w.Runs.Day), Week: runCountsOut(w.Runs.Week),
			Untrusted: Untrusted{"name": w.Name},
		}

		if o.Triggers == nil {
			o.Triggers = []string{}
		}

		if w.Hold != nil {
			o.Held = w.Hold.Reason
		}

		out.Workflows = append(out.Workflows, o)
	}

	if len(out.Workflows) > 0 {
		out.Notice = Notice
	}

	return out, nil
}

func (s *Server) switchOffUserWorkflow(ctx context.Context, p auth.Principal, in adminSwitchOffIn) (userWorkflowOut, error) {
	w, err := s.oversight.AdminSwitchOff(ctx, p, domain.UserID(in.User), domain.WorkflowID(in.Workflow), in.Reason, in.Hold)
	if err != nil {
		return userWorkflowOut{}, err
	}

	return userWorkflowOut{ID: string(w.ID), Active: w.Active, Held: w.Hold != nil}, nil
}

func (s *Server) releaseUserWorkflow(ctx context.Context, p auth.Principal, in userWorkflowIn) (userWorkflowOut, error) {
	w, err := s.oversight.AdminRelease(ctx, p, domain.UserID(in.User), domain.WorkflowID(in.Workflow))
	if err != nil {
		return userWorkflowOut{}, err
	}

	return userWorkflowOut{ID: string(w.ID), Active: w.Active, Held: w.Hold != nil}, nil
}
