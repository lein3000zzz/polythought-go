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

// CodeAccess decides who may write code: code nodes and custom nodes, not expressions.
type CodeAccess string

const (
	CodeEveryone CodeAccess = "everyone"
	CodeChosen   CodeAccess = "chosen" // admins, and the users an admin picked
	CodeOff      CodeAccess = "off"    // no one, admins included
)

func (m CodeAccess) Valid() bool {
	return m == CodeEveryone || m == CodeChosen || m == CodeOff
}

// Allows says whether u may write code, and run the workflows that have it.
func (m CodeAccess) Allows(u User) bool {
	switch m {
	case CodeEveryone:
		return true
	case CodeChosen:
		return u.IsAdmin() || u.CodeChosen
	default:
		return false
	}
}

var (
	ErrEmailTaken         = errors.New("email already registered")
	ErrInvalidEmail       = errors.New("invalid email")
	ErrWeakPassword       = errors.New("password does not meet requirements")
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserDisabled       = errors.New("account disabled")
	ErrRegistrationClosed = errors.New("registration is closed")
	ErrInvalidInvite      = errors.New("invalid or expired invite")
	// ErrInviteCodeTaken means another invite has the code, any case (D393).
	ErrInviteCodeTaken = errors.New("another invite has this code")
	ErrUnauthenticated = errors.New("not authenticated")
	ErrForbidden       = errors.New("forbidden")
	ErrRateLimited     = errors.New("too many attempts")
	ErrLastAdmin       = errors.New("the last admin cannot be demoted or disabled")
	ErrInvalidInput    = errors.New("invalid input")
	// ErrEmailNotVerified means the account must verify its address before it signs in
	// (D166).
	ErrEmailNotVerified = errors.New("verify your email first: open the link we mailed you")
	// ErrInvalidLink means a mailed link was used already, or is too old.
	ErrInvalidLink = errors.New("this link does not work any more: it was used already, or it is too old")
	// ErrPasswordResetOff means passwords cannot be reset by mail here: it is switched off,
	// or mail is not set up (D176).
	ErrPasswordResetOff = errors.New("passwords cannot be reset by email here: ask an admin")
	// ErrInviteEmailOff means invites cannot be mailed here: it is switched off, or mail
	// is not set up (D180).
	ErrInviteEmailOff = errors.New("invites cannot be mailed here: inviting by email is off, or mail is not set up")
	// ErrRequestsOff means nobody can ask to join here: requests are off, or registration is
	// not by invite, or mail is not set up (D394).
	ErrRequestsOff = errors.New("requests to join are off here")
	ErrCodeOff     = errors.New("an admin has turned code off for you")
)

// User is an account. The password hash never leaves the auth service and its repository.
type User struct {
	ID              UserID
	Email           string
	Role            Role
	EmailVerifiedAt time.Time // zero until verified
	DisabledAt      time.Time // zero while enabled
	CodeChosen      bool      // an admin picked them to write code, for CodeChosen
	LiveStoppedAt   time.Time // zero unless their kill switch is on (D220)
	LiveCaps        LiveCaps  // an admin's caps for them, over the instance's (D222)
	AgentCaps       AgentCaps // an admin's caps on their agents' spend, over the instance's (D271)
	// KillStopsAgents is their choice that their kill switch stops their agents too
	// (D276).
	KillStopsAgents bool
	// Agents is what they let their agents and API tokens do beyond their scopes.
	Agents AgentAccess
	// Unconfirmed is which of their agents' and tokens' live actions go ahead without them
	// confirming (D406); the rest wait.
	Unconfirmed Unconfirmed
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AgentAccess is what a user lets their agents and API tokens do beyond their scopes, off
// to start with and switched at a browser only: see their workflows' personal settings,
// such as webhook URLs (D401, D402); delete workflows, custom nodes and credentials, and
// share workflows to the library (D403, D404); use credentials in workflows (D404); answer
// their workflows' questions (D405).
type AgentAccess struct {
	SeePersonal    bool
	Delete         bool
	Share          bool
	UseCredentials bool
	Answer         bool
}

func (u User) IsAdmin() bool     { return u.Role == RoleAdmin }
func (u User) Disabled() bool    { return !u.DisabledAt.IsZero() }
func (u User) LiveStopped() bool { return !u.LiveStoppedAt.IsZero() }

// LiveCaps cap a user's live buys in whole dollars (D222): what one order may spend, and
// what they may spend and hold in a day. Nil is the instance's cap, 0 none.
type LiveCaps struct {
	PerOrder *int
	PerDay   *int
}

// Over is the user's caps, or the instance's where they have none of their own.
func (c LiveCaps) Over(instance LiveCaps) LiveCaps {
	if c.PerOrder == nil {
		c.PerOrder = instance.PerOrder
	}

	if c.PerDay == nil {
		c.PerDay = instance.PerDay
	}

	return c
}

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
	// Scopes are what it may do (D398); nil, for a token made before scopes, is every one.
	Scopes []Scope
}

