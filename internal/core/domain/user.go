package domain

import (
	"errors"
	"time"
)

type (
	UserID     string
	SessionID  string
	APITokenID string
	InviteID   string
)

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleUser
}

// RegistrationMode decides who may create an account.
type RegistrationMode string

const (
	RegistrationOpen   RegistrationMode = "open"
	RegistrationInvite RegistrationMode = "invite" // an admin-issued invite code is required
	RegistrationClosed RegistrationMode = "closed" // only admins create accounts
)

func (m RegistrationMode) Valid() bool {
	return m == RegistrationOpen || m == RegistrationInvite || m == RegistrationClosed
}

var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrWeakPassword       = errors.New("password does not meet requirements")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserDisabled       = errors.New("account disabled")
	ErrRegistrationClosed = errors.New("registration is closed")
	ErrInvalidInvite      = errors.New("invalid or expired invite")
	ErrUnauthenticated    = errors.New("not authenticated")
	ErrForbidden          = errors.New("forbidden")
	ErrRateLimited        = errors.New("too many attempts")
	ErrLastAdmin          = errors.New("the last admin cannot be demoted or disabled")
	ErrInvalidInput       = errors.New("invalid input")
)

// User is an account. The password hash never leaves the auth service and its repository.
type User struct {
	ID              UserID
	Email           string
	Role            Role
	EmailVerifiedAt time.Time // zero until verified
	DisabledAt      time.Time // zero while enabled
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (u User) IsAdmin() bool  { return u.Role == RoleAdmin }
func (u User) Disabled() bool { return !u.DisabledAt.IsZero() }

// Session is a browser login. Its token is only ever stored hashed.
type Session struct {
	ID                SessionID
	UserID            UserID
	UserAgent         string
	IP                string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	ExpiresAt         time.Time // slides forward with use
	AbsoluteExpiresAt time.Time // hard limit, never extended
}

// Expired reports whether the session is past either deadline at now.
func (s Session) Expired(now time.Time) bool {
	return !now.Before(s.ExpiresAt) || !now.Before(s.AbsoluteExpiresAt)
}

// APIToken grants programmatic access as its user. Its secret is shown once, then only
// Prefix identifies it.
type APIToken struct {
	ID         APITokenID
	UserID     UserID
	Name       string
	Prefix     string
	CreatedAt  time.Time
	ExpiresAt  time.Time // zero means it never expires
	LastUsedAt time.Time
}

func (t APIToken) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

// Invite is a single-use code that admits one registration in invite mode.
type Invite struct {
	ID        InviteID
	CreatedBy UserID
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedBy    UserID
	UsedAt    time.Time
}

func (i Invite) Used() bool { return !i.UsedAt.IsZero() }
