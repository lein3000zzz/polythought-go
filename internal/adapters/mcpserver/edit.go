package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
	"github.com/lein3000zzz/polythought-go/internal/core/workflowcode"
)

// The tools that build workflows (D403): made and changed from TypeScript or JSON, with
// the editor's version check, run, switched on and off, deleted where the user lets
// agents. New nodes go where the editor would put them.

// Editing is what the build tools change: the workflows service.
type Editing interface {
	Create(ctx context.Context, p auth.Principal, def workflows.Definition) (domain.Workflow, error)
	Update(ctx context.Context, p auth.Principal, id domain.WorkflowID, version int, def workflows.Definition) (domain.Workflow, error)
	Delete(ctx context.Context, p auth.Principal, id domain.WorkflowID) error
	Activate(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
	SwitchOff(ctx context.Context, p auth.Principal, id domain.WorkflowID, cancelOrders bool) (domain.Workflow, error)
	Run(ctx context.Context, p auth.Principal, id domain.WorkflowID, trigger domain.NodeID) (domain.Execution, error)
	CancelExecution(ctx context.Context, p auth.Principal, id domain.WorkflowID, execID domain.ExecutionID) (domain.Execution, error)
}

// WorkflowCode runs workflow code into the graph it builds, for a user who may write code.
type WorkflowCode interface {
	Build(ctx context.Context, p auth.Principal, source string) (domain.Workflow, error)
}

type writeIn struct {
	Name     string   `json:"name,omitempty" jsonschema:"its name; for code, the name workflow() gives unless said"`
	Code     string   `json:"code,omitempty" jsonschema:"workflow code in TypeScript, as get_workflow gives it; or give workflow"`
	Workflow *graphIn `json:"workflow,omitempty" jsonschema:"its nodes and connections as JSON, as get_workflow gives them; or give code"`
}

// graphIn is a graph as an agent gives it, where a lone trigger has no connections.
type graphIn struct {
	Nodes       []nodeOut `json:"nodes"`
	Connections []connOut `json:"connections,omitempty"`
}

type createIn struct {
	writeIn
}

type updateIn struct {
	ID      string `json:"id" jsonschema:"the workflow's ID"`
	Version int    `json:"version" jsonschema:"the version you read it at, as get_workflow gave it: a newer one means someone changed it since"`
	writeIn
}

type savedOut struct {
	workflowSummary

	Hidden []string `json:"hidden,omitempty" jsonschema:"personal settings kept as they were, as node.setting"`
}

type switchIn struct {
	ID           string `json:"id" jsonschema:"the workflow's ID"`
	On           bool   `json:"on" jsonschema:"true switches its triggers on, false off"`
	CancelOrders bool   `json:"cancelOrders,omitempty" jsonschema:"switching off, cancel its live orders resting on the book too"`
}

type runIn struct {
	ID      string `json:"id" jsonschema:"the workflow's ID"`
	Trigger string `json:"trigger,omitempty" jsonschema:"the manual trigger or schedule to start from, when it has several"`
}

type deletedOut struct {
	Deleted string `json:"deleted"`
}

func (s *Server) addEditTools(srv *mcp.Server) {
	add(s, srv, Tool[createIn, savedOut]{
		Name: "create_workflow", Title: "Create a workflow", Scope: domain.ScopeBuild,
		Description: "Make a workflow from workflow code in TypeScript, the Code view's, or from its graph as JSON. It is made " +
			"off, its nodes laid out as the editor would. check_workflow says what it lacks; one that places orders needs paper or live too.",
		Handle: s.createWorkflow,
	})

	add(s, srv, Tool[updateIn, savedOut]{
		Name: "update_workflow", Title: "Change a workflow", Scope: domain.ScopeBuild,
		Description: "Replace a workflow's graph with workflow code or JSON, as get_workflow gave it and changed: the version " +
			"you read must still be current. Nodes it had keep their places on the canvas; new ones go beside theirs. Personal " +
			`settings left {"$personal": "hidden"} keep their values. One that runs carries on with the new graph.`,
		Handle: s.updateWorkflow,
	})

	add(s, srv, Tool[idIn, deletedOut]{
		Name: "delete_workflow", Title: "Delete a workflow", Scope: domain.ScopeBuild, Destructive: true,
		Description: "Delete a workflow, its executions and what its nodes remember, its live orders resting on the book " +
			"cancelled first. It cannot be undone, and works only where the user lets agents delete workflows.",
		Handle: s.deleteWorkflow,
	})

	add(s, srv, Tool[switchIn, workflowSummary]{
		Name: "switch_workflow", Title: "Switch a workflow on or off", Scope: domain.ScopeBuild,
		Description: "Switch a workflow's triggers on, so it runs by itself, or off. Switching on one that places orders needs " +
			"paper, or live where it trades live.",
		Handle: s.switchWorkflow,
	})

	add(s, srv, Tool[runIn, executionOut]{
		Name: "run_workflow", Title: "Run a workflow", Scope: domain.ScopeBuild,
		Description: "Run a workflow once, now, from its manual trigger or schedule, and wait for it to end: each node's " +
			"status and counts, as get_execution has them; get_execution_items gives the items it kept.",
		Handle: s.runWorkflow,
	})

	add(s, srv, Tool[executionIn, executionSummary]{
		Name: "cancel_execution", Title: "Cancel an execution", Scope: domain.ScopeBuild,
		Description: "Cancel an execution that waits, such as at a Wait node or for an answer.",
		Handle:      s.cancelExecution,
	})
}

// definitionOf is the graph asked for, from code or JSON, with its name.
func (s *Server) definitionOf(ctx context.Context, p auth.Principal, in writeIn, name string) (workflows.Definition, error) {
	switch {
	case (in.Code == "") == (in.Workflow == nil):
		return workflows.Definition{}, invalid("give code or workflow, one of the two")
	case in.Code != "":
		w, err := s.code.Build(ctx, p, in.Code)
		if err != nil {
			return workflows.Definition{}, err
		}

		return workflows.Definition{Name: firstOf(in.Name, w.Name, name), Nodes: w.Nodes, Connections: w.Connections}, nil
	}

	def := workflows.Definition{Name: firstOf(in.Name, name)}

	for _, n := range in.Workflow.Nodes {
		def.Nodes = append(def.Nodes, domain.NodeSpec{
			ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Notes: n.Notes, ContinueOnFail: n.ContinueOnFail, Params: n.Params,
		})
	}

	for _, c := range in.Workflow.Connections {
		def.Connections = append(def.Connections, domain.Connection{
			From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort,
		})
	}

	return def, nil
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}

	return ""
}

