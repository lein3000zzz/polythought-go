package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// ConfirmationService holds tokens' and agents' live actions for their users (D406).
type ConfirmationService interface {
	Recent(ctx context.Context, p auth.Principal) ([]domain.Confirmation, error)
	Confirm(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
	Refuse(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error)
	SetUnconfirmed(ctx context.Context, p auth.Principal, u domain.Unconfirmed) error
}

// confirmationDTO is a token's or an agent's live action waiting for you, or how it ended.
type confirmationDTO struct {
	ID         string `json:"id"`
	Kind       string `json:"kind" enums:"go_live,switch_on,run,change,kill_off"`
	WorkflowID string `json:"workflowId,omitempty" validate:"optional"`
	// Asker is who asked: the agent by name, or "an API token".
	Asker string `json:"asker"`
	// What says what it does, such as Run "Spread watch" by hand, which trades live.
	What   string `json:"what"`
	Status string `json:"status" enums:"pending,confirmed,done,failed,refused,expired"`
	// Result is what came of it: an execution, or why it failed.
	Result    string     `json:"result,omitempty" validate:"optional"`
	CreatedAt time.Time  `json:"createdAt" format:"date-time"`
	ExpiresAt time.Time  `json:"expiresAt" format:"date-time"`
	DecidedAt *time.Time `json:"decidedAt,omitempty" validate:"optional" format:"date-time"`
} // @name Confirmation

type confirmationList struct {
	Confirmations []confirmationDTO `json:"confirmations"`
} // @name ConfirmationList

func toConfirmationDTO(c domain.Confirmation) confirmationDTO {
	return confirmationDTO{
		ID: string(c.ID), Kind: string(c.Kind), WorkflowID: string(c.WorkflowID), Asker: c.Asker(), What: c.What,
		Status: string(c.Status), Result: c.Result, CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt, DecidedAt: optionalTime(c.DecidedAt),
	}
}

// listConfirmations is your tokens' and agents' live actions, waiting for you or ended,
// the newest first (D406).
//
//	@Summary	Live actions waiting for you
//	@ID			listConfirmations
//	@Tags		confirmations
//	@Success	200	{object}	confirmationList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/confirmations [get]
func (s *Server) listConfirmations(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.confirmations.Recent(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := confirmationList{Confirmations: make([]confirmationDTO, len(list))}
	for i, conf := range list {
		out.Confirmations[i] = toConfirmationDTO(conf)
	}

	c.JSON(http.StatusOK, out)
}

// confirmConfirmation carries out a waiting live action as it was asked, at a browser only;
// it goes on in the background, and the list says how it ended.
//
//	@Summary	Confirm a live action
//	@ID			confirmConfirmation
//	@Tags		confirmations
//	@Param		id	path		string	true	"Confirmation ID"
//	@Success	200	{object}	confirmationDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not from a browser session"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	409	{object}	errorBody	"conflict: refused, expired or confirmed already"
//	@Security	SessionCookie
//	@Router		/me/confirmations/{id}/confirm [post]
func (s *Server) confirmConfirmation(ctx context.Context, c *app.RequestContext) {
	s.decideConfirmation(ctx, c, s.confirmations.Confirm)
}

// refuseConfirmation ends a waiting live action without carrying it out, at a browser
// only.
//
//	@Summary	Refuse a live action
//	@ID			refuseConfirmation
//	@Tags		confirmations
//	@Param		id	path		string	true	"Confirmation ID"
//	@Success	200	{object}	confirmationDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not from a browser session"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	409	{object}	errorBody	"conflict: refused, expired or confirmed already"
//	@Security	SessionCookie
//	@Router		/me/confirmations/{id}/refuse [post]
func (s *Server) refuseConfirmation(ctx context.Context, c *app.RequestContext) {
	s.decideConfirmation(ctx, c, s.confirmations.Refuse)
}

func (s *Server) decideConfirmation(
	ctx context.Context, c *app.RequestContext,
	decide func(context.Context, auth.Principal, domain.ConfirmationID) (domain.Confirmation, error),
) {
	p, _ := principal(c)

	conf, err := decide(ctx, p, domain.ConfirmationID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toConfirmationDTO(conf))
}

// liveConfirmDTO says which of your tokens' and agents' live actions wait for you to
// confirm them (D406): all of them to start with.
type liveConfirmDTO struct {
	// GoLive is setting a workflow to trade live, or changing its live wallet.
	GoLive bool `json:"goLive"`
	// Run is switching on, or running by hand, a workflow that trades live.
	Run bool `json:"run"`
	// Change is saving a new graph for a workflow that trades live and is on.
	Change bool `json:"change"`
	// KillOff is turning the kill switch off.
	KillOff bool `json:"killOff"`
} // @name LiveConfirm

func toLiveConfirmDTO(u domain.Unconfirmed) liveConfirmDTO {
	return liveConfirmDTO{GoLive: !u.GoLive, Run: !u.Run, Change: !u.Change, KillOff: !u.KillOff}
}

// liveConfirmRequest changes the switches it names, and leaves the others as they are.
type liveConfirmRequest struct {
	GoLive  *bool `json:"goLive,omitempty" validate:"optional"`
	Run     *bool `json:"run,omitempty" validate:"optional"`
	Change  *bool `json:"change,omitempty" validate:"optional"`
	KillOff *bool `json:"killOff,omitempty" validate:"optional"`
} // @name LiveConfirmRequest

// setLiveConfirm sets which of your tokens' and agents' live actions wait for you to
// confirm them (D406), at a browser only.
//
//	@Summary	Which live actions wait for you
//	@ID			setLiveConfirm
//	@Tags		confirmations
//	@Param		body	body	liveConfirmRequest	true	"The switches to change"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not from a browser session"
//	@Security	SessionCookie
//	@Router		/me/live-confirm [put]
func (s *Server) setLiveConfirm(ctx context.Context, c *app.RequestContext) {
	var req liveConfirmRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	// Stored as what goes unconfirmed: a switch on is a value off.
	u := p.User.Unconfirmed
	for _, sw := range []struct {
		to   *bool
		into *bool
	}{{req.GoLive, &u.GoLive}, {req.Run, &u.Run}, {req.Change, &u.Change}, {req.KillOff, &u.KillOff}} {
		if sw.to != nil {
			*sw.into = !*sw.to
		}
	}

	if err := s.confirmations.SetUnconfirmed(ctx, p, u); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
