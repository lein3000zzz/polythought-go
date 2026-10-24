package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/adaptor"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/oauth"
)

// OAuthService is the OAuth server agents sign in with (D382, D397).
type OAuthService interface {
	Enabled() bool
	Metadata() oauth.Metadata
	ResourceMetadata() oauth.ResourceMetadata
	Register(ctx context.Context, reg oauth.Registration, ip string) (domain.OAuthClient, error)
	Prepare(ctx context.Context, p auth.Principal, req oauth.AuthRequest) (oauth.Consent, error)
	Approve(ctx context.Context, p auth.Principal, req oauth.AuthRequest, scopes []domain.Scope) (string, error)
	Deny(ctx context.Context, p auth.Principal, req oauth.AuthRequest) (string, error)
	Token(ctx context.Context, req oauth.TokenRequest) (oauth.Tokens, error)
	Revoke(ctx context.Context, token, client string) error
	Grants(ctx context.Context, p auth.Principal) ([]domain.Grant, error)
	RevokeGrant(ctx context.Context, p auth.Principal, id domain.GrantID) error
	SetAgentAccess(ctx context.Context, p auth.Principal, a domain.AgentAccess) error
}

// oauthEndpoints are called by agents' programs, not the app: they take no cookie and say
// who they are themselves, so neither the session nor the origin check applies. /mcp
// checks its own bearer tokens, agents' as well as API tokens.
var oauthEndpoints = map[string]bool{
	"/api/v1/oauth/register":                    true,
	"/api/v1/oauth/token":                       true,
	"/api/v1/oauth/revoke":                      true,
	"/.well-known/oauth-authorization-server":   true,
	"/.well-known/oauth-protected-resource":     true,
	"/.well-known/oauth-protected-resource/mcp": true,
	"/mcp": true,
}

// selfAuthenticating says the request is to one of those.
func selfAuthenticating(c *app.RequestContext) bool {
	return oauthEndpoints[string(c.Path())]
}

// oauthMetadata is the authorization server's metadata (RFC 8414), at the server's root;
// with agents off, it is not there.
func (s *Server) oauthMetadata(ctx context.Context, c *app.RequestContext) {
	if !s.oauth.Enabled() {
		s.fail(ctx, c, domain.ErrAgentsOff)
		return
	}

	c.Header("Cache-Control", "max-age=3600")
	c.JSON(http.StatusOK, s.oauth.Metadata())
}

// resourceMetadata is /mcp's protected resource metadata (RFC 9728): where agents sign in.
func (s *Server) resourceMetadata(ctx context.Context, c *app.RequestContext) {
	if !s.oauth.Enabled() {
		s.fail(ctx, c, domain.ErrAgentsOff)
		return
	}

	c.Header("Cache-Control", "max-age=3600")
	c.JSON(http.StatusOK, s.oauth.ResourceMetadata())
}

// serveMCP hands /mcp to the SDK's server, which streams its answers as it goes.
func (s *Server) serveMCP(ctx context.Context, c *app.RequestContext) {
	if s.mcp == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		return
	}

	adaptor.HertzHandler(s.mcp)(ctx, c)
}

// oauthFail answers as RFC 6749 has the token and registration endpoints answer.
func (s *Server) oauthFail(ctx context.Context, c *app.RequestContext, err error) {
	var oe *oauth.Error

	switch {
	case errors.As(err, &oe):
		status := http.StatusBadRequest
		if oe.Code == "invalid_client" {
			status = http.StatusUnauthorized
		}

		c.JSON(status, map[string]string{"error": oe.Code, "error_description": oe.Description})
	case errors.Is(err, domain.ErrRateLimited):
		c.JSON(http.StatusTooManyRequests, map[string]string{"error": "slow_down", "error_description": "too many attempts"})
	case errors.Is(err, domain.ErrAgentsOff):
		c.JSON(http.StatusNotFound, map[string]string{"error": "agents_off", "error_description": err.Error()})
	default:
		s.log.Error(ctx, "OAuth request failed", "path", string(c.Path()), logging.Err(err))
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "server_error"})
	}
}

