package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

const maxEmailLength = 254

type RegisterInput struct {
	Email      string
	Password   string
	InviteCode string // required in invite mode
	IP         string
}

// Register creates a regular user, honouring the registration mode. While sign-ups verify
// (D173), the account must verify its address before it signs in, and the link is mailed;
// a failed mail leaves the account, to send again (D175). An account of the address that
// never verified is replaced (D174).
func (s *Service) Register(ctx context.Context, in RegisterInput) (Registration, error) {
	if !s.limiter.Allow("register:ip:" + in.IP) {
		return Registration{}, domain.ErrRateLimited
	}

	mode := s.config().Registration

	switch mode {
	case domain.RegistrationClosed:
		return Registration{}, domain.ErrRegistrationClosed
	case domain.RegistrationInvite:
		if strings.TrimSpace(in.InviteCode) == "" {
			return Registration{}, domain.ErrInvalidInvite
		}
	}

	user, hash, err := s.newUser(in.Email, in.Password, domain.RoleUser)
	if err != nil {
		return Registration{}, err
	}

	mustVerify := s.verifying()
	if !mustVerify {
		user.EmailVerifiedAt = user.CreatedAt
	}

	err = s.repos.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.replaceUnverified(ctx, user.Email); err != nil {
			return err
		}

		if err := s.repos.Users.Create(ctx, user, hash); err != nil {
			return err
		}

		if mode != domain.RegistrationInvite {
			return nil
		}

		return s.repos.Invites.Consume(ctx, hashSecret(strings.TrimSpace(in.InviteCode)), user.ID, user.CreatedAt)
	})
	if err != nil {
		return Registration{}, err
	}

	s.log.Info(ctx, "User registered", "user_id", user.ID, "ip", in.IP, "must_verify", mustVerify)

	out := Registration{User: user, MustVerify: mustVerify}
	if mustVerify {
		out.Sent = s.sendVerification(ctx, user) == nil
	}

	return out, nil
}

// CreateUser lets an admin add an account in any registration mode.
func (s *Service) CreateUser(ctx context.Context, actor Principal, email, password string, role domain.Role) (domain.User, error) {
	if !actor.User.IsAdmin() {
		return domain.User{}, domain.ErrForbidden
	}

	return s.createUser(ctx, email, password, role)
}

// BootstrapAdmin creates an admin without an acting user. It is for operators: the
// `admin create` command and the first-start environment variables.
func (s *Service) BootstrapAdmin(ctx context.Context, email, password string) (domain.User, error) {
	return s.createUser(ctx, email, password, domain.RoleAdmin)
}

// EnsureAdmin creates an admin from email and password unless an active admin exists.
// It reports whether it created one.
func (s *Service) EnsureAdmin(ctx context.Context, email, password string) (bool, error) {
	admins, err := s.repos.Users.CountActiveAdmins(ctx)
	if err != nil {
		return false, err
	}

	if admins > 0 {
		return false, nil
	}

	if _, err := s.BootstrapAdmin(ctx, email, password); err != nil {
		return false, err
	}

	return true, nil
}

func (s *Service) createUser(ctx context.Context, email, password string, role domain.Role) (domain.User, error) {
	if !role.Valid() {
		return domain.User{}, fmt.Errorf("%w: role %q", domain.ErrInvalidInput, role)
	}

	user, hash, err := s.newUser(email, password, role)
	if err != nil {
		return domain.User{}, err
	}

	user.EmailVerifiedAt = user.CreatedAt // an admin's word for it (D167)

	if err := s.repos.Users.Create(ctx, user, hash); err != nil {
		return domain.User{}, err
	}

	s.log.Info(ctx, "User created", "user_id", user.ID, "role", role)

	return user, nil
}

func (s *Service) newUser(email, password string, role domain.Role) (domain.User, string, error) {
	email, err := validateEmail(email)
	if err != nil {
		return domain.User{}, "", err
	}

	if err := s.validatePassword(password); err != nil {
		return domain.User{}, "", err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return domain.User{}, "", err
	}

	now := s.now()

	return domain.User{
		ID:        domain.UserID(newID()),
		Email:     email,
		Role:      role,
		CreatedAt: now,
		UpdatedAt: now,
	}, hash, nil
}

// ChangePassword needs the current password and signs out every other session.
// It is only allowed from a browser session, not with an API token.
func (s *Service) ChangePassword(ctx context.Context, p Principal, current, next string) error {
	if !p.ViaSession() {
		return domain.ErrForbidden
	}

	if !s.limiter.Allow("password:user:" + string(p.User.ID)) {
		return domain.ErrRateLimited
	}

	hash, err := s.repos.Users.PasswordHash(ctx, p.User.ID)
	if err != nil {
		return err
	}

	ok, _, err := s.hasher.Verify(current, hash)
	if err != nil {
		return err
	}

	if !ok {
		return domain.ErrInvalidCredentials
	}

	if err := s.validatePassword(next); err != nil {
		return err
	}

	newHash, err := s.hasher.Hash(next)
	if err != nil {
		return err
	}

	if err := s.repos.Users.SetPasswordHash(ctx, p.User.ID, newHash, s.now()); err != nil {
		return err
	}

	if err := s.repos.Sessions.DeleteForUser(ctx, p.User.ID, p.SessionID); err != nil {
		return err
	}

	s.log.Info(ctx, "Password changed", "user_id", p.User.ID)

	return nil
}

func validateEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)

	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email || len(email) > maxEmailLength {
		return "", domain.ErrInvalidEmail
	}

	return email, nil
}

func (s *Service) validatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	least := s.config().MinPasswordLength

	switch {
	case n < least:
		return fmt.Errorf("%w: at least %d characters", domain.ErrWeakPassword, least)
	case len(password) > maxPasswordLength:
		return fmt.Errorf("%w: at most %d bytes", domain.ErrWeakPassword, maxPasswordLength)
	case strings.TrimSpace(password) == "":
		return fmt.Errorf("%w: blank", domain.ErrWeakPassword)
	}

	return nil
}

// logIgnored records a failure the caller should not see, such as a rehash that could
// not be saved after a successful login.
func (s *Service) logIgnored(ctx context.Context, msg string, err error, args ...any) {
	if err != nil && !errors.Is(err, context.Canceled) {
		s.log.Warn(ctx, msg, append(args, logging.Err(err))...)
	}
}
