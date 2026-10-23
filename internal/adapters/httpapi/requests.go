package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

type joinRequestDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	// Note is the requester's, plain text.
	Note        string    `json:"note,omitempty" validate:"optional"`
	CreatedAt   time.Time `json:"createdAt" format:"date-time"`
	ConfirmedAt time.Time `json:"confirmedAt" format:"date-time"`
	// Status is pending until an admin approves or denies it.
	Status    string     `json:"status" enums:"pending,approved,denied"`
	DecidedAt *time.Time `json:"decidedAt,omitempty" validate:"optional" format:"date-time"`
	DecidedBy string     `json:"decidedBy,omitempty" validate:"optional"`
	// InviteID is the invite an approval mailed.
	InviteID string `json:"inviteId,omitempty" validate:"optional"`
} // @name JoinRequest

type joinRequestList struct {
	Requests []joinRequestDTO `json:"requests"`
	// Pending is how many wait for an admin, for the Invites tab's count.
	Pending int `json:"pending"`
} // @name JoinRequestList

type requestToJoinRequest struct {
	Email string `json:"email"`
	// Note says who they are and why they would like in: at most 500 characters.
	Note string `json:"note" validate:"optional"`
} // @name RequestToJoinRequest

type confirmJoinRequest struct {
	// Token is the one in the mailed link.
	Token string `json:"token"`
} // @name ConfirmJoinRequest

func toJoinRequestDTO(r domain.JoinRequest) joinRequestDTO {
	return joinRequestDTO{
		ID: string(r.ID), Email: r.Email, Note: r.Note, CreatedAt: r.CreatedAt, ConfirmedAt: r.ConfirmedAt,
		Status: string(r.Status()), DecidedAt: optionalTime(r.DecidedAt), DecidedBy: string(r.DecidedBy),
		InviteID: string(r.InviteID),
	}
}

// requestToJoin takes a request to join while requests are open (D394), and mails the
// address a link to confirm it. It answers the same whatever comes of it.
//
//	@Summary	Ask to join
//	@ID			requestToJoin
//	@Tags		auth
//	@Param		body	body	requestToJoinRequest	true	"The address and a note"
//	@Success	202
//	@Failure	400	{object}	errorBody	"invalid_email, or invalid_input"
//	@Failure	403	{object}	errorBody	"requests_off"
//	@Failure	429	{object}	errorBody	"rate_limited"
//	@Router		/auth/requests [post]
func (s *Server) requestToJoin(ctx context.Context, c *app.RequestContext) {
	var req requestToJoinRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	if err := s.auth.RequestToJoin(ctx, req.Email, req.Note, c.ClientIP()); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusAccepted)
}

// confirmJoin confirms a request to join by its mailed link; admins see it then.
//
//	@Summary	Confirm a request to join
//	@ID			confirmJoin
//	@Tags		auth
//	@Param		body	body	confirmJoinRequest	true	"The link's token"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_link: used, too old, or none"
//	@Failure	429	{object}	errorBody	"rate_limited"
//	@Router		/auth/requests/confirm [post]
func (s *Server) confirmJoin(ctx context.Context, c *app.RequestContext) {
	var req confirmJoinRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	if err := s.auth.ConfirmJoin(ctx, req.Token, c.ClientIP()); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// listJoinRequests is admin only: the confirmed requests, pending first, then those
// decided in the last 30 days.
//
//	@Summary	List requests to join
//	@ID			listJoinRequests
//	@Tags		admin
//	@Success	200	{object}	joinRequestList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/requests [get]
func (s *Server) listJoinRequests(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	list, err := s.auth.ListJoinRequests(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := joinRequestList{Requests: make([]joinRequestDTO, len(list))}
	for i, r := range list {
		out.Requests[i] = toJoinRequestDTO(r)
		if r.Status() == domain.JoinPending {
			out.Pending++
		}
	}

	c.JSON(http.StatusOK, out)
}

// approveJoin approves a pending request, mailing the address an invite that is its
// alone, once; the invite is made even when the mail fails, sent saying whether it went.
//
//	@Summary	Approve a request to join
//	@ID			approveJoin
//	@Tags		admin
//	@Param		id	path		string	true	"Request ID"
//	@Success	201	{object}	inviteDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found: no request waiting"
//	@Failure	409	{object}	errorBody	"email_taken: the address has an account already"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/requests/{id}/approve [post]
func (s *Server) approveJoin(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	inv, sent, err := s.auth.ApproveJoin(ctx, p, domain.JoinRequestID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := toInviteDTO(inv, time.Now())
	out.Sent = &sent

	c.JSON(http.StatusCreated, out)
}

// denyJoin denies a pending request, mailing the address so while Tell denied requests
// is on.
//
//	@Summary	Deny a request to join
//	@ID			denyJoin
//	@Tags		admin
//	@Param		id	path	string	true	"Request ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found: no request waiting"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/requests/{id}/deny [post]
func (s *Server) denyJoin(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.auth.DenyJoin(ctx, p, domain.JoinRequestID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