// oauthRegistrationRequest is what a client says of itself to register (RFC 7591).
type oauthRegistrationRequest struct {
	ClientName              string   `json:"client_name" validate:"optional"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types" validate:"optional"`
	ResponseTypes           []string `json:"response_types" validate:"optional"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method" validate:"optional"`
	ClientURI               string   `json:"client_uri" validate:"optional"`
} // @name OAuthRegistrationRequest

type oauthRegistrationDTO struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

// registerOAuthClient is dynamic client registration (RFC 7591): every client is public,
// with no secret.
//
//	@Summary	Register an OAuth client
//	@ID			registerOAuthClient
//	@Tags		oauth
//	@Accept		json
//	@Param		body	body		oauthRegistrationRequest	true	"The client, as it describes itself"
//	@Success	201		{object}	oauthRegistrationDTO
//	@Failure	400		{object}	map[string]string	"invalid_client_metadata, invalid_redirect_uri"
//	@Failure	429		{object}	map[string]string	"slow_down"
//	@Router		/oauth/register [post]
func (s *Server) registerOAuthClient(ctx context.Context, c *app.RequestContext) {
	var reg oauthRegistrationRequest

	// Metadata the server does not know is ignored, as RFC 7591 says: clients send more.
	if err := json.Unmarshal(c.Request.Body(), &reg); err != nil {
		s.oauthFail(ctx, c, &oauth.Error{Code: "invalid_client_metadata", Description: "the body is not a client's metadata"})
		return
	}

	client, err := s.oauth.Register(ctx, oauth.Registration{
		ClientName: reg.ClientName, RedirectURIs: reg.RedirectURIs, GrantTypes: reg.GrantTypes,
		ResponseTypes: reg.ResponseTypes, TokenEndpointAuthMethod: reg.TokenEndpointAuthMethod, ClientURI: reg.ClientURI,
	}, c.ClientIP())
	if err != nil {
		s.oauthFail(ctx, c, err)
		return
	}

	c.JSON(http.StatusCreated, oauthRegistrationDTO{
		ClientID: client.ID, ClientIDIssuedAt: client.CreatedAt.Unix(), ClientName: client.Name,
		RedirectURIs: client.RedirectURIs, GrantTypes: []string{"authorization_code", "refresh_token"},
		ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none",
	})
}

type oauthTokenDTO struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// oauthToken is the token endpoint: a code and its PKCE verifier, or a refresh token, for
// an access token of an hour and a new refresh token.
//
//	@Summary	OAuth token endpoint
//	@ID			oauthToken
//	@Tags		oauth
//	@Accept		x-www-form-urlencoded
//	@Param		grant_type		formData	string	true	"authorization_code or refresh_token"
//	@Param		client_id		formData	string	true	"The client"
//	@Param		code			formData	string	false	"The code, for authorization_code"
//	@Param		redirect_uri	formData	string	false	"The code's redirect, for authorization_code"
//	@Param		code_verifier	formData	string	false	"PKCE, for authorization_code"
//	@Param		refresh_token	formData	string	false	"For refresh_token"
//	@Param		scope			formData	string	false	"Less than the token had, for refresh_token"
//	@Param		resource		formData	string	false	"The MCP endpoint's URL (RFC 8707)"
//	@Success	200				{object}	oauthTokenDTO
//	@Failure	400				{object}	map[string]string	"invalid_request, invalid_grant, invalid_scope, unsupported_grant_type, invalid_target"
//	@Failure	429				{object}	map[string]string	"slow_down"
//	@Router		/oauth/token [post]
func (s *Server) oauthToken(ctx context.Context, c *app.RequestContext) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")

	form := func(k string) string { return string(c.PostForm(k)) }

	tokens, err := s.oauth.Token(ctx, oauth.TokenRequest{
		GrantType: form("grant_type"), ClientID: form("client_id"), Code: form("code"),
		RedirectURI: form("redirect_uri"), CodeVerifier: form("code_verifier"),
		RefreshToken: form("refresh_token"), Scope: form("scope"), Resource: form("resource"), IP: c.ClientIP(),
	})
	if err != nil {
		s.oauthFail(ctx, c, err)
		return
	}

	scopes := make([]string, len(tokens.Scopes))
	for i, sc := range tokens.Scopes {
		scopes[i] = string(sc)
	}

	c.JSON(http.StatusOK, oauthTokenDTO{
		AccessToken: tokens.Access, TokenType: "Bearer", ExpiresIn: int(tokens.ExpiresIn / time.Second),
		RefreshToken: tokens.Refresh, Scope: strings.Join(scopes, " "),
	})
}