func (s *Server) createWorkflow(ctx context.Context, p auth.Principal, in createIn) (savedOut, error) {
	def, err := s.definitionOf(ctx, p, in.writeIn, "")
	if err != nil {
		return savedOut{}, err
	}

	def.Nodes = workflowcode.Place(def.Nodes, def.Connections, nil)

	w, err := s.edit.Create(ctx, p, def)
	if err != nil {
		return savedOut{}, err
	}

	return s.savedOf(ctx, w)
}

func (s *Server) updateWorkflow(ctx context.Context, p auth.Principal, in updateIn) (savedOut, error) {
	id := domain.WorkflowID(in.ID)

	was, err := s.workflows.Get(ctx, p, id)
	if err != nil {
		return savedOut{}, err
	}

	def, err := s.definitionOf(ctx, p, in.writeIn, was.Name)
	if err != nil {
		return savedOut{}, err
	}

	places := make(map[domain.NodeID]domain.CanvasPosition, len(was.Nodes))
	for _, n := range was.Nodes {
		places[n.ID] = n.Position
	}

	def.Nodes = workflowcode.Place(def.Nodes, def.Connections, places)

	w, err := s.edit.Update(ctx, p, id, in.Version, def)
	if errors.Is(err, domain.ErrConflict) {
		return savedOut{}, fmt.Errorf("%w: the workflow changed since version %d; get_workflow again and make the change on that", domain.ErrConflict, in.Version)
	}

	if err != nil {
		return savedOut{}, err
	}

	return s.savedOf(ctx, w)
}

func (s *Server) savedOf(ctx context.Context, w domain.Workflow) (savedOut, error) {
	sum, err := s.summaryOf(ctx, w)
	if err != nil {
		return savedOut{}, err
	}

	return savedOut{workflowSummary: sum, Hidden: hiddenIn(w.Nodes)}, nil
}

func (s *Server) deleteWorkflow(ctx context.Context, p auth.Principal, in idIn) (deletedOut, error) {
	if err := s.edit.Delete(ctx, p, domain.WorkflowID(in.ID)); err != nil {
		return deletedOut{}, err
	}

	return deletedOut{Deleted: in.ID}, nil
}

func (s *Server) switchWorkflow(ctx context.Context, p auth.Principal, in switchIn) (workflowSummary, error) {
	var (
		w   domain.Workflow
		err error
	)

	if in.On {
		w, err = s.edit.Activate(ctx, p, domain.WorkflowID(in.ID))
	} else {
		w, err = s.edit.SwitchOff(ctx, p, domain.WorkflowID(in.ID), in.CancelOrders)
	}

	if err != nil {
		return workflowSummary{}, err
	}

	return s.summaryOf(ctx, w)
}

func (s *Server) runWorkflow(ctx context.Context, p auth.Principal, in runIn) (executionOut, error) {
	e, err := s.edit.Run(ctx, p, domain.WorkflowID(in.ID), domain.NodeID(in.Trigger))
	if err != nil {
		return executionOut{}, err
	}

	return executionOf(e), nil
}

func (s *Server) cancelExecution(ctx context.Context, p auth.Principal, in executionIn) (executionSummary, error) {
	e, err := s.edit.CancelExecution(ctx, p, domain.WorkflowID(in.Workflow), domain.ExecutionID(in.Execution))
	if err != nil {
		return executionSummary{}, err
	}

	return executionSummaryOf(e), nil
}
