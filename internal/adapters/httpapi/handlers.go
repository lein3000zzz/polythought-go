package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type userDTO struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Role          string `json:"role" enums:"admin,user"`
	EmailVerified bool   `json:"emailVerified"`
	Disabled      bool   `json:"disabled"`
	// CodeChosen says an admin picked them to write code, which counts while code access
	// is "chosen".
	CodeChosen bool `json:"codeChosen"`
	// MayWriteCode says whether they may write code now, as code access and CodeChosen
	// have it.
	MayWriteCode bool `json:"mayWriteCode"`
	// LiveStopped says their kill switch is on, since LiveStoppedAt: their workflows place
	// no live orders (D220).
	LiveStopped   bool       `json:"liveStopped"`
	LiveStoppedAt *time.Time `json:"liveStoppedAt,omitempty" validate:"optional" format:"date-time"`
	// LiveCaps are an admin's caps for them; those left out are the instance's (D222).
	LiveCaps liveCapsDTO `json:"liveCaps"`
	// AgentCaps are an admin's caps on their agents' spend a month; those left out are
	// the instance's (D271).
	AgentCaps agentCapsDTO `json:"agentCaps"`
	// KillStopsAgents is their choice that their kill switch stops their agents too.
	KillStopsAgents bool      `json:"killStopsAgents"`
	CreatedAt       time.Time `json:"createdAt" format:"date-time"`
} // @name User

// liveCapsDTO caps a user's live buys in whole dollars: one order, and 24 hours. Left out,
// the instance's cap; 0, none.
type liveCapsDTO struct {
	PerOrder *int `json:"perOrder,omitempty" validate:"optional" minimum:"0" maximum:"10000000"`
	PerDay   *int `json:"perDay,omitempty" validate:"optional" minimum:"0" maximum:"10000000"`
} // @name LiveCaps

// agentCapsDTO caps what agents spend a month: dollars to the cent as providers report
// them, as text like "20.00", and input, output and total tokens. Left out, the
// instance's cap; 0, none.
type agentCapsDTO struct {
	Dollars      *string `json:"dollars,omitempty" validate:"optional" example:"20.00"`
	InputTokens  *int64  `json:"inputTokens,omitempty" validate:"optional" minimum:"0"`
	OutputTokens *int64  `json:"outputTokens,omitempty" validate:"optional" minimum:"0"`
	TotalTokens  *int64  `json:"totalTokens,omitempty" validate:"optional" minimum:"0"`
} // @name AgentCaps

func toAgentCapsDTO(c domain.AgentCaps) agentCapsDTO {
	dto := agentCapsDTO{InputTokens: c.InputTokens, OutputTokens: c.OutputTokens, TotalTokens: c.TotalTokens}
	if c.Dollars != nil {
		d := c.Dollars.StringFixed(2)
		dto.Dollars = &d
	}

	return dto
}

func (dto agentCapsDTO) caps() (domain.AgentCaps, error) {
	c := domain.AgentCaps{InputTokens: dto.InputTokens, OutputTokens: dto.OutputTokens, TotalTokens: dto.TotalTokens}

	if dto.Dollars != nil {
		d, err := decimal.NewFromString(strings.TrimSpace(*dto.Dollars))
		if err != nil {
			return c, fmt.Errorf("%w: dollars: %q is not an amount, like 20.00", domain.ErrInvalidInput, *dto.Dollars)
		}

		c.Dollars = &d
	}

	return c, nil
}

func (s *Server) userDTO(u domain.User) userDTO {
	return userDTO{
		ID:              string(u.ID),
		Email:           u.Email,
		Role:            string(u.Role),
		EmailVerified:   !u.EmailVerifiedAt.IsZero(),
		Disabled:        u.Disabled(),
		CodeChosen:      u.CodeChosen,
		MayWriteCode:    s.auth.CodeAccess().Allows(u),
		LiveStopped:     u.LiveStopped(),
		LiveStoppedAt:   optionalTime(u.LiveStoppedAt),
		LiveCaps:        liveCapsDTO{PerOrder: u.LiveCaps.PerOrder, PerDay: u.LiveCaps.PerDay},
		AgentCaps:       toAgentCapsDTO(u.AgentCaps),
		KillStopsAgents: u.KillStopsAgents,
		CreatedAt:       u.CreatedAt,
	}
}

