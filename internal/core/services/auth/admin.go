package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const maxTokenName = 64

// CreateAPIToken issues a token for the principal's user. The returned secret is shown
// once; only its hash is stored. Tokens can only be managed from a browser session.
func (s *Service) CreateAPIToken(ctx context.Context, p Principal, name string, expiresAt time.Time) (string, domain.APIToken, error) {
	if !p.ViaSession() {
		return "", domain.APIToken{}, domain.ErrForbidden
	}

	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxTokenName {
		return "", domain.APIToken{}, fmt.Errorf("%w: name must be 1 to %d characters", domain.ErrInvalidInput, maxTokenName)
	}

	now := s.now()
	if !expiresAt.IsZero() && !expiresAt.After(now) {
		return "", domain.APIToken{}, fmt.Errorf("%w: expiry must be in the future", domain.ErrInvalidInput)
	}

	secret, err := newSecret(apiTokenPrefix)
	if err != nil {
		return "", domain.APIToken{}, err
	}

	token := domain.APIToken{
		ID:        domain.APITokenID(newID()),
		UserID:    p.User.ID,
		Name:      name,
		Prefix:    secret[:prefixLength],
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}

	if err := s.repos.Tokens.Create(ctx, token, hashSecret(secret)); err != nil {
		return "", domain.APIToken{}, err
	}

	s.log.Info(ctx, "API token created", "user_id", p.User.ID, "token_id", token.ID)

	return secret, token, nil
}

func (s *Service) ListAPITokens(ctx context.Context, p Principal) ([]domain.APIToken, error) {
	return s.repos.Tokens.ListForUser(ctx, p.User.ID)
}

func (s *Service) RevokeAPIToken(ctx context.Context, p Principal, id domain.APITokenID) error {
	if !p.ViaSession() {
		return domain.ErrForbidden
	}

	if err := s.repos.Tokens.Delete(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.log.Info(ctx, "API token revoked", "user_id", p.User.ID, "token_id", id)

	return nil
}

func (s *Service) ListUsers(ctx context.Context, actor Principal, f ports.PageFilter) (ports.Page[domain.User], error) {
	if !actor.User.IsAdmin() {
		return ports.Page[domain.User]{}, domain.ErrForbidden
	}

	return s.repos.Users.List(ctx, f)
}

// UserUpdate changes only the fields that are set.
type UserUpdate struct {
	Role     *domain.Role
	Disabled *bool
}

// UpdateUser changes a user's role or disabled state. Disabling signs the user out
// everywhere. The last active admin cannot be demoted or disabled, so an instance can
// never lock itself out.
func (s *Service) UpdateUser(ctx context.Context, actor Principal, id domain.UserID, upd UserUpdate) (domain.User, error) {
	if !actor.User.IsAdmin() {
		return domain.User{}, domain.ErrForbidden
	}

	if upd.Role != nil && !upd.Role.Valid() {
		return domain.User{}, fmt.Errorf("%w: role %q", domain.ErrInvalidInput, *upd.Role)
	}

	var updated domain.User

	err := s.repos.Tx.WithinTx(ctx, func(ctx context.Context) error {
		// Lock first, then read the user: concurrent updates are serialised and each one
		// sees the admins as the previous one left them.
		admins, err := s.repos.Users.LockActiveAdmins(ctx)
		if err != nil {
			return err
		}

		user, err := s.repos.Users.ByID(ctx, id)
		if err != nil {
			return err
		}

		wasActiveAdmin := user.IsAdmin() && !user.Disabled()
		now := s.now()

		if upd.Role != nil {
			user.Role = *upd.Role
		}

		if upd.Disabled != nil {
			switch {
			case *upd.Disabled && !user.Disabled():
				user.DisabledAt = now
			case !*upd.Disabled:
				user.DisabledAt = time.Time{}
			}
		}

		if wasActiveAdmin && (!user.IsAdmin() || user.Disabled()) && admins <= 1 {
			return domain.ErrLastAdmin
		}

		user.UpdatedAt = now

		if err := s.repos.Users.Update(ctx, user); err != nil {
			return err
		}

		if user.Disabled() {
			if err := s.repos.Sessions.DeleteForUser(ctx, user.ID, ""); err != nil {
				return err
			}
		}

		updated = user

		return nil
	})
	if err != nil {
		return domain.User{}, err
	}

	s.log.Info(ctx, "User updated", "actor_id", actor.User.ID, "user_id", id, "role", updated.Role, "disabled", updated.Disabled())

	// After the commit, so a running workflow never outlives a disable that went through.
	// Asking to disable an already disabled user stops them again, which repairs a stop
	// that failed the first time.
	if upd.Disabled != nil && *upd.Disabled && s.workflows != nil {
		if err := s.workflows.StopAll(ctx, id); err != nil {
			return domain.User{}, fmt.Errorf("user disabled, but stopping their workflows failed: %w", err)
		}
	}

	return updated, nil
}

// CreateInvite issues a single-use registration code. The code is shown once.
func (s *Service) CreateInvite(ctx context.Context, actor Principal) (string, domain.Invite, error) {
	if !actor.User.IsAdmin() {
		return "", domain.Invite{}, domain.ErrForbidden
	}

	code, err := newSecret(invitePrefix)
	if err != nil {
		return "", domain.Invite{}, err
	}

	now := s.now()
	inv := domain.Invite{
		ID:        domain.InviteID(newID()),
		CreatedBy: actor.User.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(s.cfg.InviteTTL),
	}

	if err := s.repos.Invites.Create(ctx, inv, hashSecret(code)); err != nil {
		return "", domain.Invite{}, err
	}

	s.log.Info(ctx, "Invite created", "actor_id", actor.User.ID, "invite_id", inv.ID)

	return code, inv, nil
}

func (s *Service) ListInvites(ctx context.Context, actor Principal) ([]domain.Invite, error) {
	if !actor.User.IsAdmin() {
		return nil, domain.ErrForbidden
	}

	return s.repos.Invites.List(ctx)
}

// RegistrationMode tells clients which sign-up form to show.
func (s *Service) RegistrationMode() domain.RegistrationMode {
	return s.cfg.Registration
}
