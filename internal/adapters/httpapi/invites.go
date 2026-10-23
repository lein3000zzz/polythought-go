package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type inviteDTO struct {
	ID string `json:"id"`
	// Code is the code to copy (D393); absent for an invite made before codes were kept.
	Code      string    `json:"code,omitempty" validate:"optional"`
	CreatedBy string    `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt" format:"date-time"`
	ExpiresAt time.Time `json:"expiresAt" format:"date-time"`
	// MaxUses is how many sign-ups it admits (D371); Used, how many it did.
	MaxUses int `json:"maxUses"`
	Used    int `json:"used"`
	// Signups are who signed up with it, oldest first.
	Signups []inviteUseDTO `json:"signups"`
	// Status is whether it admits sign-ups now, by the server's clock, and why not.
	Status    string     `json:"status" enums:"open,used,expired,revoked"`
	RevokedAt *time.Time `json:"revokedAt,omitempty" validate:"optional" format:"date-time"`
	// Email is the address it was mailed to, which alone signs up with it, once (D181).
	Email string `json:"email,omitempty" validate:"optional"`
	// Sent says the mail went, in the response that mails it; when it did not, the
	// code can still be copied.
	Sent *bool `json:"sent,omitempty" validate:"optional"`
} // @name Invite

type inviteUseDTO struct {
	// UserID and Email are the account's, absent once it is gone.
	UserID string    `json:"userId,omitempty" validate:"optional"`
	Email  string    `json:"email,omitempty" validate:"optional"`
	At     time.Time `json:"at" format:"date-time"`
} // @name InviteUse

func toInviteDTO(i domain.Invite, now time.Time) inviteDTO {
	out := inviteDTO{
		ID:        string(i.ID),
		Code:      i.Code,
		CreatedBy: string(i.CreatedBy),
		CreatedAt: i.CreatedAt,
		ExpiresAt: i.ExpiresAt,
		MaxUses:   i.MaxUses,
		Used:      i.Used,
		Signups:   make([]inviteUseDTO, len(i.Signups)),
		Status:    string(i.Status(now)),
		RevokedAt: optionalTime(i.RevokedAt),
		Email:     i.Email,
	}

	for n, u := range i.Signups {
		out.Signups[n] = inviteUseDTO{UserID: string(u.UserID), Email: u.Email, At: u.At}
	}

	return out
}

type inviteList struct {
	Invites []inviteDTO `json:"invites"`
	// ByEmail says invites can be mailed now: switched on, and mail set up (D180).
	ByEmail bool `json:"byEmail"`
} // @name InviteList

type createInviteRequest struct {
	// Email, when given, mails the invite there, and only that address signs up with it,
	// once (D371): the server makes its code and it lasts Invite lifetime, so it takes no
	// code, uses or end.
	Email string `json:"email" validate:"optional"`
	// Code is typed (D393): 8 to 64 letters, digits, - and _, any case; made when empty.
	Code string `json:"code" validate:"optional"`
	// MaxUses is how many sign-ups it admits, 1 to 1000; 1 when absent.
	MaxUses int `json:"maxUses" validate:"optional"`
	// ExpiresAt is when it ends, within a year; Invite lifetime from now when absent.
	ExpiresAt *time.Time `json:"expiresAt" validate:"optional" format:"date-time"`
} // @name CreateInviteRequest

type checkInviteCodeRequest struct {
	Code string `json:"code"`
} // @name CheckInviteCodeRequest

type checkInviteCodeDTO struct {
	// Problem says what keeps the code from making an invite: its form, or another
	// invite having it; absent when it can.
	Problem string `json:"problem,omitempty" validate:"optional"`
} // @name InviteCodeCheck

// listInvites is admin only: each invite with its code, its uses and who signed up.
//
//	@Summary	List invites
//	@ID			listInvites
//	@Tags		admin
//	@Success	200	{object}	inviteList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/invites [get]
func (s *Server) listInvites(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	invites, err := s.auth.ListInvites(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	now := time.Now()
	out := inviteList{Invites: make([]inviteDTO, len(invites)), ByEmail: s.auth.InviteEmail()}

	for i, inv := range invites {
		out.Invites[i] = toInviteDTO(inv, now)
	}

	c.JSON(http.StatusOK, out)
}

// createInvite is admin only. Its code is typed or made, it admits up to its uses and
// ends when asked (D371). With an address, it mails the invite there, naming the admin,
// and only that address signs up with it, once (D180 to D183); the invite is made even
// when the mail fails.
//
//	@Summary	Create an invite
//	@ID			createInvite
//	@Tags		admin
//	@Param		body	body		createInviteRequest	false	"Its code, uses and end, or where to mail it"
//	@Success	201		{object}	inviteDTO
//	@Failure	400		{object}	errorBody	"invalid_input, or invalid_email"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden, or invite_email_off"
//	@Failure	409		{object}	errorBody	"invite_code_taken, or email_taken: the address has an account already"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/invites [post]
func (s *Server) createInvite(ctx context.Context, c *app.RequestContext) {
	var req createInviteRequest

	if len(bytes.TrimSpace(c.Request.Body())) > 0 {
		if err := decodeJSON(c, &req); err != nil {
			s.fail(ctx, c, err)
			return
		}
	}

	p, _ := principal(c)

	if strings.TrimSpace(req.Email) == "" {
		spec := auth.InviteSpec{Code: req.Code, MaxUses: req.MaxUses}
		if req.ExpiresAt != nil {
			spec.ExpiresAt = *req.ExpiresAt
		}

		inv, err := s.auth.CreateInvite(ctx, p, spec)
		if err != nil {
			s.fail(ctx, c, err)
			return
		}

		c.JSON(http.StatusCreated, toInviteDTO(inv, time.Now()))

		return
	}

	if req.Code != "" || req.MaxUses != 0 || req.ExpiresAt != nil {
		s.fail(ctx, c, fmt.Errorf("%w: a mailed invite is one use, its code and end the server's", domain.ErrInvalidInput))
		return
	}

	inv, sent, err := s.auth.InviteByEmail(ctx, p, req.Email)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := toInviteDTO(inv, time.Now())
	out.Sent = &sent

	c.JSON(http.StatusCreated, out)
}

// checkInviteCode says whether a typed code can make an invite, as the admin types it
// (D371). The code goes in the body, out of URLs and their logs.
//
//	@Summary	Check an invite code
//	@ID			checkInviteCode
//	@Tags		admin
//	@Param		body	body		checkInviteCodeRequest	true	"The code typed"
//	@Success	200		{object}	checkInviteCodeDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/invites/check [post]
func (s *Server) checkInviteCode(ctx context.Context, c *app.RequestContext) {
	var req checkInviteCodeRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	problem, err := s.auth.CheckInviteCode(ctx, p, req.Code)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, checkInviteCodeDTO{Problem: problem})
}

// revokeInvite ends an invite: it admits no more sign-ups, and stays listed.
//
//	@Summary	Revoke an invite
//	@ID			revokeInvite
//	@Tags		admin
//	@Param		id	path	string	true	"Invite ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/invites/{id}/revoke [post]
func (s *Server) revokeInvite(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.auth.RevokeInvite(ctx, p, domain.InviteID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
