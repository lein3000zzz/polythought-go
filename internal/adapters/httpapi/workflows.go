package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/workflows"
)

type positionDTO struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
} // @name CanvasPosition

type nodeDTO struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Name           string         `json:"name,omitempty" validate:"optional"`
	Params         map[string]any `json:"params,omitempty" validate:"optional"`
	ContinueOnFail bool           `json:"continueOnFail,omitempty" validate:"optional"`
	// Position is where the editor draws the node; it never affects execution.
	Position positionDTO `json:"position" validate:"optional"`
} // @name Node

func toNodeDTO(n domain.NodeSpec) nodeDTO {
	return nodeDTO{
		ID: string(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
		Position: positionDTO{X: n.Position.X, Y: n.Position.Y},
	}
}

func (n nodeDTO) spec() domain.NodeSpec {
	return domain.NodeSpec{
		ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
		Position: domain.CanvasPosition{X: n.Position.X, Y: n.Position.Y},
	}
}

type connectionDTO struct {
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
} // @name Connection

type workflowDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Active      bool            `json:"active"`
	Version     int             `json:"version"`
	Nodes       []nodeDTO       `json:"nodes"`
	Connections []connectionDTO `json:"connections"`
	CreatedAt   time.Time       `json:"createdAt" format:"date-time"`
	UpdatedAt   time.Time       `json:"updatedAt" format:"date-time"`
} // @name Workflow

func toWorkflowDTO(w domain.Workflow) workflowDTO {
	out := workflowDTO{
		ID: string(w.ID), Name: w.Name, Active: w.Active, Version: w.Version,
		Nodes:       make([]nodeDTO, len(w.Nodes)),
		Connections: make([]connectionDTO, len(w.Connections)),
		CreatedAt:   w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}

	for i, n := range w.Nodes {
		out.Nodes[i] = toNodeDTO(n)
	}

	for i, c := range w.Connections {
		out.Connections[i] = connectionDTO{From: string(c.From), FromPort: c.FromPort, To: string(c.To), ToPort: c.ToPort}
	}

	return out
}

type workflowPage struct {
	Workflows []workflowDTO `json:"workflows"`
	// NextCursor is absent on the last page.
	NextCursor string `json:"nextCursor,omitempty" validate:"optional"`
} // @name WorkflowPage

// definitionRequest is the editable part of a workflow in create and update bodies.
type definitionRequest struct {
	Name        string          `json:"name"`
	Nodes       []nodeDTO       `json:"nodes"`
	Connections []connectionDTO `json:"connections"`
} // @name WorkflowDefinition

func (r definitionRequest) definition() workflows.Definition {
	def := workflows.Definition{
		Name:        r.Name,
		Nodes:       make([]domain.NodeSpec, len(r.Nodes)),
		Connections: make([]domain.Connection, len(r.Connections)),
	}

	for i, n := range r.Nodes {
		def.Nodes[i] = n.spec()
	}

	for i, c := range r.Connections {
		def.Connections[i] = domain.Connection{From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort}
	}

	return def
}

type updateWorkflowRequest struct {
	definitionRequest

	// Version is the version the edit started from; a newer one on the server is a conflict.
	Version int `json:"version"`
} // @name UpdateWorkflowRequest

type runRequest struct {
	// Trigger is the trigger node to start from; left out means the manual trigger.
	Trigger string `json:"trigger" validate:"optional"`
} // @name RunRequest

type nodeRunDTO struct {
	NodeID     string         `json:"nodeId"`
	Status     string         `json:"status" enums:"succeeded,failed,skipped"`
	StartedAt  time.Time      `json:"startedAt" format:"date-time"`
	FinishedAt time.Time      `json:"finishedAt" format:"date-time"`
	Input      map[string]int `json:"input,omitempty" validate:"optional"`  // items per input port
	Output     map[string]int `json:"output,omitempty" validate:"optional"` // items per output port
	Error      string         `json:"error,omitempty" validate:"optional"`
} // @name NodeRun

type executionDTO struct {
	ID              string       `json:"id"`
	WorkflowVersion int          `json:"workflowVersion"`
	TriggeredBy     string       `json:"triggeredBy"`
	Status          string       `json:"status" enums:"running,succeeded,failed,cancelled"`
	StartedAt       time.Time    `json:"startedAt" format:"date-time"`
	FinishedAt      *time.Time   `json:"finishedAt,omitempty" validate:"optional" format:"date-time"`
	DurationMs      int64        `json:"durationMs"`
	Error           string       `json:"error,omitempty" validate:"optional"`
	Runs            []nodeRunDTO `json:"runs"`
} // @name Execution

