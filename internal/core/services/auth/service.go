// Package auth registers users, logs them in and authenticates requests, by browser
// session or API token. It also holds the admin operations on users and invites.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"slices"
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
	CodeAccess        domain.CodeAccess // who may write code
	// VerifyEmail makes people who sign up themselves verify their address by mail (D173).
	VerifyEmail bool
	// PasswordReset lets people set a new password with a mailed link (D176).
	PasswordReset bool
	// InviteEmail lets admins mail invites (D180).
	InviteEmail bool
	// Requests lets people ask to join while registration is by invite (D394), and
	// RequestDeniedMail tells those denied.
	Requests          bool
	RequestDeniedMail bool
	// AgentPasswords lets an admin's agents and tokens create accounts with a password they
	// give (D410); otherwise they invite.
	AgentPasswords bool
}

func DefaultConfig() Config {
	return Config{
		Registration:       domain.RegistrationInvite,
		SessionIdleTTL:     7 * 24 * time.Hour,
		SessionAbsoluteTTL: 30 * 24 * time.Hour,
		InviteTTL:          7 * 24 * time.Hour,
		TouchInterval:      time.Minute,
		MinPasswordLength:  12,
		CodeAccess:         domain.CodeEveryone,
		PasswordReset:      true,
		InviteEmail:        true,
		RequestDeniedMail:  true,
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
	// EmailTokens keeps mailed links; needed with WithAccountMail.
	EmailTokens ports.EmailTokenRepository
	// JoinRequests keeps requests to join (D394); without it, nobody can ask.
	JoinRequests ports.JoinRequestRepository
	Tx           ports.Transactor
}

type Service struct {
	config   func() Config
	repos    Repositories
	hasher   ports.PasswordHasher
	limiter  ports.RateLimiter
	later    func(func())      // runs work after the answer; tests run it at once
	accounts ports.RateLimiter // optional: a looser limit per address, from every IP together (R9)
	log      logging.Logger
	now      func() time.Time

	workflows WorkflowStopper   // optional; commands without the engine have none
	live      LiveStopper       // optional; without it, disabling stops no live trading
	mail      ports.AccountMail // optional; without it, nothing is mailed
	inviteBox ports.SecretBox   // optional; without it, an invite's code shows only as made
	joins     JoinNotifier      // optional; without it, admins are told of no request to join
	confirm   Confirmer         // optional; without it, an admin's agents' work on users goes ahead at once

	instanceCaps func() (domain.LiveCaps, domain.AgentCaps) // optional; without it, any cap left to the instance's is a raise

	// dummyHash is verified against when an email is unknown, so a miss takes as long
	// as a wrong password and response times do not reveal which emails exist.
	dummyHash string
}

type Option func(*Service)

// WorkflowStopper stops a user's workflows. Disabling a user calls it.
type WorkflowStopper interface {
	StopAll(ctx context.Context, owner domain.UserID) error
}

// WithLater replaces how work after an answer is run, for tests: run it at once.
func WithLater(later func(func())) Option {
	return func(s *Service) { s.later = later }
}

// laterFor is how long work after an answer may take.
const laterFor = time.Minute

// background runs f after the answer, with ctx's values but not its end.
func (s *Service) background(ctx context.Context, f func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), laterFor)

	s.later(func() {
		defer cancel()
		f(ctx)
	})
}

// WithAccountLimiter limits sign-in attempts per address from all IPs together: looser
// than the limit per IP and address, so nobody locks another out (R9).
func WithAccountLimiter(l ports.RateLimiter) Option {
	return func(s *Service) { s.accounts = l }
}

// WithWorkflowStopper stops a user's workflows when an admin disables them.
func WithWorkflowStopper(w WorkflowStopper) Option {
	return func(s *Service) { s.workflows = w }
}

// LiveStopper turns a user's kill switch on, cancelling their resting live orders.
// Disabling a user calls it (D226).
type LiveStopper interface {
	StopLive(ctx context.Context, owner domain.UserID) error
}

// WithLiveStopper stops a user's live trading when an admin disables them.
func WithLiveStopper(l LiveStopper) Option {
	return func(s *Service) { s.live = l }
}

// WithInviteBox seals invites' codes, so admins can copy them again (D393).
func WithInviteBox(box ports.SecretBox) Option {
	return func(s *Service) { s.inviteBox = box }
}

