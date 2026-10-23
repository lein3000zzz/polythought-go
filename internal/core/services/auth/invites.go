package auth

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// An invite's bounds (D393): a typed code's length, the sign-ups it may admit, how far
// ahead it may end.
const (
	inviteCodeMin = 8
	inviteCodeMax = 64
	inviteUsesMax = 1000
	inviteLifeMax = 365 * 24 * time.Hour
)

var inviteCodeChars = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// InviteSpec is what an admin asks of an invite (D371): its code, typed, or made when
// empty; the sign-ups it admits, 1 when zero; when it ends, Invite lifetime from now
// when zero.
type InviteSpec struct {
	Code      string
	MaxUses   int
	ExpiresAt time.Time
}

// CreateInvite makes an invite an admin hands out as a link or a code (D371, D393).
func (s *Service) CreateInvite(ctx context.Context, actor Principal, spec InviteSpec) (domain.Invite, error) {
	if !actor.User.IsAdmin() {
		return domain.Invite{}, domain.ErrForbidden
	}

	code, err := s.inviteCode(ctx, spec.Code)
	if err != nil {
		return domain.Invite{}, err
	}

	uses := spec.MaxUses
	if uses == 0 {
		uses = 1
	}

	if uses < 1 || uses > inviteUsesMax {
		return domain.Invite{}, fmt.Errorf("%w: an invite admits 1 to %d sign-ups", domain.ErrInvalidInput, inviteUsesMax)
	}

	now := s.now()

	expires := spec.ExpiresAt
	if expires.IsZero() {
		expires = now.Add(s.config().InviteTTL)
	}

	if !expires.After(now) || expires.After(now.Add(inviteLifeMax)) {
		return domain.Invite{}, fmt.Errorf("%w: an invite ends after now and within a year", domain.ErrInvalidInput)
	}

	return s.newInvite(ctx, actor, code, "", uses, expires.UTC())
}

// CheckInviteCode says what keeps a typed code from making an invite, as the admin types
// it: empty when nothing does.
func (s *Service) CheckInviteCode(ctx context.Context, actor Principal, code string) (string, error) {
	if !actor.User.IsAdmin() {
		return "", domain.ErrForbidden
	}

	if _, err := s.inviteCode(ctx, code); err != nil {
		if errors.Is(err, domain.ErrInviteCodeTaken) {
			return "Another invite has this code.", nil
		}

		if problem := codeProblem(strings.TrimSpace(code)); problem != "" {
			return problem, nil
		}

		return "", err
	}

	return "", nil
}

// RevokeInvite ends an invite: it admits no more sign-ups, and stays listed.
func (s *Service) RevokeInvite(ctx context.Context, actor Principal, id domain.InviteID) error {
	if !actor.User.IsAdmin() {
		return domain.ErrForbidden
	}

	if err := s.repos.Invites.Revoke(ctx, id, actor.User.ID, s.now()); err != nil {
		return err
	}

	s.log.Info(ctx, "Invite revoked", "actor_id", actor.User.ID, "invite_id", id)

	return nil
}

// InviteEmail says whether invites can be mailed now: switched on, and mail set up (D180).
func (s *Service) InviteEmail() bool {
	return s.config().InviteEmail && s.mailSetUp()
}

// InviteByEmail makes an invite that only email signs up with, once, and mails it, naming
// the admin (D180 to D182, D371). An address with an account is refused (D183); one whose
// account never verified is not, as signing up replaces that. The invite is made even
// when the mail fails: sent says whether it went, and its code can be copied.
func (s *Service) InviteByEmail(ctx context.Context, actor Principal, email string) (inv domain.Invite, sent bool, err error) {
	if !actor.User.IsAdmin() {
		return domain.Invite{}, false, domain.ErrForbidden
	}

	if !s.InviteEmail() {
		return domain.Invite{}, false, domain.ErrInviteEmailOff
	}

	addr, err := validateEmail(email)
	if err != nil {
		return domain.Invite{}, false, err
	}

	if u, _, err := s.repos.Users.ByEmail(ctx, normalizeEmail(addr)); err == nil && !u.EmailVerifiedAt.IsZero() {
		return domain.Invite{}, false, fmt.Errorf("%w: %s has an account already", domain.ErrEmailTaken, addr)
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Invite{}, false, err
	}

	code, err := s.inviteCode(ctx, "")
	if err != nil {
		return domain.Invite{}, false, err
	}

	now := s.now()

	inv, err = s.newInvite(ctx, actor, code, addr, 1, now.Add(s.config().InviteTTL))
	if err != nil {
		return domain.Invite{}, false, err
	}

	if err := s.mail.SendInvite(ctx, addr, actor.User.Email, code, inv.ExpiresAt); err != nil {
		s.log.Warn(ctx, "Invite not mailed", "actor_id", actor.User.ID, "invite_id", inv.ID, logging.Err(err))
		return inv, false, nil
	}

	s.log.Info(ctx, "Invite mailed", "actor_id", actor.User.ID, "invite_id", inv.ID)

	return inv, true, nil
}

