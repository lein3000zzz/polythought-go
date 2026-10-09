package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type userDTO struct {
	ID            string    `json:"id"`
	Email         string    `json:"email"`
	Role          string    `json:"role" enums:"admin,user"`
	EmailVerified bool      `json:"emailVerified"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     time.Time `json:"createdAt" format:"date-time"`
} // @name User

func toUserDTO(u domain.User) userDTO {
	return userDTO{
		ID:            string(u.ID),
		Email:         u.Email,
		Role:          string(u.Role),
		EmailVerified: !u.EmailVerifiedAt.IsZero(),
		Disabled:      u.Disabled(),
		CreatedAt:     u.CreatedAt,
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

type inviteDTO struct {
	ID        string     `json:"id"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt" format:"date-time"`
	ExpiresAt time.Time  `json:"expiresAt" format:"date-time"`
	UsedBy    string     `json:"usedBy,omitempty" validate:"optional"`
	UsedAt    *time.Time `json:"usedAt,omitempty" validate:"optional" format:"date-time"`
	// Code is the secret, present only in the response that creates it.
	Code string `json:"code,omitempty" validate:"optional"`
} // @name Invite

func toInviteDTO(i domain.Invite) inviteDTO {
	return inviteDTO{
		ID:        string(i.ID),
		CreatedBy: string(i.CreatedBy),
		CreatedAt: i.CreatedAt,
		ExpiresAt: i.ExpiresAt,
		UsedBy:    string(i.UsedBy),
		UsedAt:    optionalTime(i.UsedAt),
	}
}

type inviteList struct {
	Invites []inviteDTO `json:"invites"`
} // @name InviteList

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

type authConfigDTO struct {
	Registration string `json:"registration" enums:"open,invite,closed"`
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
	Role     *string `json:"role" validate:"optional" enums:"admin,user"`
	Disabled *bool   `json:"disabled" validate:"optional"`
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

// authConfig tells the sign-up page whether and how accounts can be created.
//
//	@Summary	Registration settings
//	@ID			getAuthConfig
//	@Tags		auth
//	@Success	200	{object}	authConfigDTO
//	@Router		/auth/config [get]
func (s *Server) authConfig(_ context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, authConfigDTO{Registration: string(s.auth.RegistrationMode())})
}

// register creates an account when the registration mode allows it.
//
//	@Summary	Create an account
//	@ID			register
//	@Tags		auth
//	@Param		body	body		registerRequest	true	"Account"
//	@Success	201		{object}	userDTO
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

	user, err := s.auth.Register(ctx, auth.RegisterInput{
		Email: req.Email, Password: req.Password, InviteCode: req.InviteCode, IP: c.ClientIP(),
	})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, toUserDTO(user))
}

// login starts a browser session.
//
//	@Summary	Sign in
//	@ID			login
//	@Tags		auth
//	@Param		body	body		loginRequest	true	"Credentials"
//	@Success	200		{object}	userDTO
//	@Header		200		{string}	Set-Cookie	"The polythought_session cookie"
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"invalid_credentials"
//	@Failure	403		{object}	errorBody	"user_disabled"
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
	c.JSON(http.StatusOK, toUserDTO(res.User))
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
	c.JSON(http.StatusOK, toUserDTO(p.User))
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
		out.Users[i] = toUserDTO(u)
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

	c.JSON(http.StatusCreated, toUserDTO(user))
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

	upd := auth.UserUpdate{Disabled: req.Disabled}
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

	c.JSON(http.StatusOK, toUserDTO(user))
}

// listInvites is admin only.
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

	out := inviteList{Invites: make([]inviteDTO, len(invites))}
	for i, inv := range invites {
		out.Invites[i] = toInviteDTO(inv)
	}

	c.JSON(http.StatusOK, out)
}

// createInvite is admin only and returns the invite code once.
//
//	@Summary	Create an invite
//	@ID			createInvite
//	@Tags		admin
//	@Success	201	{object}	inviteDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/admin/invites [post]
func (s *Server) createInvite(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	code, inv, err := s.auth.CreateInvite(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := toInviteDTO(inv)
	out.Code = code

	c.JSON(http.StatusCreated, out)
}