type userPage struct {
	Users []userDTO `json:"users"`
	// NextCursor is absent on the last page.
	NextCursor string `json:"nextCursor,omitempty" validate:"optional"`
} // @name UserPage

type tokenDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt" format:"date-time"`
	ExpiresAt  *time.Time `json:"expiresAt,omitempty" validate:"optional" format:"date-time"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty" validate:"optional" format:"date-time"`
	// Token is the secret, present only in the response that creates it.
	Token string `json:"token,omitempty" validate:"optional"`
} // @name APIToken

func toTokenDTO(t domain.APIToken) tokenDTO {
	return tokenDTO{
		ID:         string(t.ID),
		Name:       t.Name,
		Prefix:     t.Prefix,
		CreatedAt:  t.CreatedAt,
		ExpiresAt:  optionalTime(t.ExpiresAt),
		LastUsedAt: optionalTime(t.LastUsedAt),
	}
}

type tokenList struct {
	Tokens []tokenDTO `json:"tokens"`
} // @name APITokenList

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

type authConfigDTO struct {
	Registration string `json:"registration" enums:"open,invite,closed"`
	// MinPasswordLength is the fewest characters a new password may have.
	MinPasswordLength int `json:"minPasswordLength"`
	// PasswordReset says a forgotten password can be reset by email (D176).
	PasswordReset bool `json:"passwordReset"`
	// Requests says people without an invite can ask for one (D394).
	Requests bool `json:"requests"`
} // @name AuthConfig

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// InviteCode is required when registration is by invite.
	InviteCode string `json:"inviteCode" validate:"optional"`
} // @name RegisterRequest

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
} // @name LoginRequest

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
} // @name ChangePasswordRequest

type createTokenRequest struct {
	Name string `json:"name"`
	// ExpiresAt left out means the token never expires.
	ExpiresAt *time.Time `json:"expiresAt" validate:"optional" format:"date-time"`
} // @name CreateTokenRequest

type createUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Role     string `json:"role" validate:"optional" enums:"admin,user" default:"user"`
} // @name CreateUserRequest

// updateUserRequest changes only the fields it carries.
type updateUserRequest struct {
	Role       *string `json:"role" validate:"optional" enums:"admin,user"`
	Disabled   *bool   `json:"disabled" validate:"optional"`
	CodeChosen *bool   `json:"codeChosen" validate:"optional"`
	// EmailVerified true marks the address verified; it cannot be made false.
	EmailVerified *bool `json:"emailVerified" validate:"optional"`
	// LiveCaps replace the user's caps; a cap left out is the instance's again.
	LiveCaps *liveCapsDTO `json:"liveCaps" validate:"optional"`
	// AgentCaps replace the user's caps on their agents' spend; a cap left out is the
	// instance's again.
	AgentCaps *agentCapsDTO `json:"agentCaps" validate:"optional"`
} // @name UpdateUserRequest

// decodeJSON reads the body strictly: unknown fields are an error, so typos in field
// names fail loudly instead of being ignored. Free-form values (node params) keep their
// numbers as json.Number, so 0.02 is not turned into a float.
func decodeJSON(c *app.RequestContext, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(c.Request.Body()))
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	return nil
}

func (s *Server) health(_ context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// authConfig tells the sign-up page whether and how accounts can be created, and what a
// password needs.
//
//	@Summary	Registration settings
//	@ID			getAuthConfig
//	@Tags		auth
//	@Success	200	{object}	authConfigDTO
//	@Router		/auth/config [get]
func (s *Server) authConfig(_ context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, authConfigDTO{
		Registration:      string(s.auth.RegistrationMode()),
		MinPasswordLength: s.auth.MinPasswordLength(),
		PasswordReset:     s.auth.PasswordReset(),
		Requests:          s.auth.RequestsOpen(),
	})
}

type registrationDTO struct {
	User userDTO `json:"user"`
	// MustVerify says the account signs in only once it has opened the link mailed to it.
	MustVerify bool `json:"mustVerify"`
	// Sent says the link was mailed; when it was not, it can be sent again.
	Sent bool `json:"sent"`
} // @name Registration