func toExecutionDTO(e domain.Execution) executionDTO {
	out := executionDTO{
		ID: string(e.ID), WorkflowVersion: e.WorkflowVersion, TriggeredBy: string(e.TriggeredBy), Status: string(e.Status),
		StartedAt: e.StartedAt, FinishedAt: optionalTime(e.FinishedAt), DurationMs: e.Duration().Milliseconds(),
		Error: e.Error, Runs: make([]nodeRunDTO, len(e.Runs)),
	}

	for i, r := range e.Runs {
		out.Runs[i] = nodeRunDTO{
			NodeID: string(r.NodeID), Status: string(r.Status), StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
			Input: r.Input, Output: r.Output, Error: r.Error,
		}
	}

	return out
}

type executionPage struct {
	Executions []executionDTO `json:"executions"`
	// NextCursor is absent on the last page.
	NextCursor string `json:"nextCursor,omitempty" validate:"optional"`
} // @name ExecutionPage

type paramDTO struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty" validate:"optional"`
	Description string `json:"description,omitempty" validate:"optional"`
	Kind        string `json:"kind" enums:"string,number,decimal,bool,list,duration,wallet,tokens"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty" validate:"optional"`
	// Options are the allowed values of a string, or of each element of a list.
	Options []string `json:"options,omitempty" validate:"optional"`
} // @name ParamSpec

type nodeTypeDTO struct {
	Name        string     `json:"name"`
	Label       string     `json:"label,omitempty" validate:"optional"`
	Description string     `json:"description,omitempty" validate:"optional"`
	Trigger     bool       `json:"trigger"`
	Inputs      []string   `json:"inputs"`
	Outputs     []string   `json:"outputs"`
	Params      []paramDTO `json:"params"`
} // @name NodeType

type nodeTypeList struct {
	NodeTypes []nodeTypeDTO `json:"nodeTypes"`
} // @name NodeTypeList

