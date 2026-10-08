package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Transactor runs fn in one database transaction. Repositories called with the ctx that
// fn receives take part in it. fn returning an error rolls everything back.
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type UserFilter struct {
	Limit  int
	Cursor string // ID of the last user on the previous page
}

// UserRepository returns domain.ErrNotFound for unknown users and domain.ErrEmailTaken
// when an email is registered twice. Emails are matched case-insensitively.
type UserRepository interface {
	Create(ctx context.Context, u domain.User, passwordHash string) error
	ByID(ctx context.Context, id domain.UserID) (domain.User, error)
	// ByEmail also returns the password hash, for login.
	ByEmail(ctx context.Context, email string) (domain.User, string, error)
	PasswordHash(ctx context.Context, id domain.UserID) (string, error)
	SetPasswordHash(ctx context.Context, id domain.UserID, hash string, at time.Time) error
	// Update writes Role and DisabledAt.
	Update(ctx context.Context, u domain.User) error
	List(ctx context.Context, f UserFilter) (Page[domain.User], error)
	CountActiveAdmins(ctx context.Context) (int, error)
}

// SessionRepository looks sessions up by the SHA-256 of their token.
type SessionRepository interface {
	Create(ctx context.Context, s domain.Session, tokenHash []byte) error
	ByTokenHash(ctx context.Context, tokenHash []byte) (domain.Session, error)
	Touch(ctx context.Context, id domain.SessionID, lastSeen, expiresAt time.Time) error
	Delete(ctx context.Context, id domain.SessionID) error
	// DeleteForUser removes every session of a user except keep, which may be empty.
	DeleteForUser(ctx context.Context, userID domain.UserID, keep domain.SessionID) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

type APITokenRepository interface {
	Create(ctx context.Context, t domain.APIToken, tokenHash []byte) error
	ByTokenHash(ctx context.Context, tokenHash []byte) (domain.APIToken, error)
	ListForUser(ctx context.Context, userID domain.UserID) ([]domain.APIToken, error)
	// Delete removes a token only if it belongs to userID.
	Delete(ctx context.Context, userID domain.UserID, id domain.APITokenID) error
	TouchLastUsed(ctx context.Context, id domain.APITokenID, at time.Time) error
}

type InviteRepository interface {
	Create(ctx context.Context, inv domain.Invite, codeHash []byte) error
	List(ctx context.Context) ([]domain.Invite, error)
	// Consume marks an unused, unexpired invite as used by userID, or returns
	// domain.ErrInvalidInvite. It is atomic: an invite admits exactly one registration.
	Consume(ctx context.Context, codeHash []byte, userID domain.UserID, at time.Time) error
}

// RateLimiter answers whether one more attempt under key is allowed right now.
type RateLimiter interface {
	Allow(key string) bool
}
