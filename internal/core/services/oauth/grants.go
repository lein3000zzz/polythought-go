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

// SetAgentsSeePersonal lets the user's agents see their workflows' personal settings, or
// stops them (D401). Only at a browser: an agent cannot let itself see more (D383).
func (s *Service) SetAgentsSeePersonal(ctx context.Context, p auth.Principal, on bool) error {
	if !p.ViaSession() {
		return domain.ErrForbidden
	}

	if err := s.users.SetAgentsSeePersonal(ctx, p.User.ID, on); err != nil {
		return err
	}

	s.log.Info(ctx, "Agents' sight of personal settings set", "user_id", p.User.ID, "on", on)

	return nil
}

// Prune removes ended codes and tokens, and registered clients no grant came of; run it
// periodically.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	now := s.now()

	n, err := s.repo.PruneTokens(ctx, now)
	if err != nil {
		return 0, err
	}

	m, err := s.repo.PruneClients(ctx, now.Add(-unusedFor))

	return n + m, err
}