// oauthRevoke revokes a token's grant (RFC 7009): always 200, whatever the token.
//
//	@Summary	Revoke an OAuth token
//	@ID			oauthRevoke
//	@Tags		oauth
//	@Accept		x-www-form-urlencoded
//	@Param		token		formData	string	true	"An access or refresh token"
//	@Param		client_id	formData	string	true	"The client"
//	@Success	200
//	@Router		/oauth/revoke [post]
func (s *Server) oauthRevoke(ctx context.Context, c *app.RequestContext) {
	if err := s.oauth.Revoke(ctx, string(c.PostForm("token")), string(c.PostForm("client_id"))); err != nil {
		s.oauthFail(ctx, c, err)
		return
	}

	c.Status(http.StatusOK)
}

// authRequestDTO is an authorization request, as the consent page read it from its URL.
type authRequestDTO struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Scope               string `json:"scope" validate:"optional"`
	State               string `json:"state" validate:"optional"`
	Resource            string `json:"resource" validate:"optional"`
} // @name OAuthAuthRequest

func (r authRequestDTO) request() oauth.AuthRequest {
	return oauth.AuthRequest{
		ResponseType: r.ResponseType, ClientID: r.ClientID, RedirectURI: r.RedirectURI, CodeChallenge: r.CodeChallenge,
		CodeChallengeMethod: r.CodeChallengeMethod, Scope: r.Scope, State: r.State, Resource: r.Resource,
	}
}

type oauthClientDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Verified says it is a metadata document at its ID, a URL only its owner could
	// publish at; a registered client's name is its own word.
	Verified bool `json:"verified"`
	// Site is the host it is from: its document's, or its homepage's, unchecked.
	Site string `json:"site,omitempty" validate:"optional"`
} // @name OAuthClient

func toOAuthClientDTO(c domain.OAuthClient) oauthClientDTO {
	out := oauthClientDTO{ID: c.ID, Name: c.Name, Verified: c.Kind == domain.ClientDocument}

	from := c.ClientURI
	if out.Verified {
		from = c.ID
	}

	if u, err := url.Parse(from); err == nil {
		out.Site = u.Hostname()
	}

	return out
}

type offerDTO struct {
	Scope  string `json:"scope" enums:"read,build,tasks,settings,paper,live,admin"`
	Ticked bool   `json:"ticked"`
} // @name OAuthOffer

type oauthProblemDTO struct {
	Code        string `json:"code"`
	Description string `json:"description"`
	// Redirect is where to send the browser with it, back to the client; absent, the page
	// shows it, as the client or its redirect is wrong.
	Redirect string `json:"redirect,omitempty" validate:"optional"`
} // @name OAuthProblem

type consentDTO struct {
	Client *oauthClientDTO `json:"client,omitempty" validate:"optional"`
	// RedirectHost is where the browser goes once the user decides.
	RedirectHost string     `json:"redirectHost,omitempty" validate:"optional"`
	Offered      []offerDTO `json:"offered,omitempty" validate:"optional"`
	// Allowed says the user's grant covers what the client asks: the page approves with
	// Granted, not asking again.
	Allowed bool     `json:"allowed"`
	Granted []string `json:"granted,omitempty" validate:"optional"`
	// Problem is set instead when the request is wrong.
	Problem *oauthProblemDTO `json:"problem,omitempty" validate:"optional"`
} // @name OAuthConsent

