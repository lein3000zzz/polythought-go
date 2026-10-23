package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Email verification (D166, D167, D172 to D175): while it is on and mail is set up, people
// who sign up themselves get a link by mail and sign in once they have opened it. Accounts
// from before, and those admins make, count as verified.

const (
	verifyLinkTTL  = 24 * time.Hour
	unverifiedTTL  = 7 * 24 * time.Hour
	linkGap        = time.Minute // between two links to one account
	linksPerDay    = 5
	maxTokenLength = 128
)

// WithAccountMail mails accounts their links (D165).
func WithAccountMail(m ports.AccountMail) Option {
	return func(s *Service) { s.mail = m }
}

// verifying says whether sign-ups verify their address now: the switch on and mail set up
// (D173).
func (s *Service) verifying() bool {
	return s.config().VerifyEmail && s.mailSetUp()
}

func (s *Service) mailSetUp() bool {
	return s.mail != nil && s.mail.SetUp()
}

// Registration is what signing up did: the account, whether it must verify its address
// before it signs in, and whether the link was mailed.
type Registration struct {
	User       domain.User
	MustVerify bool
	Sent       bool
}

// sendVerification mails u a new link verifying its address.
func (s *Service) sendVerification(ctx context.Context, u domain.User) error {
	token, err := s.newLink(ctx, u, domain.EmailTokenVerify, verifyLinkTTL)
	if err != nil {
		return err
	}

	if err := s.mail.SendVerification(ctx, u.Email, token, verifyLinkTTL); err != nil {
		s.log.Warn(ctx, "Verification link not mailed", "user_id", u.ID, logging.Err(err))
		return err
	}

	s.log.Info(ctx, "Verification link mailed", "user_id", u.ID)

	return nil
}

// newLink makes u a link of purpose working for lasts, and gives its token: at most one a
// minute and five a day of each purpose, domain.ErrRateLimited past that.
func (s *Service) newLink(ctx context.Context, u domain.User, purpose domain.EmailTokenPurpose, lasts time.Duration) (string, error) {
	now := s.now()

	for _, limit := range []struct {
		since time.Time
		most  int
	}{{now.Add(-linkGap), 1}, {now.Add(-24 * time.Hour), linksPerDay}} {
		n, err := s.repos.EmailTokens.CountSince(ctx, u.ID, purpose, limit.since)
		if err != nil {
			return "", err
		}

		if n >= limit.most {
			return "", domain.ErrRateLimited
		}
	}

	token, err := newSecret("")
	if err != nil {
		return "", err
	}

	link := domain.EmailToken{UserID: u.ID, Purpose: purpose, CreatedAt: now, ExpiresAt: now.Add(lasts)}
	if err := s.repos.EmailTokens.Create(ctx, link, hashSecret(token)); err != nil {
		return "", err
	}

	return token, nil
}

// VerifyEmail verifies the address of the account a mailed link is for, and gives the
// address, for the sign-in form (D172). A link works once.
func (s *Service) VerifyEmail(ctx context.Context, token, ip string) (string, error) {
	if !s.limiter.Allow("verify:ip:" + ip) {
		return "", domain.ErrRateLimited
	}

	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLength {
		return "", domain.ErrInvalidLink
	}

	now := s.now()

	id, err := s.repos.EmailTokens.Take(ctx, hashSecret(token), domain.EmailTokenVerify, now)
	if errors.Is(err, domain.ErrNotFound) {
		return "", domain.ErrInvalidLink
	}

	if err != nil {
		return "", err
	}

	if err := s.repos.Users.SetEmailVerified(ctx, id, now); err != nil {
		return "", err
	}

	if err := s.repos.EmailTokens.DeleteForUser(ctx, id, domain.EmailTokenVerify); err != nil {
		return "", err
	}

	u, err := s.repos.Users.ByID(ctx, id)
	if err != nil {
		return "", err
	}

	s.log.Info(ctx, "Email verified", "user_id", id)

	return u.Email, nil
}

// ResendVerification mails a new link to an account of email that must still verify. It
// answers the same whether or not there is one, and as fast, the account looked up and
// mailed after the answer: it tells nobody which addresses have accounts (R17).
func (s *Service) ResendVerification(ctx context.Context, email, ip string) error {
	if !s.limiter.Allow("verify:ip:" + ip) {
		return domain.ErrRateLimited
	}

	if !s.mailSetUp() {
		return domain.ErrMailNotSetUp
	}

	s.background(ctx, func(ctx context.Context) {
		u, _, err := s.repos.Users.ByEmail(ctx, normalizeEmail(email))
		if errors.Is(err, domain.ErrNotFound) {
			return
		}

		if err != nil {
			s.log.Warn(ctx, "Verification link not sent again", logging.Err(err))
			return
		}

		if !u.EmailVerifiedAt.IsZero() || u.Disabled() {
			return
		}

		if err := s.sendVerification(ctx, u); err != nil && !errors.Is(err, domain.ErrRateLimited) {
			s.log.Warn(ctx, "Verification link not sent again", "user_id", u.ID, logging.Err(err))
		}
	})

	return nil
}

// SendVerificationLink mails a user that must verify a new link, for an admin.
func (s *Service) SendVerificationLink(ctx context.Context, actor Principal, id domain.UserID) error {
	if !actor.User.IsAdmin() {
		return domain.ErrForbidden
	}

	if !s.mailSetUp() {
		return domain.ErrMailNotSetUp
	}

	u, err := s.repos.Users.ByID(ctx, id)
	if err != nil {
		return err
	}

	if !u.EmailVerifiedAt.IsZero() {
		return fmt.Errorf("%w: the address is verified already", domain.ErrInvalidInput)
	}

	return s.sendVerification(ctx, u)
}

// replaceUnverified removes the account of email if it never verified, so signing up
// again takes the address back (D174): nobody can hold an address that is not theirs.
func (s *Service) replaceUnverified(ctx context.Context, email string) error {
	old, _, err := s.repos.Users.ByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	if !old.EmailVerifiedAt.IsZero() {
		return nil // Create says the address is taken
	}

	if err := s.repos.Users.DeleteUnverified(ctx, old.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}

	s.log.Info(ctx, "Unverified account replaced", "user_id", old.ID)

	return nil
}

// PruneUnverified removes the accounts that did not verify within 7 days, expired links,
// and requests to join never confirmed (D394); run it periodically.
func (s *Service) PruneUnverified(ctx context.Context) (int64, error) {
	now := s.now()

	n, err := s.repos.Users.PruneUnverified(ctx, now.Add(-unverifiedTTL))
	if err != nil {
		return 0, err
	}

	if _, err := s.repos.EmailTokens.DeleteExpired(ctx, now); err != nil {
		return n, err
	}

	if s.repos.JoinRequests != nil {
		if _, err := s.repos.JoinRequests.PruneUnconfirmed(ctx, now); err != nil {
			return n, err
		}
	}

	return n, nil
}
