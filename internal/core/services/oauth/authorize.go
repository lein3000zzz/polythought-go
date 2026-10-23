package oauth

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// AuthRequest is an authorization request, as the client sent the browser with it.
type AuthRequest struct {
	ResponseType        string
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	State               string
	Resource            string
}

// Consent is what the consent page shows of a request (D384).
type Consent struct {
	Client domain.OAuthClient
	// Verified says the client is a metadata document at its URL, which only its owner
	// could publish: a registered client's name is its own word.
	Verified    bool
	RedirectURI string
	Offered     []Offer
	// Allowed is set when the user's grant to the client covers what it asks: the page
	// approves with Granted, not asking again.
	Allowed bool
	Granted []domain.Scope

	grant domain.Grant // the grant that allows it, when Allowed
}

// Offer is a scope the page offers, ticked or not to start with.
type Offer struct {
	Scope  domain.Scope
	Ticked bool
}

// Prepare checks a request for the consent page. A request whose client or redirect is
// wrong gets an error to show; one wrong otherwise, an error with where to send the
// browser back to the client with it (RFC 6749 4.1.2.1).
func (s *Service) Prepare(ctx context.Context, p auth.Principal, req AuthRequest) (Consent, error) {
	client, scopes, err := s.check(ctx, req)
	if err != nil {
		return Consent{}, err
	}

	c := Consent{Client: client, Verified: client.Kind == domain.ClientDocument, RedirectURI: req.RedirectURI}

	for _, sc := range domain.Scopes {
		if sc == domain.ScopeAdmin && !p.User.IsAdmin() {
			continue
		}

		// Live trading and admin start unticked, asked for or not.
		ticked := sc != domain.ScopeLive && sc != domain.ScopeAdmin &&
			(len(scopes) == 0 || slices.Contains(scopes, sc))
		c.Offered = append(c.Offered, Offer{Scope: sc, Ticked: ticked})
	}

	if g, err := s.repo.GrantOf(ctx, p.User.ID, client.ID); err == nil && g.Active() && g.Resource == s.cfg.Resource {
		switch {
		case len(scopes) == 0:
			c.Allowed, c.Granted, c.grant = true, g.Scopes, g
		case g.Covers(scopes):
			c.Allowed, c.Granted, c.grant = true, scopes, g
		}
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Consent{}, err
	}

	return c, nil
}

// Approve grants the client the scopes the user ticked, and gives where to send the
// browser: the client's redirect, with a code it exchanges for tokens. Only a person at a
// browser grants access (D383).
func (s *Service) Approve(ctx context.Context, p auth.Principal, req AuthRequest, scopes []domain.Scope) (string, error) {
	if !p.ViaSession() {
		return "", domain.ErrForbidden
	}

	consent, err := s.Prepare(ctx, p, req)
	if err != nil {
		return "", err
	}

	if len(scopes) == 0 {
		return "", oauthErr("invalid_scope", "allow at least one scope, or deny")
	}

	for _, sc := range scopes {
		if !slices.ContainsFunc(consent.Offered, func(o Offer) bool { return o.Scope == sc }) {
			return "", oauthErr("invalid_scope", "scope "+string(sc)+" is not offered")
		}
	}

	slices.SortFunc(scopes, func(a, b domain.Scope) int { return slices.Index(domain.Scopes, a) - slices.Index(domain.Scopes, b) })
	scopes = slices.Compact(scopes)

	now := s.now()

	// A grant that allows the request already stays as it is: only what the user ticked
	// on the page replaces a grant's scopes, never a request that asks for fewer.
	grant := consent.grant
	if !consent.Allowed || !grant.Covers(scopes) {
		grant, err = s.repo.SaveGrant(ctx, domain.Grant{
			ID: domain.GrantID(newID()), UserID: p.User.ID, Client: consent.Client, Scopes: scopes,
			Resource: s.cfg.Resource, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return "", err
		}
	}

	code, err := secret("")
	if err != nil {
		return "", err
	}

	err = s.repo.SaveCode(ctx, hash(code), domain.AuthCode{
		GrantID: grant.ID, RedirectURI: req.RedirectURI, Challenge: req.CodeChallenge, Scopes: scopes,
		Resource: s.cfg.Resource, ExpiresAt: now.Add(codeLasts),
	})
	if err != nil {
		return "", err
	}

	s.log.Info(ctx, "OAuth grant allowed", "user_id", p.User.ID, "client_id", consent.Client.ID, "grant_id", grant.ID,
		"scopes", strings.Join(scopeStrings(scopes), " "))

	return s.redirect(req, url.Values{"code": {code}}), nil
}

// Deny gives where to send the browser when the user says no.
func (s *Service) Deny(ctx context.Context, p auth.Principal, req AuthRequest) (string, error) {
	if _, _, err := s.check(ctx, req); err != nil {
		return "", err
	}

	return s.redirect(req, url.Values{"error": {"access_denied"}, "error_description": {"the user did not allow it"}}), nil
}

// check checks a request, and gives its client and the scopes it asks for.
func (s *Service) check(ctx context.Context, req AuthRequest) (domain.OAuthClient, []domain.Scope, error) {
	if req.ClientID == "" {
		return domain.OAuthClient{}, nil, oauthErr("invalid_request", "no client_id")
	}

	client, err := s.client(ctx, req.ClientID)
	if err != nil {
		return domain.OAuthClient{}, nil, err
	}

	if req.RedirectURI == "" || !matchesRedirect(client, req.RedirectURI) {
		return domain.OAuthClient{}, nil, oauthErr("invalid_request", "the redirect_uri is not one the client registered")
	}

	// From here, errors go back to the client.
	back := func(code, description string) error {
		return &Error{Code: code, Description: description, Redirect: s.redirect(req, url.Values{"error": {code}, "error_description": {description}})}
	}

	switch {
	case req.ResponseType != "code":
		return domain.OAuthClient{}, nil, back("unsupported_response_type", "the response_type is code")
	case req.CodeChallengeMethod != "S256" || !challenge.MatchString(req.CodeChallenge):
		return domain.OAuthClient{}, nil, back("invalid_request", "PKCE with S256 is required")
	case req.Resource != "" && !s.isResource(req.Resource):
		return domain.OAuthClient{}, nil, back("invalid_target", "tokens here are for "+s.cfg.Resource)
	}

	var scopes []domain.Scope

	for _, f := range strings.Fields(req.Scope) {
		sc := domain.Scope(f)
		if !sc.Valid() {
			return domain.OAuthClient{}, nil, back("invalid_scope", "no scope "+f)
		}

		scopes = append(scopes, sc)
	}

	return client, scopes, nil
}

// isResource says a resource indicator names the MCP endpoint, a trailing slash or not.
func (s *Service) isResource(r string) bool {
	return strings.TrimRight(r, "/") == strings.TrimRight(s.cfg.Resource, "/")
}

// redirect is the client's redirect with values added, the request's state and the
// issuer, which clients check (RFC 9207).
func (s *Service) redirect(req AuthRequest, values url.Values) string {
	u, _ := url.Parse(req.RedirectURI) // checked against the client's

	q := u.Query()
	for k, v := range values {
		q[k] = v
	}

	if req.State != "" {
		q.Set("state", req.State)
	}

	q.Set("iss", s.cfg.Issuer)
	u.RawQuery = q.Encode()

	return u.String()
}

func scopeStrings(scopes []domain.Scope) []string {
	out := make([]string, len(scopes))
	for i, sc := range scopes {
		out[i] = string(sc)
	}

	return out
}
