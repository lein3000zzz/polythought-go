package oauth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Access is an agent's access token, checked (D398): who it acts for, and what it may do.
type Access struct {
	Principal auth.Principal
	Client    domain.OAuthClient
	ExpiresAt time.Time
}

// Verify checks an access token as it is used: the token, unexpired; its grant, still
// active and for the MCP endpoint; its user, still able to sign in. Its scopes are those
// it was given that the grant still has, so allowing an agent less narrows its tokens at
// once, and revoking it stops them.
func (s *Service) Verify(ctx context.Context, token string) (Access, error) {
	if !s.Enabled() {
		return Access{}, domain.ErrAgentsOff
	}

	if !strings.HasPrefix(token, accessPrefix) {
		return Access{}, domain.ErrUnauthenticated
	}

	t, err := s.repo.Token(ctx, hash(token))
	if errors.Is(err, domain.ErrNotFound) {
		return Access{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return Access{}, err
	}

	now := s.now()
	if t.Kind != domain.AccessToken || !now.Before(t.ExpiresAt) {
		return Access{}, domain.ErrUnauthenticated
	}

	grant, err := s.repo.Grant(ctx, t.GrantID)
	if errors.Is(err, domain.ErrNotFound) {
		return Access{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return Access{}, err
	}

	if !grant.Active() || grant.Resource != s.cfg.Resource {
		return Access{}, domain.ErrUnauthenticated
	}

	user, err := s.users.ByID(ctx, grant.UserID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && user.Disabled()) {
		return Access{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return Access{}, err
	}

	scopes := make([]domain.Scope, 0, len(t.Scopes)) // none left is none, never every scope
	for _, sc := range t.Scopes {
		if grant.Covers([]domain.Scope{sc}) {
			scopes = append(scopes, sc)
		}
	}

	if now.Sub(grant.LastUsedAt) >= touchEach {
		if err := s.repo.TouchGrant(ctx, grant.ID, now); err != nil {
			s.log.Warn(ctx, "OAuth grant's last use not noted", "grant_id", grant.ID, logging.Err(err))
		}
	}

	return Access{
		Principal: auth.Principal{User: user, GrantID: grant.ID, Agent: grant.Client.Name, Scopes: scopes},
		Client:    grant.Client,
		ExpiresAt: t.ExpiresAt,
	}, nil
}
