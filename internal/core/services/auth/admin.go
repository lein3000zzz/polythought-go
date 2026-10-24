package auth

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const maxTokenName = 64

// CreateAPIToken issues a token for the principal's user, able to do what its scopes
// allow (D398). The returned secret is shown once; only its hash is stored. Tokens can
// only be managed from a browser session.
func (s *Service) CreateAPIToken(ctx context.Context, p Principal, name string, expiresAt time.Time, scopes []domain.Scope) (string, domain.APIToken, error) {
	if !p.ViaSession() {
		return "", domain.APIToken{}, domain.ErrForbidden
	}

	scopes, err := tokenScopes(p.User, scopes)
	if err != nil {
		return "", domain.APIToken{}, err
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
		Scopes:    scopes,
	}

	if err := s.repos.Tokens.Create(ctx, token, hashSecret(secret)); err != nil {
		return "", domain.APIToken{}, err
	}

	s.log.Info(ctx, "API token created", "user_id", p.User.ID, "token_id", token.ID, "scopes", scopes)

	return secret, token, nil
}

// tokenScopes checks a new token's scopes, and gives them in their order, once each: at
// least one, and admin only for an admin.
func tokenScopes(u domain.User, scopes []domain.Scope) ([]domain.Scope, error) {
	if len(scopes) == 0 {
		return nil, fmt.Errorf("%w: a token needs at least one scope", domain.ErrInvalidInput)
	}

	for _, sc := range scopes {
		switch {
		case !sc.Valid():
			return nil, fmt.Errorf("%w: no scope %q", domain.ErrInvalidInput, sc)
		case sc == domain.ScopeAdmin && !u.IsAdmin():
			return nil, fmt.Errorf("%w: only an admin's token may have the admin scope", domain.ErrForbidden)
		}
	}

	out := slices.Clone(scopes)
	slices.SortFunc(out, func(a, b domain.Scope) int { return slices.Index(domain.Scopes, a) - slices.Index(domain.Scopes, b) })

	return slices.Compact(out), nil
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
	Role       *domain.Role
	Disabled   *bool
	CodeChosen *bool // picked to write code, for code access "chosen"
	// EmailVerified true marks the address verified, an admin's word for it (D175); an
	// address cannot be unverified.
	EmailVerified *bool
	// LiveCaps replace the user's own caps on their live buys (D222).
	LiveCaps *domain.LiveCaps
	// AgentCaps replace the user's own caps on their agents' spend a month (D271).
	AgentCaps *domain.AgentCaps
}

// maxLiveCap is the most a cap may be, as the instance's settings have it; for agents,
// maxAgentDollars and maxAgentTokens.
const (
	maxLiveCap      = 10_000_000
	maxAgentDollars = 10_000_000
	maxAgentTokens  = 1_000_000_000_000
)

