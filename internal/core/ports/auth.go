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

// UserRepository returns domain.ErrNotFound for unknown users and domain.ErrEmailTaken
// when an email is registered twice. Emails are matched case-insensitively.
type UserRepository interface {
	Create(ctx context.Context, u domain.User, passwordHash string) error
	ByID(ctx context.Context, id domain.UserID) (domain.User, error)
	// ByEmail also returns the password hash, for login.
	ByEmail(ctx context.Context, email string) (domain.User, string, error)
	PasswordHash(ctx context.Context, id domain.UserID) (string, error)
	SetPasswordHash(ctx context.Context, id domain.UserID, hash string, at time.Time) error
	// Update writes Role, DisabledAt, CodeChosen and LiveCaps; not LiveStoppedAt.
	Update(ctx context.Context, u domain.User) error
	// SetLiveStopped turns the user's kill switch on at at, or off with the zero time
	// (D220).
	SetLiveStopped(ctx context.Context, id domain.UserID, at time.Time) error
	// SetKillStopsAgents records the user's choice that their kill switch stops their
	// agents too (D276).
	SetKillStopsAgents(ctx context.Context, id domain.UserID, on bool) error
	// SetAgentsSeePersonal records whether agents over MCP see the user's workflows'
	// personal settings (D401).
	SetAgentsSeePersonal(ctx context.Context, id domain.UserID, on bool) error
	List(ctx context.Context, f PageFilter) (Page[domain.User], error)
	CountActiveAdmins(ctx context.Context) (int, error)
	// LockActiveAdmins locks the active admins' rows until the surrounding transaction
	// ends and returns how many there are. It must run inside Transactor.WithinTx.
	LockActiveAdmins(ctx context.Context) (int, error)

	// SetEmailVerified records when the account verified its address.
	SetEmailVerified(ctx context.Context, id domain.UserID, at time.Time) error
	// DeleteUnverified removes the account if it has not verified its address;
	// domain.ErrNotFound when it has, or is gone.
	DeleteUnverified(ctx context.Context, id domain.UserID) error
	// PruneUnverified removes the accounts made before before that never verified.
	PruneUnverified(ctx context.Context, before time.Time) (int64, error)
}

// EmailTokenRepository keeps the links mailed to accounts, by the SHA-256 of their token.
type EmailTokenRepository interface {
	Create(ctx context.Context, t domain.EmailToken, tokenHash []byte) error
	// Take uses a link: it removes it and says whose it is; domain.ErrNotFound when no
	// link of purpose has that hash, or it expired by now.
	Take(ctx context.Context, tokenHash []byte, purpose domain.EmailTokenPurpose, now time.Time) (domain.UserID, error)
	// CountSince counts the account's links of purpose made since.
	CountSince(ctx context.Context, user domain.UserID, purpose domain.EmailTokenPurpose, since time.Time) (int, error)
	// DeleteForUser removes the account's links of purpose.
	DeleteForUser(ctx context.Context, user domain.UserID, purpose domain.EmailTokenPurpose) error
	// DeleteExpired removes the links expired by now.
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
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

// StoredInvite is an invite as kept: its code sealed, to open for admins (D393), nil for
// one made before codes were kept.
type StoredInvite struct {
	domain.Invite

	Sealed []byte
}

// InviteRepository keeps invites. A code is found by its hashes: the hash of the code in
// lower case, and for invites made before codes were any case, of the code as given.
type InviteRepository interface {
	// Create keeps an invite; domain.ErrInviteCodeTaken when another has its hash.
	Create(ctx context.Context, inv domain.Invite, codeHash, sealed []byte) error
	// List is every invite, newest first, with its sign-ups.
	List(ctx context.Context) ([]StoredInvite, error)
	// Taken says whether an invite has one of the hashes, whatever became of it.
	Taken(ctx context.Context, codeHashes [][]byte) (bool, error)
	// Consume takes one use of the invite for userID, while it is open at at, or returns
	// domain.ErrInvalidInvite. It is atomic: an invite admits at most MaxUses sign-ups.
	Consume(ctx context.Context, id domain.InviteID, userID domain.UserID, at time.Time) error
	// ByCode is the open invite of the code at at; domain.ErrInvalidInvite when there is
	// none: used up, expired, revoked or never made.
	ByCode(ctx context.Context, codeHashes [][]byte, at time.Time) (domain.Invite, error)
	// Revoke ends an invite, by an admin, at at; domain.ErrNotFound when there is none.
	Revoke(ctx context.Context, id domain.InviteID, by domain.UserID, at time.Time) error
}

// JoinRequestRepository keeps requests to join (D394). An address has one open request
// at most: unconfirmed, or pending an admin.
type JoinRequestRepository interface {
	// Create keeps a request, its confirmation link's hash with it.
	Create(ctx context.Context, r domain.JoinRequest, tokenHash []byte) error
	// Open is the address's open request; domain.ErrNotFound when it has none.
	Open(ctx context.Context, emailNormalized string) (domain.JoinRequest, error)
	// DeniedSince says whether a request of the address was denied at since or after.
	DeniedSince(ctx context.Context, emailNormalized string, since time.Time) (bool, error)
	// Remail gives an unconfirmed request a new link, until expires, mailed at at.
	Remail(ctx context.Context, id domain.JoinRequestID, tokenHash []byte, expires, at time.Time) error
	// Confirm confirms the unconfirmed request of a link working at at, once, and gives
	// it; domain.ErrInvalidLink when there is none.
	Confirm(ctx context.Context, tokenHash []byte, at time.Time) (domain.JoinRequest, error)
	// CountOpen is how many requests are open, unconfirmed or pending.
	CountOpen(ctx context.Context) (int, error)
	// List is the confirmed requests, pending first, then those decided at since or
	// after, each newest first.
	List(ctx context.Context, since time.Time) ([]domain.JoinRequest, error)
	// Decide closes a pending request as approved or not, by an admin, at at, with the
	// invite an approval mailed; domain.ErrNotFound when it is not pending.
	Decide(ctx context.Context, id domain.JoinRequestID, approved bool, by domain.UserID, at time.Time, invite domain.InviteID) error
	// Get is a request; domain.ErrNotFound when there is none.
	Get(ctx context.Context, id domain.JoinRequestID) (domain.JoinRequest, error)
	// PruneUnconfirmed removes the unconfirmed requests whose link stopped working before
	// before.
	PruneUnconfirmed(ctx context.Context, before time.Time) (int64, error)
}

// RateLimiter answers whether one more attempt under key is allowed right now.
type RateLimiter interface {
	Allow(key string) bool
}
