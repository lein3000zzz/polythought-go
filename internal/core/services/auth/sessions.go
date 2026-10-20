package auth

import (
	"context"
	"errors"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

type LoginInput struct {
	Email     string
	Password  string
	IP        string
	UserAgent string
}

// LoginResult carries the session token, which exists only here: it is stored hashed.
type LoginResult struct {
	User    domain.User
	Session domain.Session
	Token   string
}

// Login checks credentials and opens a session. Unknown emails and wrong passwords give
// the same error and take the same time.
func (s *Service) Login(ctx context.Context, in LoginInput) (LoginResult, error) {
	email := normalizeEmail(in.Email)

	// The IP first, so a client it stops charges nothing else, and keeps nothing more in
	// the limiter's memory (R4).
	if !s.limiter.Allow("login:ip:" + in.IP) {
		s.log.Warn(ctx, "Login rate limited", "ip", in.IP)
		return LoginResult{}, domain.ErrRateLimited
	}

	if len(email) > maxEmailLength || len(in.Password) > maxPasswordLength {
		return LoginResult{}, domain.ErrInvalidCredentials // no account has them
	}

	// One address from one IP gets few guesses, and from all IPs together many more: a
	// stranger cannot lock its owner out (R9), nor guess on from many places.
	if !s.limiter.Allow("login:ip-email:"+in.IP+" "+email) ||
		(s.accounts != nil && !s.accounts.Allow("login:email:"+email)) {
		s.log.Warn(ctx, "Login rate limited", "ip", in.IP)
		return LoginResult{}, domain.ErrRateLimited
	}

	user, hash, err := s.repos.Users.ByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		_, _, _ = s.hasher.Verify(in.Password, s.dummyHash)
		s.log.Warn(ctx, "Login failed", "ip", in.IP, "reason", "unknown email")

		return LoginResult{}, domain.ErrInvalidCredentials
	}

	if err != nil {
		return LoginResult{}, err
	}

	ok, needsRehash, err := s.hasher.Verify(in.Password, hash)
	if err != nil {
		return LoginResult{}, err
	}

	if !ok {
		s.log.Warn(ctx, "Login failed", "ip", in.IP, "user_id", user.ID, "reason", "wrong password")
		return LoginResult{}, domain.ErrInvalidCredentials
	}

	// Checked after the password, so a disabled or unverified account is not revealed to
	// guessers.
	if user.Disabled() {
		return LoginResult{}, domain.ErrUserDisabled
	}

	if user.EmailVerifiedAt.IsZero() {
		s.log.Info(ctx, "Login refused: email not verified", "user_id", user.ID)
		return LoginResult{}, domain.ErrEmailNotVerified
	}

	if needsRehash {
		s.rehash(ctx, user.ID, in.Password)
	}

	token, err := newSecret("")
	if err != nil {
		return LoginResult{}, err
	}

	now, cfg := s.now(), s.config()
	session := domain.Session{
		ID:                domain.SessionID(newID()),
		UserID:            user.ID,
		UserAgent:         truncate(in.UserAgent, maxUserAgent),
		IP:                in.IP,
		CreatedAt:         now,
		LastSeenAt:        now,
		ExpiresAt:         now.Add(cfg.SessionIdleTTL),
		AbsoluteExpiresAt: now.Add(cfg.SessionAbsoluteTTL),
	}

	if session.ExpiresAt.After(session.AbsoluteExpiresAt) {
		session.ExpiresAt = session.AbsoluteExpiresAt
	}

	if err := s.repos.Sessions.Create(ctx, session, hashSecret(token)); err != nil {
		return LoginResult{}, err
	}

	s.log.Info(ctx, "User logged in", "user_id", user.ID, "ip", in.IP)

	return LoginResult{User: user, Session: session, Token: token}, nil
}

func (s *Service) rehash(ctx context.Context, id domain.UserID, password string) {
	hash, err := s.hasher.Hash(password)
	if err == nil {
		err = s.repos.Users.SetPasswordHash(ctx, id, hash, s.now())
	}

	s.logIgnored(ctx, "Password rehash failed", err, "user_id", id)
}

// Logout ends a session. Ending an already-gone session is not an error.
func (s *Service) Logout(ctx context.Context, id domain.SessionID) error {
	err := s.repos.Sessions.Delete(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}

	return err
}

// AuthenticateSession resolves a session token and slides its idle expiry forward.
func (s *Service) AuthenticateSession(ctx context.Context, token string) (Principal, error) {
	if token == "" {
		return Principal{}, domain.ErrUnauthenticated
	}

	session, err := s.repos.Sessions.ByTokenHash(ctx, hashSecret(token))
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return Principal{}, err
	}

	now := s.now()

	if session.Expired(now) {
		s.logIgnored(ctx, "Expired session cleanup failed", s.repos.Sessions.Delete(ctx, session.ID), "session_id", session.ID)
		return Principal{}, domain.ErrUnauthenticated
	}

	user, err := s.activeUser(ctx, session.UserID)
	if err != nil {
		return Principal{}, err
	}

	if cfg := s.config(); now.Sub(session.LastSeenAt) >= cfg.TouchInterval {
		expires := now.Add(cfg.SessionIdleTTL)
		if expires.After(session.AbsoluteExpiresAt) {
			expires = session.AbsoluteExpiresAt
		}

		s.logIgnored(ctx, "Session touch failed", s.repos.Sessions.Touch(ctx, session.ID, now, expires), "session_id", session.ID)
	}

	return Principal{User: user, SessionID: session.ID}, nil
}

// AuthenticateToken resolves an API token ("pt_…").
func (s *Service) AuthenticateToken(ctx context.Context, token string) (Principal, error) {
	if !strings.HasPrefix(token, apiTokenPrefix) {
		return Principal{}, domain.ErrUnauthenticated
	}

	t, err := s.repos.Tokens.ByTokenHash(ctx, hashSecret(token))
	if errors.Is(err, domain.ErrNotFound) {
		return Principal{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return Principal{}, err
	}

	now := s.now()

	if t.Expired(now) {
		return Principal{}, domain.ErrUnauthenticated
	}

	user, err := s.activeUser(ctx, t.UserID)
	if err != nil {
		return Principal{}, err
	}

	if now.Sub(t.LastUsedAt) >= s.config().TouchInterval {
		s.logIgnored(ctx, "Token touch failed", s.repos.Tokens.TouchLastUsed(ctx, t.ID, now), "token_id", t.ID)
	}

	return Principal{User: user, TokenID: t.ID}, nil
}

// activeUser loads a user for authentication; a missing or disabled user is just
// unauthenticated, so callers learn nothing more.
func (s *Service) activeUser(ctx context.Context, id domain.UserID) (domain.User, error) {
	user, err := s.repos.Users.ByID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, domain.ErrUnauthenticated
	}

	if err != nil {
		return domain.User{}, err
	}

	if user.Disabled() {
		return domain.User{}, domain.ErrUnauthenticated
	}

	return user, nil
}

// PruneSessions deletes expired sessions; run it periodically.
func (s *Service) PruneSessions(ctx context.Context) (int64, error) {
	return s.repos.Sessions.DeleteExpired(ctx, s.now())
}
