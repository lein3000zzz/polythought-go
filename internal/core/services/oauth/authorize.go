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
	// Allowed is set when the user's grant to the client covers what it asks and it comes
	// back to its own site: the page approves with Granted, not asking again. Again is set
	// instead when it comes back elsewhere, as to this machine, where any program could
	// listen: the page asks for a click, the grant's scopes ticked (A15).
	Allowed bool
	Again   bool
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
	if !s.Enabled() {
		return Consent{}, domain.ErrAgentsOff
	}

	client, scopes, err := s.check(ctx, p, req)
	if err != nil {
		return Consent{}, err
	}

	c := Consent{Client: client, Verified: client.Kind == domain.ClientDocument, RedirectURI: req.RedirectURI}

	for _, sc := range domain.Scopes {
		if sc == domain.ScopeAdmin && !p.User.IsAdmin() {
			continue
		}

		// Live trading, admin, settings and tasks start unticked, asked for or not (D384,
		// D399, D412).
		ticked := !slices.Contains([]domain.Scope{domain.ScopeLive, domain.ScopeAdmin, domain.ScopeSettings, domain.ScopeTasks}, sc) &&
			(len(scopes) == 0 || slices.Contains(scopes, sc))
		c.Offered = append(c.Offered, Offer{Scope: sc, Ticked: ticked})
	}

	if !s.policy().SkipConsent {
		return c, nil // the page asks every time
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

	if c.Allowed && !ownSite(client, req.RedirectURI) {
		c.Allowed, c.Again = false, true

		for i, o := range c.Offered {
			c.Offered[i].Ticked = slices.Contains(c.Granted, o.Scope)
		}
	}

	return c, nil
}

// ownSite says a redirect goes back to the client's own site: https, on the host its
// document is published at. A registered client's is its own word, and anything on this
// machine may listen at a loopback one (A15, RFC 8252 8.6).
func ownSite(c domain.OAuthClient, redirect string) bool {
	if c.Kind != domain.ClientDocument {
		return false
	}

	id, err := url.Parse(c.ID)
	r, rerr := url.Parse(redirect)

	return err == nil && rerr == nil && r.Scheme == "https" && strings.EqualFold(r.Host, id.Host)
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

	// A grant that allows the request already stays as it is, the click asked again for it
	// included: only more than it has, ticked on the page, replaces a grant's scopes, never
	// a request that asks for fewer.
	grant := consent.grant
	if !consent.Allowed && !consent.Again || !grant.Covers(scopes) {
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
	if !s.Enabled() {
		return "", domain.ErrAgentsOff
	}

	if _, _, err := s.check(ctx, p, req); err != nil {
		return "", err
	}

	return s.redirect(req, url.Values{"error": {"access_denied"}, "error_description": {"the user did not allow it"}}), nil
}

// check checks p's request, and gives its client and the scopes it asks for.
func (s *Service) check(ctx context.Context, p auth.Principal, req AuthRequest) (domain.OAuthClient, []domain.Scope, error) {
	if req.ClientID == "" {
		return domain.OAuthClient{}, nil, oauthErr("invalid_request", "no client_id")
	}

	client, err := s.client(ctx, p.User.ID, req.ClientID)
	if err != nil {
		return domain.OAuthClient{}, nil, err
	}

	if req.RedirectURI == "" || !matchesRedirect(client, req.RedirectURI) {
		return domain.OAuthClient{}, nil, oauthErr("invalid_request", "the redirect_uri is not one the client registered")
	}

	// One registered before a scheme was refused is refused now.
	if err := allowedRedirect(req.RedirectURI); err != nil {
		return domain.OAuthClient{}, nil, oauthErr("invalid_request", err.Error())
	}

	// From here, errors go back to the client: at once to one the user allowed before,
	// else once they see where (A14).
	back := func(code, description string) error {
		e := &Error{Code: code, Description: description, Redirect: s.redirect(req, url.Values{"error": {code}, "error_description": {description}})}

		g, err := s.repo.GrantOf(ctx, p.User.ID, client.ID)
		e.AskFirst = err != nil || !g.Active()

		return e
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
