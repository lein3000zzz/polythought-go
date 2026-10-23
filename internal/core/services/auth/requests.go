package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Requests to join (D380, D394): their bounds.
const (
	joinLinkLasts  = 24 * time.Hour      // the confirmation link
	joinMailsMost  = 5                   // links mailed for one request, at most one a minute
	joinDeniedWait = 30 * 24 * time.Hour // before a denied address may ask again
	joinOpenMost   = 200                 // open requests on the instance
	joinNoteMost   = 500                 // characters in a note
	joinListed     = 30 * 24 * time.Hour // how long decided requests stay listed
)

// JoinNotifier tells the admins someone asks to join. It never blocks.
type JoinNotifier interface {
	JoinRequested(email, note string)
}

// WithJoinNotifier tells admins of each confirmed request (D394).
func WithJoinNotifier(n JoinNotifier) Option {
	return func(s *Service) { s.joins = n }
}

// RequestsOpen says whether people can ask to join now (D394): switched on, registration
// by invite, and mail set up, which carries the confirmation and the invite.
func (s *Service) RequestsOpen() bool {
	cfg := s.config()
	return cfg.Requests && cfg.Registration == domain.RegistrationInvite && s.mailSetUp() && s.repos.JoinRequests != nil
}

// RequestToJoin takes a request to join, mailing the address a link to confirm it. It
// answers the same whatever comes of it, so the form tells nobody which addresses have
// accounts or requests (R17); the work is done after the answer.
func (s *Service) RequestToJoin(ctx context.Context, email, note, ip string) error {
	if !s.limiter.Allow("join:ip:" + ip) {
		return domain.ErrRateLimited
	}

	if !s.RequestsOpen() {
		return domain.ErrRequestsOff
	}

	addr, err := validateEmail(email)
	if err != nil {
		return err
	}

	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > joinNoteMost || !utf8.ValidString(note) || strings.ContainsRune(note, 0) {
		return fmt.Errorf("%w: a note has at most %d characters", domain.ErrInvalidInput, joinNoteMost)
	}

	s.background(ctx, func(ctx context.Context) { s.request(ctx, addr, note) })

	return nil
}

func (s *Service) request(ctx context.Context, addr, note string) {
	norm := normalizeEmail(addr)
	now := s.now()

	// An account, a request with the admins, or one denied lately: nothing to do.
	if u, _, err := s.repos.Users.ByEmail(ctx, norm); err == nil && !u.EmailVerifiedAt.IsZero() {
		return
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.log.Warn(ctx, "Join request not taken", logging.Err(err))
		return
	}

	if denied, err := s.repos.JoinRequests.DeniedSince(ctx, norm, now.Add(-joinDeniedWait)); err != nil || denied {
		return
	}

	open, err := s.repos.JoinRequests.Open(ctx, norm)

	switch {
	case err == nil && open.Status() == domain.JoinPending:
		return
	case err == nil:
		// Not confirmed yet: a new link, at most one a minute and five in all.
		if now.Sub(open.MailedAt) < linkGap || open.Mails >= joinMailsMost {
			return
		}

		token, err := newSecret("")
		if err != nil {
			return
		}

		if err := s.repos.JoinRequests.Remail(ctx, open.ID, hashSecret(token), now.Add(joinLinkLasts), now); err != nil {
			s.log.Warn(ctx, "Join request link not renewed", "request_id", open.ID, logging.Err(err))
			return
		}

		s.mailJoinLink(ctx, open.ID, addr, token)
	case errors.Is(err, domain.ErrNotFound):
		n, err := s.repos.JoinRequests.CountOpen(ctx)
		if err != nil || n >= joinOpenMost {
			s.log.Warn(ctx, "Join request dropped: too many open", "open", n)
			return
		}

		token, err := newSecret("")
		if err != nil {
			return
		}

		req := domain.JoinRequest{
			ID: domain.JoinRequestID(newID()), Email: addr, Note: note,
			CreatedAt: now, LinkExpiresAt: now.Add(joinLinkLasts), MailedAt: now, Mails: 1,
		}

		// Two at once for one address: the second loses on the table's index.
		if err := s.repos.JoinRequests.Create(ctx, req, hashSecret(token)); err != nil {
			s.log.Warn(ctx, "Join request not kept", logging.Err(err))
			return
		}

		s.log.Info(ctx, "Join request taken", "request_id", req.ID)
		s.mailJoinLink(ctx, req.ID, addr, token)
	default:
		s.log.Warn(ctx, "Join request not taken", logging.Err(err))
	}
}

func (s *Service) mailJoinLink(ctx context.Context, id domain.JoinRequestID, addr, token string) {
	if err := s.mail.SendJoinConfirm(ctx, addr, token, joinLinkLasts); err != nil {
		s.log.Warn(ctx, "Join request link not mailed", "request_id", id, logging.Err(err))
	}
}

