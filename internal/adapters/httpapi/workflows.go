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

type nodeDTO struct {
	ID             string         `json:"id"`
	Type           string         `json:"type"`
	Name           string         `json:"name,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	ContinueOnFail bool           `json:"continueOnFail,omitempty"`
}

type connectionDTO struct {
	From     string `json:"from"`
	FromPort string `json:"fromPort"`
	To       string `json:"to"`
	ToPort   string `json:"toPort"`
}

type workflowDTO struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Active      bool            `json:"active"`
	Version     int             `json:"version"`
	Nodes       []nodeDTO       `json:"nodes"`
	Connections []connectionDTO `json:"connections"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

func toWorkflowDTO(w domain.Workflow) workflowDTO {
	out := workflowDTO{
		ID: string(w.ID), Name: w.Name, Active: w.Active, Version: w.Version,
		Nodes:       make([]nodeDTO, len(w.Nodes)),
		Connections: make([]connectionDTO, len(w.Connections)),
		CreatedAt:   w.CreatedAt, UpdatedAt: w.UpdatedAt,
	}

	for i, n := range w.Nodes {
		out.Nodes[i] = nodeDTO{ID: string(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail}
	}

	for i, c := range w.Connections {
		out.Connections[i] = connectionDTO{From: string(c.From), FromPort: c.FromPort, To: string(c.To), ToPort: c.ToPort}
	}

	return out
}

// definitionRequest is the editable part of a workflow in create and update bodies.
type definitionRequest struct {
	Name        string          `json:"name"`
	Nodes       []nodeDTO       `json:"nodes"`
	Connections []connectionDTO `json:"connections"`
}

func (r definitionRequest) definition() workflows.Definition {
	def := workflows.Definition{
		Name:        r.Name,
		Nodes:       make([]domain.NodeSpec, len(r.Nodes)),
		Connections: make([]domain.Connection, len(r.Connections)),
	}

	for i, n := range r.Nodes {
		def.Nodes[i] = domain.NodeSpec{ID: domain.NodeID(n.ID), Type: n.Type, Name: n.Name, Params: n.Params, ContinueOnFail: n.ContinueOnFail}
	}

	for i, c := range r.Connections {
		def.Connections[i] = domain.Connection{From: domain.NodeID(c.From), FromPort: c.FromPort, To: domain.NodeID(c.To), ToPort: c.ToPort}
	}

	return def
}

type nodeRunDTO struct {
	NodeID     string         `json:"nodeId"`
	Status     string         `json:"status"`
	StartedAt  time.Time      `json:"startedAt"`
	FinishedAt time.Time      `json:"finishedAt"`
	Input      map[string]int `json:"input,omitempty"`
	Output     map[string]int `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type executionDTO struct {
	ID              string       `json:"id"`
	WorkflowVersion int          `json:"workflowVersion"`
	TriggeredBy     string       `json:"triggeredBy"`
	Status          string       `json:"status"`
	StartedAt       time.Time    `json:"startedAt"`
	FinishedAt      *time.Time   `json:"finishedAt"`
	DurationMs      int64        `json:"durationMs"`
	Error           string       `json:"error,omitempty"`
	Runs            []nodeRunDTO `json:"runs"`
}

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

type paramDTO struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Default  any    `json:"default,omitempty"`
}

type nodeTypeDTO struct {
	Name    string     `json:"name"`
	Trigger bool       `json:"trigger"`
	Inputs  []string   `json:"inputs"`
	Outputs []string   `json:"outputs"`
	Params  []paramDTO `json:"params"`
}

func (s *Server) listNodeTypes(_ context.Context, c *app.RequestContext) {
	types := s.nodeTypes.Types()

	out := make([]nodeTypeDTO, len(types))
	for i, t := range types {
		out[i] = nodeTypeDTO{
			Name: t.Name, Trigger: t.Trigger,
			Inputs: nonNil(t.Inputs), Outputs: nonNil(t.Outputs),
			Params: make([]paramDTO, len(t.Params)),
		}

		for j, p := range t.Params {
			out[i].Params[j] = paramDTO{Name: p.Name, Kind: string(p.Kind), Required: p.Required, Default: p.Default}
		}
	}

	c.JSON(http.StatusOK, map[string]any{"nodeTypes": out})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

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

	out := make([]workflowDTO, len(page.Items))
	for i, w := range page.Items {
		out[i] = toWorkflowDTO(w)
	}

	c.JSON(http.StatusOK, map[string]any{"workflows": out, "nextCursor": page.NextCursor})
}

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

func (s *Server) getWorkflow(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	w, err := s.workflows.Get(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}

func (s *Server) updateWorkflow(ctx context.Context, c *app.RequestContext) {
	var req struct {
		definitionRequest

		Version int `json:"version"`
	}

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

func (s *Server) deleteWorkflow(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.workflows.Delete(ctx, p, domain.WorkflowID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) activateWorkflow(ctx context.Context, c *app.RequestContext) {
	s.setActive(ctx, c, s.workflows.Activate)
}

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

func (s *Server) runWorkflow(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Trigger string `json:"trigger"`
	}

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

	out := make([]executionDTO, len(page.Items))
	for i, e := range page.Items {
		out[i] = toExecutionDTO(e)
	}

	c.JSON(http.StatusOK, map[string]any{"executions": out, "nextCursor": page.NextCursor})
}

func (s *Server) getExecution(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	e, err := s.workflows.Execution(ctx, p, domain.WorkflowID(c.Param("id")), domain.ExecutionID(c.Param("eid")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toExecutionDTO(e))
}