func (t APIToken) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && !now.Before(t.ExpiresAt)
}

// Invite admits registrations in invite mode (D371): up to MaxUses of them, until it
// expires or an admin revokes it. One mailed to an address admits it once (D181).
type Invite struct {
	ID InviteID
	// Code is the code itself, for admins to copy again (D393); empty for an invite made
	// before codes were kept.
	Code      string
	CreatedBy UserID
	Email     string // the address it was mailed to, which alone signs up with it; empty for a copied link
	CreatedAt time.Time
	ExpiresAt time.Time
	MaxUses   int
	Used      int         // the sign-ups it admitted
	Signups   []InviteUse // who they were, oldest first, where listed
	RevokedAt time.Time
	RevokedBy UserID
}

// InviteUse is a sign-up with an invite.
type InviteUse struct {
	UserID UserID // empty once the account is gone
	Email  string
	At     time.Time
}

type JoinRequestID string

// JoinRequest is someone asking for an invite (D380, D394). They confirm their address by
// a mailed link; only then do admins see it, and approve it, mailing an invite there, or
// deny it.
type JoinRequest struct {
	ID          JoinRequestID
	Email       string
	Note        string // theirs, plain text, at most 500 characters
	CreatedAt   time.Time
	ConfirmedAt time.Time // zero until the link was opened
	DecidedAt   time.Time
	DecidedBy   UserID
	Approved    bool
	InviteID    InviteID // the invite an approval mailed

	// The confirmation link: when it stops working, how many were mailed, the last when.
	LinkExpiresAt time.Time
	Mails         int
	MailedAt      time.Time
}

// JoinRequestStatus is where a request is.
type JoinRequestStatus string

const (
	JoinUnconfirmed JoinRequestStatus = "unconfirmed"
	JoinPending     JoinRequestStatus = "pending"
	JoinApproved    JoinRequestStatus = "approved"
	JoinDenied      JoinRequestStatus = "denied"
)

func (r JoinRequest) Status() JoinRequestStatus {
	switch {
	case r.DecidedAt.IsZero() && r.ConfirmedAt.IsZero():
		return JoinUnconfirmed
	case r.DecidedAt.IsZero():
		return JoinPending
	case r.Approved:
		return JoinApproved
	}

	return JoinDenied
}

// InviteStatus is whether an invite still admits sign-ups, and why not.
type InviteStatus string

const (
	InviteOpen    InviteStatus = "open"
	InviteUsedUp  InviteStatus = "used"
	InviteExpired InviteStatus = "expired"
	InviteRevoked InviteStatus = "revoked"
)

func (i Invite) Status(now time.Time) InviteStatus {
	switch {
	case !i.RevokedAt.IsZero():
		return InviteRevoked
	case i.Used >= i.MaxUses:
		return InviteUsedUp
	case !now.Before(i.ExpiresAt):
		return InviteExpired
	}

	return InviteOpen
}

// EmailTokenPurpose is what a mailed link does.
type EmailTokenPurpose string

const (
	EmailTokenVerify EmailTokenPurpose = "verify" // verifies the address
	EmailTokenReset  EmailTokenPurpose = "reset"  // sets a new password
)

// EmailToken is a link mailed to an account. Its secret is only ever stored hashed.
type EmailToken struct {
	UserID    UserID
	Purpose   EmailTokenPurpose
	CreatedAt time.Time
	ExpiresAt time.Time
}
