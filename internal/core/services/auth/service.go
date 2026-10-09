// Package auth registers users, logs them in and authenticates requests, by browser
// session or API token. It also holds the admin operations on users and invites.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	apiTokenPrefix = "pt_"
	invitePrefix   = "inv_"
	prefixLength   = 10 // characters of an API token kept to recognise it in lists
	maxUserAgent   = 512
)

type Config struct {
	Registration       domain.RegistrationMode
	SessionIdleTTL     time.Duration // a session unused this long expires
	SessionAbsoluteTTL time.Duration // a session never lives longer than this
	InviteTTL          time.Duration
	// TouchInterval limits how often a session or token records its use, to save writes.
	TouchInterval     time.Duration
	MinPasswordLength int
}

func DefaultConfig() Config {
	return Config{
		Registration:       domain.RegistrationInvite,
		SessionIdleTTL:     7 * 24 * time.Hour,
		SessionAbsoluteTTL: 30 * 24 * time.Hour,
		InviteTTL:          7 * 24 * time.Hour,
		TouchInterval:      time.Minute,
		MinPasswordLength:  12,
	}
}

// maxPasswordLength bounds hashing work per request; passphrases fit comfortably.
const maxPasswordLength = 1024

// Repositories are the stores the service needs.
type Repositories struct {
	Users    ports.UserRepository
	Sessions ports.SessionRepository
	Tokens   ports.APITokenRepository
	Invites  ports.InviteRepository
	Tx       ports.Transactor
}

type Service struct {
	cfg     Config
	repos   Repositories
	hasher  ports.PasswordHasher
	limiter ports.RateLimiter
	log     logging.Logger
	now     func() time.Time

	workflows WorkflowStopper // optional; commands without the engine have none

	// dummyHash is verified against when an email is unknown, so a miss takes as long
	// as a wrong password and response times do not reveal which emails exist.
	dummyHash string
}

type Option func(*Service)

// WorkflowStopper stops a user's workflows. Disabling a user calls it.
type WorkflowStopper interface {
	StopAll(ctx context.Context, owner domain.UserID) error
}

// WithWorkflowStopper stops a user's workflows when an admin disables them.
func WithWorkflowStopper(w WorkflowStopper) Option {
	return func(s *Service) { s.workflows = w }
}

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(cfg Config, repos Repositories, hasher ports.PasswordHasher, limiter ports.RateLimiter, log logging.Logger, opts ...Option) (*Service, error) {
	if !cfg.Registration.Valid() {
		return nil, fmt.Errorf("auth: invalid registration mode %q", cfg.Registration)
	}

	dummy, err := hasher.Hash("polythought-dummy-password")
	if err != nil {
		return nil, fmt.Errorf("auth: prepare dummy hash: %w", err)
	}

	s := &Service{
		cfg:       cfg,
		repos:     repos,
		hasher:    hasher,
		limiter:   limiter,
		log:       log.With("component", "auth"),
		now:       now,
		dummyHash: dummy,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// Principal is who a request acts as. Exactly one of SessionID and TokenID is set.
type Principal struct {
	User      domain.User
	SessionID domain.SessionID
	TokenID   domain.APITokenID
}

func (p Principal) ViaSession() bool { return p.SessionID != "" }

// now is UTC at the database's microsecond precision, so values the service returns
// equal the ones read back later.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Microsecond)
}

func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// newSecret returns a random URL-safe secret with 256 bits of entropy.
func newSecret(prefix string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: random: %w", err)
	}

	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSecret is how sessions, tokens and invites are stored and looked up. The secrets
// are random 256-bit values, so a fast hash is enough; no salt or stretching needed.
func hashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}
