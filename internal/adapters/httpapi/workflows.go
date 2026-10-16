package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
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
	// Notes explain the node to whoever reads the workflow; they never affect execution.
	Notes string `json:"notes,omitempty" validate:"optional"`
} // @name Node

func toNodeDTO(n domain.NodeSpec) nodeDTO {
	return nodeDTO{
		ID: string(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
		Position: positionDTO{X: n.Position.X, Y: n.Position.Y}, Notes: n.Notes,
	}
}

func (n nodeDTO) spec() domain.NodeSpec {
	return domain.NodeSpec{
		ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail,
		Position: domain.CanvasPosition{X: n.Position.X, Y: n.Position.Y}, Notes: n.Notes,
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
	Status     string         `json:"status" enums:"succeeded,failed,skipped,waiting,cancelled"`
	StartedAt  time.Time      `json:"startedAt" format:"date-time"`
	FinishedAt *time.Time     `json:"finishedAt,omitempty" validate:"optional" format:"date-time"` // absent while it waits
	Input      map[string]int `json:"input,omitempty" validate:"optional"`                         // items per input port
	Output     map[string]int `json:"output,omitempty" validate:"optional"`                        // items per output port
	Error      string         `json:"error,omitempty" validate:"optional"`
	// Samples are the first items of each output port. Only runs started by hand keep
	// them, and execution lists leave them out.
	Samples map[string][]itemSampleDTO `json:"samples,omitempty" validate:"optional"`
} // @name NodeRun

// itemSampleDTO is an item a node emitted, and why it failed if it went to the error port.
type itemSampleDTO struct {
	JSON  map[string]any `json:"json,omitempty" validate:"optional"`
	Error string         `json:"error,omitempty" validate:"optional"`
} // @name ItemSample

type executionDTO struct {
	ID              string       `json:"id"`
	WorkflowVersion int          `json:"workflowVersion"`
	TriggeredBy     string       `json:"triggeredBy"`
	Status          string       `json:"status" enums:"running,waiting,succeeded,failed,cancelled"`
	StartedAt       time.Time    `json:"startedAt" format:"date-time"`
	FinishedAt      *time.Time   `json:"finishedAt,omitempty" validate:"optional" format:"date-time"`
	DurationMs      int64        `json:"durationMs"`
	Error           string       `json:"error,omitempty" validate:"optional"`
	Runs            []nodeRunDTO `json:"runs"`
	// WaitingOn is the node a waiting execution paused at, and WaitingUntil when the wait
	// ends at the latest.
	WaitingOn    string     `json:"waitingOn,omitempty" validate:"optional"`
	WaitingUntil *time.Time `json:"waitingUntil,omitempty" validate:"optional" format:"date-time"`
} // @name Execution

// toExecutionDTO describes e, with the runs' item samples when withSamples is set.
func toExecutionDTO(e domain.Execution, withSamples bool) executionDTO {
	out := executionDTO{
		ID: string(e.ID), WorkflowVersion: e.WorkflowVersion, TriggeredBy: string(e.TriggeredBy), Status: string(e.Status),
		StartedAt: e.StartedAt, FinishedAt: optionalTime(e.FinishedAt), DurationMs: e.Duration().Milliseconds(),
		Error: e.Error, Runs: make([]nodeRunDTO, len(e.Runs)),
		WaitingOn: string(e.WaitingOn), WaitingUntil: optionalTime(e.WaitingUntil),
	}

	for i, r := range e.Runs {
		out.Runs[i] = nodeRunDTO{
			NodeID: string(r.NodeID), Status: string(r.Status), StartedAt: r.StartedAt, FinishedAt: optionalTime(r.FinishedAt),
			Input: r.Input, Output: r.Output, Error: r.Error,
		}

		if withSamples {
			out.Runs[i].Samples = toSampleDTOs(r.Samples)
		}
	}

	return out
}

func toSampleDTOs(samples map[string][]domain.ItemSample) map[string][]itemSampleDTO {
	if len(samples) == 0 {
		return nil
	}

	out := make(map[string][]itemSampleDTO, len(samples))
	for port, list := range samples {
		out[port] = make([]itemSampleDTO, len(list))
		for i, s := range list {
			out[port][i] = itemSampleDTO{JSON: itemJSON(s.JSON), Error: s.Error}
		}
	}

	return out
}

// itemJSON decodes a stored item for the response. Numbers stay as written, so 0.020 is
// not turned into a float. Items are objects; anything else is left out.
func itemJSON(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var item map[string]any
	if err := dec.Decode(&item); err != nil {
		return nil
	}

	return item
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
	Kind        string `json:"kind" enums:"string,number,decimal,bool,list,duration,wallet,tokens,credential,code"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty" validate:"optional"`
	// Options are the allowed values of a string, or of each element of a list.
	Options []string `json:"options,omitempty" validate:"optional"`
	// Sensitive values, like a webhook URL, are personal: sharing leaves them out.
	Sensitive bool `json:"sensitive,omitempty" validate:"optional"`
	// CredentialType is the kind of credential a credential param takes, one of the
	// user's of that type.
	CredentialType string `json:"credentialType,omitempty" validate:"optional"`
	// Field marks a string, or each element of a list, as a path to a field of the items
	// the node receives, like spread or outcomes.0.price. FieldKinds, when present, are
	// the kinds that field may be.
	Field      bool     `json:"field,omitempty" validate:"optional"`
	FieldKinds []string `json:"fieldKinds,omitempty" validate:"optional" enums:"text,number,decimal,bool,time,list,object,any"`
} // @name ParamSpec

type nodeTypeDTO struct {
	Name        string     `json:"name"`
	Label       string     `json:"label,omitempty" validate:"optional"`
	Description string     `json:"description,omitempty" validate:"optional"`
	Trigger     bool       `json:"trigger"`
	Inputs      []string   `json:"inputs"`
	Outputs     []string   `json:"outputs"`
	Params      []paramDTO `json:"params"`
	// OutputsParam names a list param whose values are more output ports, after outputs.
	OutputsParam string `json:"outputsParam,omitempty" validate:"optional"`
	// Items describes what the node emits; absent when nobody knows.
	Items *itemsDTO `json:"items,omitempty" validate:"optional"`
	// Code says it runs code its owner wrote: only users who may write code (the
	// mayWriteCode of /me) can save it.
	Code bool `json:"code"`
} // @name NodeType

// itemsDTO describes the items a node emits on every output port: fields of its own, or
// the items it received, whole or with only the fields a list param names. The error
// port carries the items received.
type itemsDTO struct {
	Fields []fieldDTO `json:"fields"`
	Passed bool       `json:"passed"`
	Picked string     `json:"picked,omitempty" validate:"optional"`
} // @name NodeItems

type fieldDTO struct {
	// Name is empty for a list's elements.
	Name        string   `json:"name"`
	Kind        string   `json:"kind" enums:"text,number,decimal,bool,time,list,object,any"`
	Description string   `json:"description,omitempty" validate:"optional"`
	Optional    bool     `json:"optional"`
	Nullable    bool     `json:"nullable"`
	Options     []string `json:"options,omitempty" validate:"optional"`
	// Of describes a list's elements.
	Of *fieldDTO `json:"of,omitempty" validate:"optional"`
	// Fields are an object's.
	Fields []fieldDTO `json:"fields,omitempty" validate:"optional"`
} // @name ItemField

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
			Params: make([]paramDTO, len(t.Params)), OutputsParam: t.OutputsParam,
			Items: toItemsDTO(t.Items), Code: t.Code,
		}

		for j, p := range t.Params {
			dto.Params[j] = toParamDTO(p)
		}

		out.NodeTypes[i] = dto
	}

	c.JSON(http.StatusOK, out)
}

