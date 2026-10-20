package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Password reset (D176 to D179): a link mailed to the address sets a new password, once,
// within the hour; it verifies the address, and signs the account out everywhere.

const resetLinkTTL = time.Hour

// PasswordReset says whether passwords can be reset by mail now: switched on, and mail set
// up (D176).
func (s *Service) PasswordReset() bool {
	return s.config().PasswordReset && s.mailSetUp()
}

// RequestPasswordReset mails a link setting a new password to the account of email. It
// answers the same whether or not there is one, and as fast, the account looked up and
// mailed after the answer: it tells nobody which addresses have accounts (R17). A
// disabled account is sent nothing.
func (s *Service) RequestPasswordReset(ctx context.Context, email, ip string) error {
	if !s.limiter.Allow("reset:ip:" + ip) {
		return domain.ErrRateLimited
	}

	if !s.PasswordReset() {
		return domain.ErrPasswordResetOff
	}

	s.background(ctx, func(ctx context.Context) {
		if err := s.mailReset(ctx, normalizeEmail(email)); err != nil {
			s.log.Warn(ctx, "Password reset link not mailed", logging.Err(err))
		}
	})

	return nil
}

func (s *Service) mailReset(ctx context.Context, email string) error {
	u, _, err := s.repos.Users.ByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	if u.Disabled() {
		return nil
	}

	token, err := s.newLink(ctx, u, domain.EmailTokenReset, resetLinkTTL)
	if errors.Is(err, domain.ErrRateLimited) {
		return nil
	}

	if err != nil {
		return err
	}

	if err := s.mail.SendPasswordReset(ctx, u.Email, token, resetLinkTTL); err != nil {
		s.log.Warn(ctx, "Password reset link not mailed", "user_id", u.ID, logging.Err(err))
		return nil
	}

	s.log.Info(ctx, "Password reset link mailed", "user_id", u.ID)

	return nil
}

// ResetPassword sets the password of the account a mailed link is for, and gives its
// address for the sign-in form (D177). The link works once; it verifies the address
// (D178) and signs the account out of every session (D179).
func (s *Service) ResetPassword(ctx context.Context, token, password, ip string) (string, error) {
	if !s.limiter.Allow("reset:ip:" + ip) {
		return "", domain.ErrRateLimited
	}

	if !s.PasswordReset() {
		return "", domain.ErrPasswordResetOff
	}

	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLength {
		return "", domain.ErrInvalidLink
	}

	// Before the link is used, so a weak password leaves it working.
	if err := s.validatePassword(password); err != nil {
		return "", err
	}

	hash, err := s.hasher.Hash(password)
	if err != nil {
		return "", err
	}

	now := s.now()

	var user domain.User

	err = s.repos.Tx.WithinTx(ctx, func(ctx context.Context) error {
		id, err := s.repos.EmailTokens.Take(ctx, hashSecret(token), domain.EmailTokenReset, now)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrInvalidLink
		}

		if err != nil {
			return err
		}

		if user, err = s.repos.Users.ByID(ctx, id); err != nil {
			return err
		}

		if user.Disabled() {
			return domain.ErrInvalidLink
		}

		if err := s.repos.Users.SetPasswordHash(ctx, id, hash, now); err != nil {
			return err
		}

		if user.EmailVerifiedAt.IsZero() {
			if err := s.repos.Users.SetEmailVerified(ctx, id, now); err != nil {
				return err
			}
		}

		if err := s.repos.EmailTokens.DeleteForUser(ctx, id, domain.EmailTokenReset); err != nil {
			return err
		}

		return s.repos.Sessions.DeleteForUser(ctx, id, "")
	})
	if err != nil {
		return "", err
	}

	s.log.Info(ctx, "Password reset", "user_id", user.ID, "ip", ip)
	s.tellPasswordChanged(ctx, user, now)

	return user.Email, nil
}

// tellPasswordChanged mails the account that its password changed, while mail is set up.
func (s *Service) tellPasswordChanged(ctx context.Context, u domain.User, at time.Time) {
	if !s.mailSetUp() {
		return
	}

	if err := s.mail.SendPasswordChanged(ctx, u.Email, at); err != nil {
		s.log.Warn(ctx, "Password change not told", "user_id", u.ID, logging.Err(err))
	}
}
