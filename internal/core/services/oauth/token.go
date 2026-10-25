package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// TokenRequest is a request to the token endpoint, as its form has it.
type TokenRequest struct {
	GrantType    string
	ClientID     string
	Code         string
	RedirectURI  string
	CodeVerifier string
	RefreshToken string
	Scope        string
	Resource     string
	IP           string
}

// Tokens are what the token endpoint gives: an access token for an hour and a refresh
// token for 30 days, which works once.
type Tokens struct {
	Access    string
	Refresh   string
	ExpiresIn time.Duration
	Scopes    []domain.Scope
}

// Token exchanges a code for tokens, with its PKCE verifier, or a refresh token for new
// ones. A code or a refresh token used a second time means a copy is about: the grant is
// revoked, and its owner signs the agent in again.
func (s *Service) Token(ctx context.Context, req TokenRequest) (Tokens, error) {
	if !s.Enabled() {
		return Tokens{}, domain.ErrAgentsOff
	}

	if !s.limiter.Allow("oauth-token:ip:" + req.IP) {
		return Tokens{}, domain.ErrRateLimited
	}

	if req.ClientID == "" {
		return Tokens{}, oauthErr("invalid_request", "no client_id")
	}

	if req.Resource != "" && !s.isResource(req.Resource) {
		return Tokens{}, oauthErr("invalid_target", "tokens here are for "+s.cfg.Resource)
	}

	switch req.GrantType {
	case "authorization_code":
		return s.exchange(ctx, req)
	case "refresh_token":
		return s.refresh(ctx, req)
	}

	return Tokens{}, oauthErr("unsupported_grant_type", "the grant types are authorization_code and refresh_token")
}

func (s *Service) exchange(ctx context.Context, req TokenRequest) (Tokens, error) {
	if req.Code == "" || req.RedirectURI == "" || req.CodeVerifier == "" {
		return Tokens{}, oauthErr("invalid_request", "a code, its redirect_uri and the code_verifier are needed")
	}

	now := s.now()

	code, err := s.repo.TakeCode(ctx, hash(req.Code), now)
	if errors.Is(err, domain.ErrNotFound) {
		return Tokens{}, oauthErr("invalid_grant", "no such code")
	}

	if err != nil {
		return Tokens{}, err
	}

	if !code.UsedAt.IsZero() {
		s.revokeCopied(ctx, code.GrantID, "a code used twice")
		return Tokens{}, oauthErr("invalid_grant", "the code was used already")
	}

	grant, err := s.activeGrant(ctx, code.GrantID, req.ClientID)
	if err != nil {
		return Tokens{}, err
	}

	switch {
	case !now.Before(code.ExpiresAt):
		return Tokens{}, oauthErr("invalid_grant", "the code expired")
	case req.RedirectURI != code.RedirectURI:
		return Tokens{}, oauthErr("invalid_grant", "the redirect_uri is not the one the code was given to")
	case !verifier.MatchString(req.CodeVerifier) || !pkce(req.CodeVerifier, code.Challenge):
		return Tokens{}, oauthErr("invalid_grant", "the code_verifier does not match")
	}

	return s.issue(ctx, grant, code.Scopes)
}

func (s *Service) refresh(ctx context.Context, req TokenRequest) (Tokens, error) {
	if req.RefreshToken == "" {
		return Tokens{}, oauthErr("invalid_request", "no refresh_token")
	}

	now := s.now()

	old, err := s.repo.TakeRefresh(ctx, hash(req.RefreshToken), now)
	if errors.Is(err, domain.ErrNotFound) {
		return Tokens{}, oauthErr("invalid_grant", "no such refresh token")
	}

	if err != nil {
		return Tokens{}, err
	}

	if !old.UsedAt.IsZero() {
		s.revokeCopied(ctx, old.GrantID, "a refresh token used twice")
		return Tokens{}, oauthErr("invalid_grant", "the refresh token was used already")
	}

	grant, err := s.activeGrant(ctx, old.GrantID, req.ClientID)
	if err != nil {
		return Tokens{}, err
	}

	if !now.Before(old.ExpiresAt) {
		return Tokens{}, oauthErr("invalid_grant", "the refresh token expired")
	}

	// A refresh may ask for less, never more, than its token had; and no more than the
	// grant has now, if its user allowed less since.
	scopes := old.Scopes

	if req.Scope != "" {
		scopes = nil

		for _, f := range strings.Fields(req.Scope) {
			sc := domain.Scope(f)
			if !containsScope(old.Scopes, sc) {
				return Tokens{}, oauthErr("invalid_scope", "scope "+f+" was not granted")
			}

			scopes = append(scopes, sc)
		}
	}

	var kept []domain.Scope

	for _, sc := range scopes {
		if grant.Covers([]domain.Scope{sc}) {
			kept = append(kept, sc)
		}
	}

	return s.issue(ctx, grant, kept)
}