// register creates an account when the registration mode allows it. While sign-ups
// verify, the account signs in only once it has opened the link mailed to it (D166).
//
//	@Summary	Create an account
//	@ID			register
//	@Tags		auth
//	@Param		body	body		registerRequest	true	"Account"
//	@Success	201		{object}	registrationDTO
//	@Failure	400		{object}	errorBody	"invalid_input, invalid_email, weak_password or invalid_invite"
//	@Failure	403		{object}	errorBody	"registration_closed"
//	@Failure	409		{object}	errorBody	"email_taken"
//	@Failure	429		{object}	errorBody	"rate_limited"
//	@Router		/auth/register [post]
func (s *Server) register(ctx context.Context, c *app.RequestContext) {
	var req registerRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	reg, err := s.auth.Register(ctx, auth.RegisterInput{
		Email: req.Email, Password: req.Password, InviteCode: req.InviteCode, IP: c.ClientIP(),
	})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, registrationDTO{User: s.userDTO(reg.User), MustVerify: reg.MustVerify, Sent: reg.Sent})
}

type verifyEmailRequest struct {
	// Token is the one in the mailed link.
	Token string `json:"token"`
} // @name VerifyEmailRequest

type verifiedDTO struct {
	// Email is the account's address, now verified, for the sign-in form.
	Email string `json:"email"`
} // @name Verified

// verifyEmail verifies an account's address with the token of the link mailed to it. It
// does not sign in: the password is still needed (D172).
//
//	@Summary	Verify an email address
//	@ID			verifyEmail
//	@Tags		auth
//	@Param		body	body		verifyEmailRequest	true	"The link's token"
//	@Success	200		{object}	verifiedDTO
//	@Failure	400		{object}	errorBody	"invalid_link: used already, or too old"
//	@Failure	429		{object}	errorBody	"rate_limited"
//	@Router		/auth/verify-email [post]
func (s *Server) verifyEmail(ctx context.Context, c *app.RequestContext) {
	var req verifyEmailRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	email, err := s.auth.VerifyEmail(ctx, req.Token, c.ClientIP())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, verifiedDTO{Email: email})
}

type resendVerificationRequest struct {
	Email string `json:"email"`
} // @name ResendVerificationRequest

