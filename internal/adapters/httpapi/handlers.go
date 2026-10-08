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
	Role          string    `json:"role"`
	EmailVerified bool      `json:"emailVerified"`
	Disabled      bool      `json:"disabled"`
	CreatedAt     time.Time `json:"createdAt"`
}

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

type tokenDTO struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	// Token is the secret, present only in the response that creates it.
	Token string `json:"token,omitempty"`
}

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

type inviteDTO struct {
	ID        string     `json:"id"`
	CreatedBy string     `json:"createdBy"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt time.Time  `json:"expiresAt"`
	UsedBy    string     `json:"usedBy,omitempty"`
	UsedAt    *time.Time `json:"usedAt"`
	// Code is the secret, present only in the response that creates it.
	Code string `json:"code,omitempty"`
}

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

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

// decodeJSON reads the body strictly: unknown fields are an error, so typos in field
// names fail loudly instead of being ignored.
func decodeJSON(c *app.RequestContext, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(c.Request.Body()))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, err.Error())
	}

	return nil
}

func (s *Server) health(_ context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) authConfig(_ context.Context, c *app.RequestContext) {
	c.JSON(http.StatusOK, map[string]string{"registration": string(s.auth.RegistrationMode())})
}

func (s *Server) register(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		InviteCode string `json:"inviteCode"`
	}

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

func (s *Server) login(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

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

func (s *Server) me(_ context.Context, c *app.RequestContext) {
	p, _ := principal(c)
	c.JSON(http.StatusOK, toUserDTO(p.User))
}

func (s *Server) changePassword(ctx context.Context, c *app.RequestContext) {
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}

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

func (s *Server) listTokens(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	tokens, err := s.auth.ListAPITokens(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := make([]tokenDTO, len(tokens))
	for i, t := range tokens {
		out[i] = toTokenDTO(t)
	}

	c.JSON(http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) createToken(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Name      string     `json:"name"`
		ExpiresAt *time.Time `json:"expiresAt"`
	}

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

func (s *Server) revokeToken(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.auth.RevokeAPIToken(ctx, p, domain.APITokenID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *Server) listUsers(ctx context.Context, c *app.RequestContext) {
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			s.fail(ctx, c, fmt.Errorf("%w: limit must be a positive integer", domain.ErrInvalidInput))
			return
		}

		limit = n
	}

	p, _ := principal(c)

	page, err := s.auth.ListUsers(ctx, p, ports.UserFilter{Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := make([]userDTO, len(page.Items))
	for i, u := range page.Items {
		out[i] = toUserDTO(u)
	}

	c.JSON(http.StatusOK, map[string]any{"users": out, "nextCursor": page.NextCursor})
}

func (s *Server) createUser(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}

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

func (s *Server) updateUser(ctx context.Context, c *app.RequestContext) {
	var req struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}

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

func (s *Server) listInvites(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	invites, err := s.auth.ListInvites(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := make([]inviteDTO, len(invites))
	for i, inv := range invites {
		out[i] = toInviteDTO(inv)
	}

	c.JSON(http.StatusOK, map[string]any{"invites": out})
}

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