// UpdateUser changes a user's role, disabled state or code access. Disabling signs the user out
// everywhere. The last active admin cannot be demoted or disabled, so an instance can
// never lock itself out.
func (s *Service) UpdateUser(ctx context.Context, actor Principal, id domain.UserID, upd UserUpdate) (domain.User, error) {
	if !actor.User.IsAdmin() {
		return domain.User{}, domain.ErrForbidden
	}

	if upd.Role != nil && !upd.Role.Valid() {
		return domain.User{}, fmt.Errorf("%w: role %q", domain.ErrInvalidInput, *upd.Role)
	}

	if upd.EmailVerified != nil && !*upd.EmailVerified {
		return domain.User{}, fmt.Errorf("%w: an address cannot be unverified", domain.ErrInvalidInput)
	}

	if upd.LiveCaps != nil {
		for _, c := range []*int{upd.LiveCaps.PerOrder, upd.LiveCaps.PerDay} {
			if c != nil && (*c < 0 || *c > maxLiveCap) {
				return domain.User{}, fmt.Errorf("%w: a cap must be between 0 and %d dollars", domain.ErrInvalidInput, maxLiveCap)
			}
		}
	}

	if err := checkAgentCaps(upd.AgentCaps); err != nil {
		return domain.User{}, err
	}

	// A token's or an agent's change waits for the admin where they say so (D410), checked
	// as any change is first.
	if s.confirm != nil && !actor.Itself() && !actor.Confirmed {
		user, err := s.repos.Users.ByID(ctx, id)
		if err != nil {
			return domain.User{}, err
		}

		if err := s.holdUserUpdate(ctx, actor, user, upd); err != nil {
			return domain.User{}, err
		}
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

		if upd.CodeChosen != nil {
			user.CodeChosen = *upd.CodeChosen
		}

		if upd.LiveCaps != nil {
			user.LiveCaps = *upd.LiveCaps
		}

		if upd.AgentCaps != nil {
			user.AgentCaps = *upd.AgentCaps
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

		if upd.EmailVerified != nil && user.EmailVerifiedAt.IsZero() {
			if err := s.repos.Users.SetEmailVerified(ctx, user.ID, now); err != nil {
				return err
			}

			user.EmailVerifiedAt = now
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

	s.log.Info(ctx, "User updated", "actor_id", actor.User.ID, "user_id", id,
		"role", updated.Role, "disabled", updated.Disabled(), "code_chosen", updated.CodeChosen,
		"max_live_order", capLog(updated.LiveCaps.PerOrder), "max_live_day", capLog(updated.LiveCaps.PerDay),
		"max_agent_dollars", dollarsLog(updated.AgentCaps.Dollars), "max_agent_input", tokensLog(updated.AgentCaps.InputTokens),
		"max_agent_output", tokensLog(updated.AgentCaps.OutputTokens), "max_agent_total", tokensLog(updated.AgentCaps.TotalTokens))

	// After the commit, so a running workflow never outlives a disable that went through.
	// Asking to disable an already disabled user stops them again, which repairs a stop
	// that failed the first time.
	if upd.Disabled != nil && *upd.Disabled && s.workflows != nil {
		if err := s.workflows.StopAll(ctx, id); err != nil {
			return domain.User{}, fmt.Errorf("user disabled, but stopping their workflows failed: %w", err)
		}
	}

	// Their kill switch goes on, cancelling what rests on the book; enabled again, it
	// stays on until they turn it off (D226).
	if upd.Disabled != nil && *upd.Disabled && s.live != nil {
		if err := s.live.StopLive(ctx, id); err != nil {
			return domain.User{}, fmt.Errorf("user disabled, but stopping their live trading failed: %w", err)
		}
	}

	return updated, nil
}

// RegistrationMode tells clients which sign-up form to show.
func (s *Service) RegistrationMode() domain.RegistrationMode {
	return s.config().Registration
}

// MinPasswordLength is the fewest characters a new password may have, for forms to say.
func (s *Service) MinPasswordLength() int {
	return s.config().MinPasswordLength
}

// CodeAccess says who may write code now.
func (s *Service) CodeAccess() domain.CodeAccess {
	return s.config().CodeAccess
}

// capLog is a cap as the log has it: "default" for the instance's.
func capLog(c *int) any {
	if c == nil {
		return "default"
	}

	return *c
}

// checkAgentCaps keeps a user's agent caps to what the instance's settings allow:
// dollars to the cent.
func checkAgentCaps(c *domain.AgentCaps) error {
	if c == nil {
		return nil
	}

	if d := c.Dollars; d != nil && (d.IsNegative() || d.GreaterThan(decimal.NewFromInt(maxAgentDollars)) || !d.Equal(d.Truncate(2))) {
		return fmt.Errorf("%w: a dollar cap must be between 0 and %d, to the cent", domain.ErrInvalidInput, maxAgentDollars)
	}

	for _, n := range []*int64{c.InputTokens, c.OutputTokens, c.TotalTokens} {
		if n != nil && (*n < 0 || *n > maxAgentTokens) {
			return fmt.Errorf("%w: a token cap must be between 0 and %d", domain.ErrInvalidInput, int64(maxAgentTokens))
		}
	}

	return nil
}

func dollarsLog(d *decimal.Decimal) any {
	if d == nil {
		return "default"
	}

	return d.StringFixed(2)
}

func tokensLog(n *int64) any {
	if n == nil {
		return "default"
	}

	return *n
}