// InviteFor is the invite of a code, for the sign-up form to fill in the address it is
// for; domain.ErrInvalidInvite when it admits no one now.
func (s *Service) InviteFor(ctx context.Context, code, ip string) (domain.Invite, error) {
	if !s.limiter.Allow("invite:ip:" + ip) {
		return domain.Invite{}, domain.ErrRateLimited
	}

	code = strings.TrimSpace(code)
	if code == "" || len(code) > maxTokenLength {
		return domain.Invite{}, domain.ErrInvalidInvite
	}

	return s.repos.Invites.ByCode(ctx, inviteHashes(code), s.now())
}

// ListInvites is every invite, newest first, with its code to copy and its sign-ups.
func (s *Service) ListInvites(ctx context.Context, actor Principal) ([]domain.Invite, error) {
	if !actor.User.IsAdmin() {
		return nil, domain.ErrForbidden
	}

	stored, err := s.repos.Invites.List(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]domain.Invite, len(stored))
	for i, st := range stored {
		out[i] = st.Invite

		if st.Sealed == nil || s.inviteBox == nil {
			continue
		}

		code, err := s.inviteBox.Open(ctx, st.Sealed, inviteAAD(st.ID))
		if err != nil {
			s.log.Warn(ctx, "Invite's code not opened", "invite_id", st.ID, logging.Err(err))
			continue
		}

		out[i].Code = string(code)
	}

	return out, nil
}

// inviteCode is the code an invite is made with: the one typed, unless something keeps
// it from making one, or a new one.
func (s *Service) inviteCode(ctx context.Context, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "" {
		return newSecret(invitePrefix)
	}

	if problem := codeProblem(typed); problem != "" {
		return "", fmt.Errorf("%w: %s", domain.ErrInvalidInput, problem)
	}

	taken, err := s.repos.Invites.Taken(ctx, inviteHashes(typed))
	if err != nil {
		return "", err
	}

	if taken {
		return "", domain.ErrInviteCodeTaken
	}

	return typed, nil
}

// codeProblem says what is wrong with a typed code, empty when nothing is.
func codeProblem(code string) string {
	switch {
	case len(code) < inviteCodeMin || len(code) > inviteCodeMax:
		return fmt.Sprintf("A code has %d to %d characters.", inviteCodeMin, inviteCodeMax)
	case !inviteCodeChars.MatchString(code):
		return "A code has letters, digits, - and _ only."
	}

	return ""
}

// inviteHashes are what a code is found by (D393): any case, so the hash of it in lower
// case; and as given, for the invites made before codes were any case.
func inviteHashes(code string) [][]byte {
	lower := strings.ToLower(code)
	if lower == code {
		return [][]byte{hashSecret(lower)}
	}

	return [][]byte{hashSecret(lower), hashSecret(code)}
}

func (s *Service) newInvite(ctx context.Context, actor Principal, code, email string, uses int, expires time.Time) (domain.Invite, error) {
	now := s.now()
	inv := domain.Invite{
		ID:        domain.InviteID(newID()),
		CreatedBy: actor.User.ID,
		Email:     email,
		CreatedAt: now,
		ExpiresAt: expires,
		MaxUses:   uses,
	}

	// Sealed, the code can be shown again (D393); without the box, only now.
	var sealed []byte

	if s.inviteBox != nil {
		var err error
		if sealed, err = s.inviteBox.Seal(ctx, []byte(code), inviteAAD(inv.ID)); err != nil {
			return domain.Invite{}, err
		}
	}

	if err := s.repos.Invites.Create(ctx, inv, hashSecret(strings.ToLower(code)), sealed); err != nil {
		return domain.Invite{}, err
	}

	s.log.Info(ctx, "Invite created", "actor_id", actor.User.ID, "invite_id", inv.ID, "mailed", email != "", "max_uses", uses)

	inv.Code = code

	return inv, nil
}

// inviteAAD binds a sealed code to its invite.
func inviteAAD(id domain.InviteID) []byte {
	return []byte("invite:" + string(id))
}