// ConfirmJoin confirms the request of a mailed link, which then goes to the admins.
func (s *Service) ConfirmJoin(ctx context.Context, token, ip string) error {
	if !s.limiter.Allow("join-confirm:ip:" + ip) {
		return domain.ErrRateLimited
	}

	token = strings.TrimSpace(token)
	if token == "" || len(token) > maxTokenLength || s.repos.JoinRequests == nil {
		return domain.ErrInvalidLink
	}

	req, err := s.repos.JoinRequests.Confirm(ctx, hashSecret(token), s.now())
	if err != nil {
		return err
	}

	s.log.Info(ctx, "Join request confirmed", "request_id", req.ID)

	if s.joins != nil {
		s.joins.JoinRequested(req.Email, req.Note)
	}

	return nil
}

// ListJoinRequests is the confirmed requests, pending first, then those decided in the
// last 30 days.
func (s *Service) ListJoinRequests(ctx context.Context, actor Principal) ([]domain.JoinRequest, error) {
	if !actor.User.IsAdmin() {
		return nil, domain.ErrForbidden
	}

	if s.repos.JoinRequests == nil {
		return nil, nil
	}

	return s.repos.JoinRequests.List(ctx, s.now().Add(-joinListed))
}

// ApproveJoin approves a pending request, mailing the address an invite that is its
// alone, once, as an admin's mailed invite is (D181, D394): the invite is made even when
// the mail fails, and sent says whether it went.
func (s *Service) ApproveJoin(ctx context.Context, actor Principal, id domain.JoinRequestID) (domain.Invite, bool, error) {
	req, err := s.pendingJoin(ctx, actor, id)
	if err != nil {
		return domain.Invite{}, false, err
	}

	if u, _, err := s.repos.Users.ByEmail(ctx, normalizeEmail(req.Email)); err == nil && !u.EmailVerifiedAt.IsZero() {
		return domain.Invite{}, false, fmt.Errorf("%w: %s has an account already", domain.ErrEmailTaken, req.Email)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Invite{}, false, err
	}

	code, err := s.inviteCode(ctx, "")
	if err != nil {
		return domain.Invite{}, false, err
	}

	now := s.now()

	var inv domain.Invite

	err = s.repos.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if inv, err = s.newInvite(ctx, actor, code, req.Email, 1, now.Add(s.config().InviteTTL)); err != nil {
			return err
		}

		return s.repos.JoinRequests.Decide(ctx, id, true, actor.User.ID, now, inv.ID)
	})
	if err != nil {
		return domain.Invite{}, false, err
	}

	s.log.Info(ctx, "Join request approved", "actor_id", actor.User.ID, "request_id", id, "invite_id", inv.ID)

	if err := s.mail.SendInvite(ctx, req.Email, actor.User.Email, code, inv.ExpiresAt); err != nil {
		s.log.Warn(ctx, "Invite not mailed", "actor_id", actor.User.ID, "invite_id", inv.ID, logging.Err(err))
		return inv, false, nil
	}

	return inv, true, nil
}

// DenyJoin denies a pending request, mailing the address so while Tell denied requests
// is on (D394). The address may ask again in 30 days.
func (s *Service) DenyJoin(ctx context.Context, actor Principal, id domain.JoinRequestID) error {
	req, err := s.pendingJoin(ctx, actor, id)
	if err != nil {
		return err
	}

	if err := s.repos.JoinRequests.Decide(ctx, id, false, actor.User.ID, s.now(), ""); err != nil {
		return err
	}

	s.log.Info(ctx, "Join request denied", "actor_id", actor.User.ID, "request_id", id)

	if s.config().RequestDeniedMail && s.mailSetUp() {
		s.background(ctx, func(ctx context.Context) {
			if err := s.mail.SendJoinDenied(ctx, req.Email); err != nil {
				s.log.Warn(ctx, "Denial not mailed", "request_id", id, logging.Err(err))
			}
		})
	}

	return nil
}

// pendingJoin is a request still waiting for an admin; domain.ErrNotFound otherwise.
func (s *Service) pendingJoin(ctx context.Context, actor Principal, id domain.JoinRequestID) (domain.JoinRequest, error) {
	if !actor.User.IsAdmin() {
		return domain.JoinRequest{}, domain.ErrForbidden
	}

	if s.repos.JoinRequests == nil {
		return domain.JoinRequest{}, domain.ErrNotFound
	}

	req, err := s.repos.JoinRequests.Get(ctx, id)
	if err != nil {
		return domain.JoinRequest{}, err
	}

	if req.Status() != domain.JoinPending {
		return domain.JoinRequest{}, fmt.Errorf("%w: the request is not waiting for an admin", domain.ErrNotFound)
	}

	return req, nil
}