type decisionDTO struct {
	// Redirect is where to send the browser.
	Redirect string           `json:"redirect,omitempty" validate:"optional"`
	Problem  *oauthProblemDTO `json:"problem,omitempty" validate:"optional"`
} // @name OAuthDecision

type approveRequest struct {
	Request authRequestDTO `json:"request"`
	Scopes  []string       `json:"scopes"`
} // @name OAuthApproveRequest

// problemOf is an OAuth error as the consent page shows it, or nil for any other.
func problemOf(err error) *oauthProblemDTO {
	var oe *oauth.Error
	if !errors.As(err, &oe) {
		return nil
	}

	return &oauthProblemDTO{Code: oe.Code, Description: oe.Description, Redirect: oe.Redirect}
}

// oauthConsent checks an authorization request for the consent page, signed in.
//
//	@Summary	What a client asks to be allowed
//	@ID			oauthConsent
//	@Tags		oauth
//	@Param		body	body		authRequestDTO	true	"The request, from the page's URL"
//	@Success	200		{object}	consentDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Router		/oauth/authorization [post]
func (s *Server) oauthConsent(ctx context.Context, c *app.RequestContext) {
	var req authRequestDTO

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	consent, err := s.oauth.Prepare(ctx, p, req.request())
	if problem := problemOf(err); problem != nil {
		c.JSON(http.StatusOK, consentDTO{Problem: problem})
		return
	}

	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	client := toOAuthClientDTO(consent.Client)
	out := consentDTO{Client: &client, Allowed: consent.Allowed}

	if u, err := url.Parse(consent.RedirectURI); err == nil {
		out.RedirectHost = u.Scheme + "://" + u.Host
	}

	for _, o := range consent.Offered {
		out.Offered = append(out.Offered, offerDTO{Scope: string(o.Scope), Ticked: o.Ticked})
	}

	for _, sc := range consent.Granted {
		out.Granted = append(out.Granted, string(sc))
	}

	c.JSON(http.StatusOK, out)
}

// oauthApprove allows a client the scopes ticked, from a browser session alone (D383).
//
//	@Summary	Allow a client
//	@ID			oauthApprove
//	@Tags		oauth
//	@Param		body	body		approveRequest	true	"The request and the scopes allowed"
//	@Success	200		{object}	decisionDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	403		{object}	errorBody	"forbidden: not from a browser session"
//	@Security	SessionCookie
//	@Router		/oauth/authorization/approve [post]
func (s *Server) oauthApprove(ctx context.Context, c *app.RequestContext) {
	var req approveRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	scopes := make([]domain.Scope, len(req.Scopes))
	for i, sc := range req.Scopes {
		scopes[i] = domain.Scope(sc)
	}

	redirect, err := s.oauth.Approve(ctx, p, req.Request.request(), scopes)
	s.decided(ctx, c, redirect, err)
}

// oauthDeny tells the client the user said no.
//
//	@Summary	Deny a client
//	@ID			oauthDeny
//	@Tags		oauth
//	@Param		body	body		authRequestDTO	true	"The request"
//	@Success	200		{object}	decisionDTO
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Router		/oauth/authorization/deny [post]
func (s *Server) oauthDeny(ctx context.Context, c *app.RequestContext) {
	var req authRequestDTO

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	redirect, err := s.oauth.Deny(ctx, p, req.request())
	s.decided(ctx, c, redirect, err)
}

func (s *Server) decided(ctx context.Context, c *app.RequestContext, redirect string, err error) {
	if problem := problemOf(err); problem != nil {
		c.JSON(http.StatusOK, decisionDTO{Problem: problem})
		return
	}

	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, decisionDTO{Redirect: redirect})
}