// resendVerification mails a new verification link to the account of the address, if it
// has one that must still verify. It answers the same either way.
//
//	@Summary	Send the verification link again
//	@ID			resendVerification
//	@Tags		auth
//	@Param		body	body	resendVerificationRequest	true	"The address"
//	@Success	204
//	@Failure	409	{object}	errorBody	"mail_not_set_up"
//	@Failure	429	{object}	errorBody	"rate_limited"
//	@Router		/auth/verification [post]
func (s *Server) resendVerification(ctx context.Context, c *app.RequestContext) {
	var req resendVerificationRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	if err := s.auth.ResendVerification(ctx, req.Email, c.ClientIP()); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

type passwordResetRequest struct {
	Email string `json:"email"`
} // @name PasswordResetRequest

// requestPasswordReset mails a link setting a new password to the account of the address,
// if it has one. It answers the same either way (D176).
//
//	@Summary	Ask for a password reset link
//	@ID			requestPasswordReset
//	@Tags		auth
//	@Param		body	body	passwordResetRequest	true	"The address"
//	@Success	204
//	@Failure	403	{object}	errorBody	"password_reset_off"
//	@Failure	429	{object}	errorBody	"rate_limited"
//	@Router		/auth/password-reset [post]
func (s *Server) requestPasswordReset(ctx context.Context, c *app.RequestContext) {
	var req passwordResetRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	if err := s.auth.RequestPasswordReset(ctx, req.Email, c.ClientIP()); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

type resetPasswordRequest struct {
	// Token is the one in the mailed link.
	Token    string `json:"token"`
	Password string `json:"password"`
} // @name ResetPasswordRequest

// resetPassword sets a new password with the token of the link mailed for it: it verifies
// the address and signs the account out everywhere, but signs nobody in (D177 to D179).
//
//	@Summary	Set a new password with a mailed link
//	@ID			resetPassword
//	@Tags		auth
//	@Param		body	body		resetPasswordRequest	true	"The link's token and the new password"
//	@Success	200		{object}	verifiedDTO
//	@Failure	400		{object}	errorBody	"invalid_link or weak_password"
//	@Failure	403		{object}	errorBody	"password_reset_off"
//	@Failure	429		{object}	errorBody	"rate_limited"
//	@Router		/auth/password-reset/confirm [post]
func (s *Server) resetPassword(ctx context.Context, c *app.RequestContext) {
	var req resetPasswordRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	email, err := s.auth.ResetPassword(ctx, req.Token, req.Password, c.ClientIP())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, verifiedDTO{Email: email})
}

// sendVerificationLink mails a user that must verify a new link, for an admin.
//
//	@Summary	Send a user's verification link again
//	@ID			sendVerificationLink
//	@Tags		admin
//	@Param		id	path	string	true	"User ID"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input: verified already"
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Failure	409	{object}	errorBody	"mail_not_set_up"
//	@Failure	429	{object}	errorBody	"rate_limited: one a minute, five a day"
//	@Failure	502	{object}	errorBody	"mail_failed"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id}/verification [post]
func (s *Server) sendVerificationLink(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.auth.SendVerificationLink(ctx, p, domain.UserID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// login starts a browser session.
//
//	@Summary	Sign in
//	@ID			login
//	@Tags		auth
//	@Param		body	body		loginRequest	true	"Credentials"
//	@Success	200		{object}	userDTO
//	@Header		200		{string}	Set-Cookie	"The polythought_session cookie, __Host-polythought_session over https"
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"invalid_credentials"
//	@Failure	403		{object}	errorBody	"user_disabled, or email_not_verified"
//	@Failure	429		{object}	errorBody	"rate_limited"
//	@Router		/auth/login [post]
func (s *Server) login(ctx context.Context, c *app.RequestContext) {
	var req loginRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	res, err := s.auth.Login(ctx, auth.LoginInput{
		Email: req.Email, Password: req.Password, IP: c.ClientIP(), UserAgent: string(c.UserAgent()),
	})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	s.setSessionCookie(c, res.Token, res.Session.AbsoluteExpiresAt)
	c.JSON(http.StatusOK, s.userDTO(res.User))
}

// logout ends the current session and clears its cookie.
//
//	@Summary	Sign out
//	@ID			logout
//	@Tags		auth
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/auth/logout [post]
func (s *Server) logout(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if p.ViaSession() {
		if err := s.auth.Logout(ctx, p.SessionID); err != nil {
			s.fail(ctx, c, err)
			return
		}
	}

	s.clearSessionCookie(c)
	c.Status(http.StatusNoContent)
}

// me returns the signed-in user.
//
//	@Summary	Current user
//	@ID			getMe
//	@Tags		me
//	@Success	200	{object}	userDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me [get]
func (s *Server) me(_ context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	c.JSON(http.StatusOK, s.userDTO(p.User))
}

// changePassword ends every other session of the user.
//
//	@Summary	Change password
//	@ID			changePassword
//	@Tags		me
//	@Param		body	body	changePasswordRequest	true	"Current and new password"
//	@Success	204
//	@Failure	400	{object}	errorBody	"invalid_input or weak_password"
//	@Failure	401	{object}	errorBody	"unauthenticated or invalid_credentials"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/password [put]
func (s *Server) changePassword(ctx context.Context, c *app.RequestContext) {
	var req changePasswordRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	if err := s.auth.ChangePassword(ctx, p, req.CurrentPassword, req.NewPassword); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// listTokens lists the user's API tokens, without their secrets.
//
//	@Summary	List API tokens
//	@ID			listTokens
//	@Tags		tokens
//	@Success	200	{object}	tokenList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/tokens [get]
func (s *Server) listTokens(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	tokens, err := s.auth.ListAPITokens(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := tokenList{Tokens: make([]tokenDTO, len(tokens))}
	for i, t := range tokens {
		out.Tokens[i] = toTokenDTO(t)
	}

	c.JSON(http.StatusOK, out)
}

// createToken returns the token's secret once; it cannot be read again.
//
//	@Summary	Create an API token
//	@ID			createToken
//	@Tags		tokens
//	@Param		body	body		createTokenRequest	true	"Token"
//	@Success	201		{object}	tokenDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/tokens [post]
func (s *Server) createToken(ctx context.Context, c *app.RequestContext) {
	var req createTokenRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	var expires time.Time
	if req.ExpiresAt != nil {
		expires = *req.ExpiresAt
	}

	p, _ := principal(c)

	secret, token, err := s.auth.CreateAPIToken(ctx, p, req.Name, expires)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := toTokenDTO(token)
	out.Token = secret

	c.JSON(http.StatusCreated, out)
}

// revokeToken stops a token from working.
//
//	@Summary	Revoke an API token
//	@ID			revokeToken
//	@Tags		tokens
//	@Param		id	path	string	true	"Token ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/tokens/{id} [delete]
func (s *Server) revokeToken(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.auth.RevokeAPIToken(ctx, p, domain.APITokenID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// pageFilter reads ?limit=&cursor= query parameters.
func pageFilter(c *app.RequestContext) (ports.PageFilter, error) {
	f := ports.PageFilter{Cursor: c.Query("cursor")}

	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return ports.PageFilter{}, fmt.Errorf("%w: limit must be a positive integer", domain.ErrInvalidInput)
		}

		f.Limit = n
	}

	return f, nil
}

// listUsers is admin only.
//
//	@Summary	List users
//	@ID			listUsers
//	@Tags		admin
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	userPage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users [get]
func (s *Server) listUsers(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.auth.ListUsers(ctx, p, f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := userPage{Users: make([]userDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, u := range page.Items {
		out.Users[i] = s.userDTO(u)
	}

	c.JSON(http.StatusOK, out)
}

// createUser is admin only and works whatever the registration mode.
//
//	@Summary	Create a user
//	@ID			createUser
//	@Tags		admin
//	@Param		body	body		createUserRequest	true	"User"
//	@Success	201		{object}	userDTO
//	@Failure	400		{object}	errorBody	"invalid_input, invalid_email or weak_password"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Failure	409		{object}	errorBody	"email_taken"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users [post]
func (s *Server) createUser(ctx context.Context, c *app.RequestContext) {
	var req createUserRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	role := domain.Role(req.Role)
	if role == "" {
		role = domain.RoleUser
	}

	p, _ := principal(c)

	user, err := s.auth.CreateUser(ctx, p, req.Email, req.Password, role)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, s.userDTO(user))
}

// updateUser is admin only. The last active admin cannot be demoted or disabled.
//
//	@Summary	Change a user's role or disable them
//	@ID			updateUser
//	@Tags		admin
//	@Param		id		path		string				true	"User ID"
//	@Param		body	body		updateUserRequest	true	"Fields to change"
//	@Success	200		{object}	userDTO
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Failure	409		{object}	errorBody	"last_admin"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/users/{id} [patch]
func (s *Server) updateUser(ctx context.Context, c *app.RequestContext) {
	var req updateUserRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	upd := auth.UserUpdate{Disabled: req.Disabled, CodeChosen: req.CodeChosen, EmailVerified: req.EmailVerified}
	if req.LiveCaps != nil {
		upd.LiveCaps = &domain.LiveCaps{PerOrder: req.LiveCaps.PerOrder, PerDay: req.LiveCaps.PerDay}
	}

	if req.AgentCaps != nil {
		caps, err := req.AgentCaps.caps()
		if err != nil {
			s.fail(ctx, c, err)
			return
		}

		upd.AgentCaps = &caps
	}

	if req.Role != nil {
		role := domain.Role(*req.Role)
		upd.Role = &role
	}

	p, _ := principal(c)

	user, err := s.auth.UpdateUser(ctx, p, domain.UserID(c.Param("id")), upd)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, s.userDTO(user))
}

type inviteForRequest struct {
	// Code is the one in the invite link.
	Code string `json:"code"`
} // @name InviteForRequest

type inviteForDTO struct {
	// Email is the address the invite is for, when it was mailed: the sign-up form fills
	// it in (D181).
	Email string `json:"email,omitempty" validate:"optional"`
} // @name InviteFor

// inviteFor says whether an invite works, and which address it is for, for the sign-up
// form. The code goes in the body, out of URLs and their logs.
//
//	@Summary	Look an invite up
//	@ID			inviteFor
//	@Tags		auth
//	@Param		body	body		inviteForRequest	true	"The invite's code"
//	@Success	200		{object}	inviteForDTO
//	@Failure	400		{object}	errorBody	"invalid_invite: used up, expired, revoked or none"
//	@Failure	429		{object}	errorBody	"rate_limited"
//	@Router		/auth/invite [post]
func (s *Server) inviteFor(ctx context.Context, c *app.RequestContext) {
	var req inviteForRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	inv, err := s.auth.InviteFor(ctx, req.Code, c.ClientIP())
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, inviteForDTO{Email: inv.Email})
}