// activeGrant is the grant a code or token is of, while it is active, its client the one
// asking and its user able to sign in.
func (s *Service) activeGrant(ctx context.Context, id domain.GrantID, client string) (domain.Grant, error) {
	grant, err := s.repo.Grant(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Grant{}, oauthErr("invalid_grant", "the grant is gone")
	}

	if err != nil {
		return domain.Grant{}, err
	}

	if grant.Client.ID != client {
		return domain.Grant{}, oauthErr("invalid_grant", "the grant is another client's")
	}

	if !grant.Active() {
		return domain.Grant{}, oauthErr("invalid_grant", "the grant was revoked")
	}

	user, err := s.users.ByID(ctx, grant.UserID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && user.Disabled()) {
		return domain.Grant{}, oauthErr("invalid_grant", "the account cannot sign in")
	}

	if err != nil {
		return domain.Grant{}, err
	}

	return grant, nil
}

func (s *Service) issue(ctx context.Context, grant domain.Grant, scopes []domain.Scope) (Tokens, error) {
	if len(scopes) == 0 {
		return Tokens{}, oauthErr("invalid_scope", "no scope left to grant")
	}

	access, err := secret(accessPrefix)
	if err != nil {
		return Tokens{}, err
	}

	refresh, err := secret(refreshPrefix)
	if err != nil {
		return Tokens{}, err
	}

	now, policy := s.now(), s.policy()

	for _, t := range []struct {
		secret string
		kind   domain.OAuthTokenKind
		lasts  time.Duration
	}{{access, domain.AccessToken, policy.AccessTTL}, {refresh, domain.RefreshToken, policy.RefreshTTL}} {
		err := s.repo.SaveToken(ctx, hash(t.secret), domain.OAuthToken{
			GrantID: grant.ID, Kind: t.kind, Scopes: scopes, CreatedAt: now, ExpiresAt: now.Add(t.lasts),
		})
		if err != nil {
			return Tokens{}, err
		}
	}

	if err := s.repo.TouchGrant(ctx, grant.ID, now); err != nil {
		s.log.Warn(ctx, "OAuth grant's last use not noted", "grant_id", grant.ID, logging.Err(err))
	}

	return Tokens{Access: access, Refresh: refresh, ExpiresIn: policy.AccessTTL, Scopes: scopes}, nil
}

// revokeCopied revokes a grant a code or refresh token of was used twice: one of the two
// was a copy, and which cannot be told.
func (s *Service) revokeCopied(ctx context.Context, id domain.GrantID, why string) {
	s.log.Warn(ctx, "OAuth grant revoked: "+why, "grant_id", id)

	if err := s.repo.RevokeGrant(ctx, "", id, s.now()); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.log.Warn(ctx, "OAuth grant not revoked", "grant_id", id, logging.Err(err))
		return
	}

	s.ended(ctx, id)
}

// Revoke revokes a token's grant, as the client asks when it signs out (RFC 7009): its
// other tokens too. Whatever the token, the answer is the same.
func (s *Service) Revoke(ctx context.Context, token, client string) error {
	if !s.Enabled() {
		return domain.ErrAgentsOff
	}

	t, err := s.repo.Token(ctx, hash(token))
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	grant, err := s.repo.Grant(ctx, t.GrantID)
	if err != nil || grant.Client.ID != client {
		return nil
	}

	if err := s.repo.RevokeGrant(ctx, "", grant.ID, s.now()); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	s.log.Info(ctx, "OAuth grant revoked by its client", "grant_id", grant.ID, "client_id", client)
	s.ended(ctx, grant.ID)

	return nil
}

// pkce says the verifier answers the S256 challenge, in constant time.
func pkce(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func containsScope(scopes []domain.Scope, sc domain.Scope) bool {
	for _, s := range scopes {
		if s == sc {
			return true
		}
	}

	return false
}