func toItemsDTO(it *engine.Items) *itemsDTO {
	if it == nil {
		return nil
	}

	return &itemsDTO{Fields: toFieldDTOs(it.Fields), Passed: it.Passed, Picked: it.Picked}
}

func toFieldDTOs(fields []engine.Field) []fieldDTO {
	out := make([]fieldDTO, len(fields))
	for i, f := range fields {
		out[i] = toFieldDTO(f)
	}

	return out
}

func toFieldDTO(f engine.Field) fieldDTO {
	dto := fieldDTO{
		Name: f.Name, Kind: string(f.Kind), Description: f.Description,
		Optional: f.Optional, Nullable: f.Nullable, Options: f.Options,
	}

	if f.Of != nil {
		of := toFieldDTO(*f.Of)
		dto.Of = &of
	}

	if len(f.Fields) > 0 {
		dto.Fields = toFieldDTOs(f.Fields)
	}

	return dto
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

// runWorkflow runs the saved workflow once and waits for it to finish: from its manual
// trigger, or its schedule as if it fired. The execution keeps samples of each node's
// output. The body is optional.
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

	c.JSON(http.StatusOK, toExecutionDTO(exec, true))
}

// listExecutions lists a workflow's executions, newest first, without item samples.
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
		out.Executions[i] = toExecutionDTO(e, false)
	}

	c.JSON(http.StatusOK, out)
}

// cancelExecution cancels an execution that waits.
//
//	@Summary	Cancel a waiting execution
//	@ID			cancelExecution
//	@Tags		executions
//	@Param		id	path		string	true	"Workflow ID"
//	@Param		eid	path		string	true	"Execution ID"
//	@Success	200	{object}	executionDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	409	{object}	errorBody	"not_waiting"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/executions/{eid}/cancel [post]
func (s *Server) cancelExecution(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	e, err := s.workflows.CancelExecution(ctx, p, domain.WorkflowID(c.Param("id")), domain.ExecutionID(c.Param("eid")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toExecutionDTO(e, false))
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

	c.JSON(http.StatusOK, toExecutionDTO(e, true))
}
