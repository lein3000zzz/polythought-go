package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/spend"
)

// SpendService is what users' agents spend this month, and the caps on it (D277).
type SpendService interface {
	Spend(ctx context.Context, owner domain.UserID) (spend.Spend, error)
	SetKillStopsAgents(ctx context.Context, owner domain.UserID, on bool) error
}

// agentSpendDTO is a user's agent spend this calendar month, UTC, and the caps on it.
type agentSpendDTO struct {
	Month  time.Time `json:"month" format:"date-time"`
	Resets time.Time `json:"resets" format:"date-time"`
	// Tokens are every call's; dollars, what providers reported, which only some do
	// (OpenRouter): CostedCalls of Calls.
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	TotalTokens  int64  `json:"totalTokens"`
	Dollars      string `json:"dollars" example:"4.20"`
	Calls        int    `json:"calls"`
	CostedCalls  int    `json:"costedCalls"`
	// Caps are those that apply, 0 for none; Own, an admin's for the user, those left out
	// being the instance's.
	Caps agentCapsDTO `json:"caps"`
	Own  agentCapsDTO `json:"own"`
	// KillStopsAgents is the user's choice that their kill switch stops their agents too.
	KillStopsAgents bool `json:"killStopsAgents"`
} // @name AgentSpend

type agentChoicesRequest struct {
	KillStopsAgents bool `json:"killStopsAgents"`
} // @name AgentChoicesRequest

func toAgentSpendDTO(sp spend.Spend) agentSpendDTO {
	u := sp.Usage

	return agentSpendDTO{
		Month: u.Month, Resets: sp.Resets, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		TotalTokens: u.TotalTokens(), Dollars: u.Dollars.StringFixed(2), Calls: u.Calls, CostedCalls: u.CostedCalls,
		Caps: toAgentCapsDTO(sp.Caps), Own: toAgentCapsDTO(sp.Own), KillStopsAgents: sp.KillStopsAgents,
	}
}

// myAgentSpend is what your AI agent nodes spent this month, and the caps on it.
//
//	@Summary	Your agent spend this month
//	@ID			myAgentSpend
//	@Tags		me
//	@Success	200	{object}	agentSpendDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/agent-spend [get]
func (s *Server) myAgentSpend(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	s.writeSpend(ctx, c, p.User.ID)
}

// setAgentChoices records your choices for your agents: whether your kill switch stops
// them too (D276).
//
//	@Summary	Set your agent choices
//	@ID			setAgentChoices
//	@Tags		me
//	@Param		body	body		agentChoicesRequest	true	"Your choices"
//	@Success	200		{object}	agentSpendDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/agents [put]
func (s *Server) setAgentChoices(ctx context.Context, c *app.RequestContext) {
	var req agentChoicesRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	if s.spend == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	p, _ := principal(c)

	if err := s.spend.SetKillStopsAgents(ctx, p.User.ID, req.KillStopsAgents); err != nil {
		s.fail(ctx, c, err)
		return
	}

	s.writeSpend(ctx, c, p.User.ID)
}

// userAgentSpend is admin only: what a user's agents spent this month, beside their caps.
//
//	@Summary	A user's agent spend this month
//	@ID			userAgentSpend
//	@Tags		admin
//	@Param		id	path		string	true	"User ID"
//	@Success	200	{object}	agentSpendDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not an admin"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/agent-spend [get]
func (s *Server) userAgentSpend(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	if !p.User.IsAdmin() {
		s.fail(ctx, c, domain.ErrForbidden)
		return
	}

	s.writeSpend(ctx, c, domain.UserID(c.Param("id")))
}

func (s *Server) writeSpend(ctx context.Context, c *app.RequestContext, owner domain.UserID) {
	if s.spend == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	sp, err := s.spend.Spend(ctx, owner)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toAgentSpendDTO(sp))
}
