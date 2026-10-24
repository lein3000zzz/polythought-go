package oauth

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Grants are the user's, newest first, revoked ones too, for Settings (D384).
func (s *Service) Grants(ctx context.Context, p auth.Principal) ([]domain.Grant, error) {
	return s.repo.Grants(ctx, p.User.ID)
}

// RevokeGrant ends one of the user's grants: its tokens stop working at once.
func (s *Service) RevokeGrant(ctx context.Context, p auth.Principal, id domain.GrantID) error {
	if err := s.repo.RevokeGrant(ctx, p.User.ID, id, s.now()); err != nil {
		return err
	}

	s.log.Info(ctx, "OAuth grant revoked", "user_id", p.User.ID, "grant_id", id)

	return nil
}

// SetAgentAccess sets what the user lets their agents and API tokens do beyond their
// scopes (D401, D403). Only at a browser: an agent cannot let itself do more (D383).
func (s *Service) SetAgentAccess(ctx context.Context, p auth.Principal, a domain.AgentAccess) error {
	if !p.ViaSession() {
		return domain.ErrForbidden
	}

	if err := s.users.SetAgentAccess(ctx, p.User.ID, a); err != nil {
		return err
	}

	s.log.Info(ctx, "Agents' access set", "user_id", p.User.ID, "see_personal", a.SeePersonal, "delete", a.Delete, "share", a.Share)

	return nil
}

// Prune removes ended codes and tokens, and the clients no grant came of, registered or
// fetched a day ago (A13); run it periodically.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	now := s.now()

	n, err := s.repo.PruneTokens(ctx, now)
	if err != nil {
		return 0, err
	}

	m, err := s.repo.PruneClients(ctx, now.Add(-unusedFor))

	return n + m, err
}
