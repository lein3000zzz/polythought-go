package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

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

// CreateInvite issues a single-use registration code. The code is shown once.
func (s *Service) CreateInvite(ctx context.Context, actor Principal) (string, domain.Invite, error) {
	if !actor.User.IsAdmin() {
		return "", domain.Invite{}, domain.ErrForbidden
	}

	return s.newInvite(ctx, actor, "")
}

// InviteEmail says whether invites can be mailed now: switched on, and mail set up (D180).
func (s *Service) InviteEmail() bool {
	return s.config().InviteEmail && s.mailSetUp()
}

// InviteByEmail makes an invite that only email signs up with, and mails it, naming the
// admin (D180 to D182). An address with an account is refused (D183); one whose account
// never verified is not, as signing up replaces that. The invite is made even when the
// mail fails: sent says whether it went, and the code is given, to copy.
func (s *Service) InviteByEmail(ctx context.Context, actor Principal, email string) (code string, inv domain.Invite, sent bool, err error) {
	if !actor.User.IsAdmin() {
		return "", domain.Invite{}, false, domain.ErrForbidden
	}

	if !s.InviteEmail() {
		return "", domain.Invite{}, false, domain.ErrInviteEmailOff
	}

	addr, err := validateEmail(email)
	if err != nil {
		return "", domain.Invite{}, false, err
	}

	if u, _, err := s.repos.Users.ByEmail(ctx, normalizeEmail(addr)); err == nil && !u.EmailVerifiedAt.IsZero() {
		return "", domain.Invite{}, false, fmt.Errorf("%w: %s has an account already", domain.ErrEmailTaken, addr)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", domain.Invite{}, false, err
	}

	code, inv, err = s.newInvite(ctx, actor, addr)
	if err != nil {
		return "", domain.Invite{}, false, err
	}

	if err := s.mail.SendInvite(ctx, addr, actor.User.Email, code, inv.ExpiresAt); err != nil {
		s.log.Warn(ctx, "Invite not mailed", "actor_id", actor.User.ID, "invite_id", inv.ID, logging.Err(err))
		return code, inv, false, nil
	}

	s.log.Info(ctx, "Invite mailed", "actor_id", actor.User.ID, "invite_id", inv.ID)

	return code, inv, true, nil
}

// InviteFor is the invite of a code, for the sign-up form to fill in the address it is
// for; domain.ErrInvalidInvite when it is used, expired or none.
func (s *Service) InviteFor(ctx context.Context, code, ip string) (domain.Invite, error) {
	if !s.limiter.Allow("invite:ip:" + ip) {
		return domain.Invite{}, domain.ErrRateLimited
	}

	code = strings.TrimSpace(code)
	if code == "" || len(code) > maxTokenLength {
		return domain.Invite{}, domain.ErrInvalidInvite
	}

	return s.repos.Invites.ByCode(ctx, hashSecret(code), s.now())
}

func (s *Service) newInvite(ctx context.Context, actor Principal, email string) (string, domain.Invite, error) {
	code, err := newSecret(invitePrefix)
	if err != nil {
		return "", domain.Invite{}, err
	}

	now := s.now()
	inv := domain.Invite{
		ID:        domain.InviteID(newID()),
		CreatedBy: actor.User.ID,
		Email:     email,
		CreatedAt: now,
		ExpiresAt: now.Add(s.config().InviteTTL),
	}

	if err := s.repos.Invites.Create(ctx, inv, hashSecret(code)); err != nil {
		return "", domain.Invite{}, err
	}

	s.log.Info(ctx, "Invite created", "actor_id", actor.User.ID, "invite_id", inv.ID, "mailed", email != "")

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
