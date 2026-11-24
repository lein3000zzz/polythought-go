package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/hooks"
)

// HookService is inbound webhooks (D439): each trigger's secret address for its owner,
// and the calls to them.
type HookService interface {
	Address(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID, node domain.NodeID) (hooks.Address, error)
	Renew(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID, node domain.NodeID) (hooks.Address, error)
	Call(ctx context.Context, secret, address string, body []byte, query map[string][]string) (domain.ExecutionID, error)
}

type hookDTO struct {
	// URL is the secret address a service calls with a POST: whoever has it can start
	// the workflow.
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"createdAt"`
	// Enabled says an admin lets inbound webhooks take calls on this instance.
	Enabled bool `json:"enabled"`
	// Listening says the workflow is on, taking calls.
	Listening bool `json:"listening"`
} // @name Hook

type hookCallDTO struct {
	// ExecutionID is the run the call starts, once those before it end.
	ExecutionID string `json:"executionId"`
} // @name HookCall

func toHookDTO(a hooks.Address) hookDTO {
	return hookDTO{URL: a.URL, CreatedAt: a.CreatedAt, Enabled: a.Enabled, Listening: a.Listening}
}

// getHook is an Inbound webhook trigger's secret address, made the first time (D439). Its
// owner's alone, in the browser: no token or agent gets it.
//
//	@Summary	Inbound webhook address
//	@ID			getHook
//	@Tags		workflows
//	@Param		id		path		string	true	"Workflow ID"
//	@Param		node	path		string	true	"Node ID of an Inbound webhook trigger, as saved"
//	@Success	200		{object}	hookDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Router		/workflows/{id}/hooks/{node} [get]
func (s *Server) getHook(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	a, err := s.hooks.Address(ctx, p.User.ID, domain.WorkflowID(c.Param("id")), domain.NodeID(c.Param("node")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toHookDTO(a))
}

// renewHook gives the trigger a new secret address; the old one answers 404 from now.
//
//	@Summary	Renew an inbound webhook address
//	@ID			renewHook
//	@Tags		workflows
//	@Param		id		path		string	true	"Workflow ID"
//	@Param		node	path		string	true	"Node ID of an Inbound webhook trigger, as saved"
//	@Success	200		{object}	hookDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Router		/workflows/{id}/hooks/{node}/renew [post]
func (s *Server) renewHook(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	a, err := s.hooks.Renew(ctx, p.User.ID, domain.WorkflowID(c.Param("id")), domain.NodeID(c.Param("node")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toHookDTO(a))
}

// callHook takes a call to an inbound webhook, /hooks/<secret>, outside the API: no
// session, no token, the secret its only proof. Answered 202 with the run's ID at once,
// the run starting once the workflow's earlier ones end.
func (s *Server) callHook(ctx context.Context, c *app.RequestContext) {
	if s.hooks == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	query := map[string][]string{}
	c.QueryArgs().VisitAll(func(k, v []byte) {
		query[string(k)] = append(query[string(k)], string(v))
	})

	id, err := s.hooks.Call(ctx, strings.TrimSpace(c.Param("secret")), c.ClientIP(), c.Request.Body(), query)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusAccepted, hookCallDTO{ExecutionID: string(id)})
}

// hasHooks answers 404 where the server has no inbound webhooks.
func (s *Server) hasHooks(ctx context.Context, c *app.RequestContext) {
	if s.hooks == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}