type grantDTO struct {
	ID         string         `json:"id"`
	Client     oauthClientDTO `json:"client"`
	Scopes     []string       `json:"scopes"`
	CreatedAt  time.Time      `json:"createdAt" format:"date-time"`
	LastUsedAt *time.Time     `json:"lastUsedAt,omitempty" validate:"optional" format:"date-time"`
	RevokedAt  *time.Time     `json:"revokedAt,omitempty" validate:"optional" format:"date-time"`
	// TakingTasks says the agent waits for the tasks of your Local agent nodes now, or did
	// a moment ago (D413).
	TakingTasks bool `json:"takingTasks"`
} // @name OAuthGrant

type grantList struct {
	Grants []grantDTO `json:"grants"`
} // @name OAuthGrantList

// listGrants is the agents the user allowed, revoked ones too (D384).
//
//	@Summary	The agents you allowed
//	@ID			listGrants
//	@Tags		oauth
//	@Success	200	{object}	grantList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/grants [get]
func (s *Server) listGrants(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	grants, err := s.oauth.Grants(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	taking := s.takingTasks(p.User.ID)
	out := grantList{Grants: make([]grantDTO, len(grants))}

	for i, g := range grants {
		scopes := make([]string, len(g.Scopes))
		for j, sc := range g.Scopes {
			scopes[j] = string(sc)
		}

		out.Grants[i] = grantDTO{
			ID: string(g.ID), Client: toOAuthClientDTO(g.Client), Scopes: scopes, CreatedAt: g.CreatedAt,
			LastUsedAt: optionalTime(g.LastUsedAt), RevokedAt: optionalTime(g.RevokedAt),
			TakingTasks: taking("grant:" + string(g.ID)),
		}
	}

	c.JSON(http.StatusOK, out)
}

// agentAccessRequest changes the switches it names, and leaves the others as they are.
type agentAccessRequest struct {
	// SeePersonal lets agents and API tokens see the workflows' personal settings, such as
	// webhook URLs.
	SeePersonal *bool `json:"seePersonal,omitempty" validate:"optional"`
	// Delete lets them delete workflows, custom nodes and credentials; Share, share
	// workflows to the library (D403, D404).
	Delete *bool `json:"delete,omitempty" validate:"optional"`
	Share  *bool `json:"share,omitempty" validate:"optional"`
	// UseCredentials lets them save and run workflows that use credentials, test
	// credentials and change where they go (D404).
	UseCredentials *bool `json:"useCredentials,omitempty" validate:"optional"`
	// Answer lets them answer the questions the user's workflows ask (D405).
	Answer *bool `json:"answer,omitempty" validate:"optional"`
} // @name AgentAccessRequest

// setAgentAccess sets what the user's agents and API tokens may see (D401, D402) and do
// (D403), at a browser only.
//
//	@Summary	What your agents may see and do
//	@ID			setAgentAccess
//	@Tags		oauth
//	@Param		body	body	agentAccessRequest	true	"The switches to change"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	403	{object}	errorBody	"forbidden: not from a browser session"
//	@Security	SessionCookie
//	@Router		/me/agent-access [put]
func (s *Server) setAgentAccess(ctx context.Context, c *app.RequestContext) {
	var req agentAccessRequest

	if err := decodeJSON(c, &req); err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	access := p.User.Agents
	for _, sw := range []struct {
		to   *bool
		into *bool
	}{
		{req.SeePersonal, &access.SeePersonal}, {req.Delete, &access.Delete}, {req.Share, &access.Share},
		{req.UseCredentials, &access.UseCredentials}, {req.Answer, &access.Answer},
	} {
		if sw.to != nil {
			*sw.into = *sw.to
		}
	}

	if err := s.oauth.SetAgentAccess(ctx, p, access); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

// revokeGrant ends a grant: the agent's tokens stop working at once.
//
//	@Summary	Revoke an agent's grant
//	@ID			revokeGrant
//	@Tags		oauth
//	@Param		id	path	string	true	"Grant ID"
//	@Success	204
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/me/grants/{id} [delete]
func (s *Server) revokeGrant(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	if err := s.oauth.RevokeGrant(ctx, p, domain.GrantID(c.Param("id"))); err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.Status(http.StatusNoContent)
}