// listNodeTypes lists what can be placed in a workflow, for the editor's palette and forms.
//
//	@Summary	List node types
//	@ID			listNodeTypes
//	@Tags		workflows
//	@Success	200	{object}	nodeTypeList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/node-types [get]
func (s *Server) listNodeTypes(_ context.Context, c *app.RequestContext) {
	types := s.nodeTypes.Types()

	out := nodeTypeList{NodeTypes: make([]nodeTypeDTO, len(types))}
	for i, t := range types {
		dto := nodeTypeDTO{
			Name: t.Name, Label: t.Label, Description: t.Description, Trigger: t.Trigger,
			Inputs: nonNil(t.Inputs), Outputs: nonNil(t.Outputs),
			Params: make([]paramDTO, len(t.Params)),
		}

		for j, p := range t.Params {
			dto.Params[j] = paramDTO{
				Name: p.Name, Label: p.Label, Description: p.Description, Kind: string(p.Kind),
				Required: p.Required, Default: p.Default, Options: p.Options,
			}
		}

		out.NodeTypes[i] = dto
	}

	c.JSON(http.StatusOK, out)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// listWorkflows lists the user's workflows.
//
//	@Summary	List workflows
//	@ID			listWorkflows
//	@Tags		workflows
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	workflowPage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows [get]
func (s *Server) listWorkflows(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.workflows.List(ctx, p, f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := workflowPage{Workflows: make([]workflowDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, w := range page.Items {
		out.Workflows[i] = toWorkflowDTO(w)
	}

	c.JSON(http.StatusOK, out)
}

// createWorkflow saves a new, inactive workflow after validating it.
//
//	@Summary	Create a workflow
//	@ID			createWorkflow
//	@Tags		workflows
//	@Param		body	body		definitionRequest	true	"Workflow"
//	@Success	201		{object}	workflowDTO
//	@Failure	400		{object}	errorBody	"invalid_input, or invalid_workflow with problems"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows [post]
func (s *Server) createWorkflow(ctx context.Context, c *app.RequestContext) {
	var req definitionRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	w, err := s.workflows.Create(ctx, p, req.definition())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toWorkflowDTO(w))
}

// getWorkflow returns one workflow.
//
//	@Summary	Get a workflow
//	@ID			getWorkflow
//	@Tags		workflows
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	workflowDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id} [get]
func (s *Server) getWorkflow(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	w, err := s.workflows.Get(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}

// updateWorkflow replaces the definition. An active workflow restarts with it.
//
//	@Summary	Update a workflow
//	@ID			updateWorkflow
//	@Tags		workflows
//	@Param		id		path		string					true	"Workflow ID"
//	@Param		body	body		updateWorkflowRequest	true	"Workflow and the version it was edited from"
//	@Success	200		{object}	workflowDTO
//	@Failure	400		{object}	errorBody	"invalid_input, or invalid_workflow with problems"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"conflict: someone saved a newer version"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id} [put]
func (s *Server) updateWorkflow(ctx context.Context, c *app.RequestContext) {
	var req updateWorkflowRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	w, err := s.workflows.Update(ctx, p, domain.WorkflowID(c.Param("id")), req.Version, req.definition())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}

// deleteWorkflow stops and deletes a workflow with its executions.
//
//	@Summary	Delete a workflow
//	@ID			deleteWorkflow
//	@Tags		workflows
//	@Param		id	path	string	true	"Workflow ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id} [delete]
func (s *Server) deleteWorkflow(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.workflows.Delete(ctx, p, domain.WorkflowID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// activateWorkflow starts the workflow's triggers.
//
//	@Summary	Activate a workflow
//	@ID			activateWorkflow
//	@Tags		workflows
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	workflowDTO
//	@Failure	400	{object}	errorBody	"invalid_workflow"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/activate [post]
func (s *Server) activateWorkflow(ctx context.Context, c *app.RequestContext) {
	s.setActive(ctx, c, s.workflows.Activate)
}

// deactivateWorkflow stops the workflow's triggers.
//
//	@Summary	Deactivate a workflow
//	@ID			deactivateWorkflow
//	@Tags		workflows
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	workflowDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/deactivate [post]
func (s *Server) deactivateWorkflow(ctx context.Context, c *app.RequestContext) {
	s.setActive(ctx, c, s.workflows.Deactivate)
}

func (s *Server) setActive(
	ctx context.Context,
	c *app.RequestContext,
	change func(context.Context, auth.Principal, domain.WorkflowID) (domain.Workflow, error),
) {
	p, _ := principal(c)

	w, err := change(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}

// runWorkflow runs the saved workflow once and waits for it to finish. The body is optional.
//
//	@Summary	Run a workflow now
//	@ID			runWorkflow
//	@Tags		executions
//	@Param		id		path		string		true	"Workflow ID"
//	@Param		body	body		runRequest	false	"Trigger to start from"
//	@Success	200		{object}	executionDTO
//	@Failure	400		{object}	errorBody	"invalid_input or invalid_workflow"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/run [post]
func (s *Server) runWorkflow(ctx context.Context, c *app.RequestContext) {
	var req runRequest

	if len(c.Request.Body()) > 0 {
		if err := decodeJSON(c, &req); err != nil {
			s.fail(ctx, c, err)
			return
		}
	}

	p, _ := principal(c)

	exec, err := s.workflows.Run(ctx, p, domain.WorkflowID(c.Param("id")), domain.NodeID(req.Trigger))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toExecutionDTO(exec))
}

// listExecutions lists a workflow's executions, newest first.
//
//	@Summary	List executions
//	@ID			listExecutions
//	@Tags		executions
//	@Param		id		path		string	true	"Workflow ID"
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	executionPage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/executions [get]
func (s *Server) listExecutions(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.workflows.Executions(ctx, p, domain.WorkflowID(c.Param("id")), f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := executionPage{Executions: make([]executionDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, e := range page.Items {
		out.Executions[i] = toExecutionDTO(e)
	}

	c.JSON(http.StatusOK, out)
}

// getExecution returns one execution with its node runs.
//
//	@Summary	Get an execution
//	@ID			getExecution
//	@Tags		executions
//	@Param		id	path		string	true	"Workflow ID"
//	@Param		eid	path		string	true	"Execution ID"
//	@Success	200	{object}	executionDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/executions/{eid} [get]
func (s *Server) getExecution(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	e, err := s.workflows.Execution(ctx, p, domain.WorkflowID(c.Param("id")), domain.ExecutionID(c.Param("eid")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toExecutionDTO(e))
}
