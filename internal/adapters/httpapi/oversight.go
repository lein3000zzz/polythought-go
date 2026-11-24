package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// An admin's oversight of every user's workflows (D435): all of them, the busiest first;
// one read as its owner has it, personal settings hidden, its runs listed without their
// items; switched off, held off until released, and released.

type adminWorkflowDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OwnerID    string `json:"ownerId"`
	OwnerEmail string `json:"ownerEmail"`
	Active     bool   `json:"active"`
	// Hold is an admin's holding it off; absent while none does.
	Hold *holdDTO `json:"hold,omitempty" validate:"optional"`
	// Triggers are its triggers' node types, each once.
	Triggers []string `json:"triggers"`
	// Hour, Day and Week are its runs started in the last hour, day and week.
	Hour runCountsDTO `json:"hour"`
	Day  runCountsDTO `json:"day"`
	Week runCountsDTO `json:"week"`
	// Last is its latest run; absent when none is kept.
	Last *lastRunDTO `json:"last,omitempty" validate:"optional"`
} // @name AdminWorkflow

type adminWorkflowsDTO struct {
	// Workflows are every user's, the busiest first: by runs in the last hour, day, week.
	Workflows []adminWorkflowDTO `json:"workflows"`
	At        time.Time          `json:"at" format:"date-time"`
} // @name AdminWorkflows

type adminSwitchOffRequest struct {
	// Reason is why, for its owner, who is told.
	Reason string `json:"reason"`
	// Hold keeps it off until an admin releases it.
	Hold bool `json:"hold,omitempty" validate:"optional"`
} // @name AdminSwitchOffRequest

// adminWorkflows lists every user's workflows for an admin, the busiest first (D435).
//
//	@Summary	Every user's workflows
//	@ID			listAdminWorkflows
//	@Tags		admin
//	@Success	200	{object}	adminWorkflowsDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"not an admin"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/workflows [get]
func (s *Server) adminWorkflows(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, at, err := s.stats.Workflows(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := adminWorkflowsDTO{Workflows: make([]adminWorkflowDTO, len(list)), At: at}

	for i, w := range list {
		dto := adminWorkflowDTO{
			ID: string(w.ID), Name: w.Name, OwnerID: string(w.Owner.ID), OwnerEmail: w.Owner.Email, Active: w.Active,
			Triggers: w.Triggers, Hour: toRunCounts(w.Runs.Hour), Day: toRunCounts(w.Runs.Day), Week: toRunCounts(w.Runs.Week),
		}

		if dto.Triggers == nil {
			dto.Triggers = []string{}
		}

		if h := w.Hold; h != nil {
			dto.Hold = &holdDTO{Reason: h.Reason, At: h.At}
		}

		if l := w.Runs.Last; l != nil {
			dto.Last = &lastRunDTO{ID: string(l.ID), Status: string(l.Status), StartedAt: l.StartedAt, FinishedAt: l.FinishedAt}
		}

		out.Workflows[i] = dto
	}

	c.JSON(http.StatusOK, out)
}

// adminWorkflow is one of a user's workflows for an admin, read-only: its personal
// settings hidden (D435).
//
//	@Summary	A user's workflow
//	@ID			getAdminWorkflow
//	@Tags		admin
//	@Param		id	path		string	true	"The owner's ID"
//	@Param		wid	path		string	true	"Workflow ID"
//	@Success	200	{object}	workflowDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"not an admin"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/workflows/{wid} [get]
func (s *Server) adminWorkflow(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	w, err := s.workflows.AdminGet(ctx, p, domain.UserID(c.Param("id")), domain.WorkflowID(c.Param("wid")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}

// adminWorkflowExecutions lists a user's workflow's runs for an admin, newest first,
// without their items (D435).
//
//	@Summary	A user's workflow's runs
//	@ID			listAdminWorkflowExecutions
//	@Tags		admin
//	@Param		id		path		string	true	"The owner's ID"
//	@Param		wid		path		string	true	"Workflow ID"
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	executionPage
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"not an admin"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/workflows/{wid}/executions [get]
func (s *Server) adminWorkflowExecutions(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.workflows.AdminExecutions(ctx, p, domain.UserID(c.Param("id")), domain.WorkflowID(c.Param("wid")), f)
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

// adminWorkflowHistory lists who switched a user's workflow and why, for an admin (D435).
//
//	@Summary	A user's workflow's history
//	@ID			getAdminWorkflowHistory
//	@Tags		admin
//	@Param		id		path		string	true	"The owner's ID"
//	@Param		wid		path		string	true	"Workflow ID"
//	@Param		kinds	query		string	false	"Some kinds alone, comma-separated: on_off, trading, reset, kill_switch"
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	historyPage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"not an admin"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/workflows/{wid}/history [get]
func (s *Server) adminWorkflowHistory(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.history.AdminList(ctx, p, domain.UserID(c.Param("id")), domain.WorkflowID(c.Param("wid")), kindsOf(c), f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toHistoryPage(page))
}

// adminSwitchOff switches off a user's workflow for an admin, saying why, and with hold
// keeps it off until an admin releases it; its owner is told (D435).
//
//	@Summary	Switch off a user's workflow
//	@ID			adminSwitchOffWorkflow
//	@Tags		admin
//	@Param		id		path		string					true	"The owner's ID"
//	@Param		wid		path		string					true	"Workflow ID"
//	@Param		body	body		adminSwitchOffRequest	true	"Why, and whether to hold it off"
//	@Success	200		{object}	workflowDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"not an admin"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"awaits_confirmation: an admin's token's work waits for the admin to confirm it (D410)"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/workflows/{wid}/switch-off [post]
func (s *Server) adminSwitchOff(ctx context.Context, c *app.RequestContext) {
	var req adminSwitchOffRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	w, err := s.workflows.AdminSwitchOff(ctx, p, domain.UserID(c.Param("id")), domain.WorkflowID(c.Param("wid")), req.Reason, req.Hold)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}

// adminRelease lets a user's workflow an admin held off be switched on again; its owner
// is told (D435).
//
//	@Summary	Release a user's workflow
//	@ID			adminReleaseWorkflow
//	@Tags		admin
//	@Param		id	path		string	true	"The owner's ID"
//	@Param		wid	path		string	true	"Workflow ID"
//	@Success	200	{object}	workflowDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"not an admin"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	409	{object}	errorBody	"awaits_confirmation: an admin's token's work waits for the admin to confirm it (D410)"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/workflows/{wid}/release [post]
func (s *Server) adminRelease(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	w, err := s.workflows.AdminRelease(ctx, p, domain.UserID(c.Param("id")), domain.WorkflowID(c.Param("wid")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toWorkflowDTO(w))
}