// WithLiveConfig reads the configuration through config on every use, so changes to it
// apply at once. It replaces the configuration New was given.
func WithLiveConfig(config func() Config) Option {
	return func(s *Service) { s.config = config }
}

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(cfg Config, repos Repositories, hasher ports.PasswordHasher, limiter ports.RateLimiter, log logging.Logger, opts ...Option) (*Service, error) {
	if !cfg.Registration.Valid() {
		return nil, fmt.Errorf("auth: invalid registration mode %q", cfg.Registration)
	}

	if !cfg.CodeAccess.Valid() {
		return nil, fmt.Errorf("auth: invalid code access %q", cfg.CodeAccess)
	}

	dummy, err := hasher.Hash("polythought-dummy-password")
	if err != nil {
		return nil, fmt.Errorf("auth: prepare dummy hash: %w", err)
	}

	s := &Service{
		config:    func() Config { return cfg },
		repos:     repos,
		hasher:    hasher,
		limiter:   limiter,
		log:       log.With("component", "auth"),
		now:       now,
		dummyHash: dummy,
		later:     func(f func()) { go f() },
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
	// GrantID is set for an agent signed in with OAuth (D397), Agent to its name; TokenName
	// is a token's.
	GrantID   domain.GrantID
	Agent     string
	TokenName string
	// Scopes are what a token or an agent may do (D398).
	Scopes []domain.Scope
	// Confirmed says the user confirmed this live action of a token's or an agent's
	// (D406): it goes ahead as it was asked.
	Confirmed bool
}

func (p Principal) ViaSession() bool { return p.SessionID != "" }

// Can says the principal's scopes allow scope (D398, D399). Scopes limit tokens and
// agents: the user themself, at a browser or in their Telegram chat, has every scope, as
// has a token made before scopes. Every scope brings read, so an agent with none left has
// none. Whether the user's role allows it is the services' to say.
func (p Principal) Can(scope domain.Scope) bool {
	switch {
	case p.Itself():
		return true
	case p.TokenID != "" && p.Scopes == nil:
		return true
	case scope == domain.ScopeRead:
		return len(p.Scopes) > 0
	}

	return slices.Contains(p.Scopes, scope)
}

// Itself says the principal is the user themself, at a browser or in their Telegram chat,
// not a token or an agent.
func (p Principal) Itself() bool { return p.TokenID == "" && p.GrantID == "" }

// SeesPersonal says the principal may see personal settings, such as webhook URLs (D401,
// D402): the user themself always, a token or an agent when the user lets them.
func (p Principal) SeesPersonal() bool { return p.Itself() || p.User.Agents.SeePersonal }

// MayDelete says the principal may delete workflows, custom nodes and credentials (D403,
// D404), as SeesPersonal has it.
func (p Principal) MayDelete() bool { return p.Itself() || p.User.Agents.Delete }

// NeedDelete is nil when the principal may delete, and says where the user lets them
// otherwise.
func (p Principal) NeedDelete() error {
	if p.MayDelete() {
		return nil
	}

	return fmt.Errorf("%w: the user has not let agents and tokens delete: Settings, Connected agents", domain.ErrForbidden)
}

// Actor is who the principal is behind what it does, how (D407): zero for the user.
func (p Principal) Actor(how domain.ActorHow) domain.Actor {
	if p.Itself() {
		return domain.Actor{}
	}

	name := p.Agent
	if p.GrantID == "" {
		name = p.TokenName
	}

	return domain.Actor{GrantID: p.GrantID, TokenID: p.TokenID, Name: name, How: how, Confirmed: p.Confirmed}
}

// MayUseCredentials says the principal may make credentials work (D404): save and run
// workflows that use them, test them, change where they go. As SeesPersonal has it.
func (p Principal) MayUseCredentials() bool { return p.Itself() || p.User.Agents.UseCredentials }

// MayAnswer says the principal may answer the questions the user's workflows ask them
// (D405), as SeesPersonal has it.
func (p Principal) MayAnswer() bool { return p.Itself() || p.User.Agents.Answer }

// MayShare says the principal may share workflows to the library (D403), as SeesPersonal
// has it.
func (p Principal) MayShare() bool { return p.Itself() || p.User.Agents.Share }

// Need is nil when the principal's scopes allow one of scopes, and says which it lacks
// otherwise.
func (p Principal) Need(scopes ...domain.Scope) error {
	for _, sc := range scopes {
		if p.Can(sc) {
			return nil
		}
	}

	names := make([]string, len(scopes))
	for i, sc := range scopes {
		names[i] = string(sc)
	}

	return fmt.Errorf("%w: it needs the %s scope", domain.ErrInsufficientScope, strings.Join(names, " or "))
}

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
