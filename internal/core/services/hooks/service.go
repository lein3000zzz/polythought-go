// Package hooks is inbound webhooks (D439): each Inbound webhook trigger's secret address,
// /hooks/<secret>, made and renewed for its owner, and the calls to it, each handed to the
// trigger as a run to start, answered at once with that run's ID.
package hooks

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	// Path is where hooks are, under the instance's public URL.
	Path = "/hooks/"

	// waiting is how many calls may wait for a hook's runs; past it, a call is refused.
	waiting = 50

	// blockedFor is how long an address that guessed wrong too often is refused.
	blockedFor = time.Minute
)

var _ ports.HookInbox = (*Service)(nil)

// Workflows reads the owner's workflows, to check the trigger a hook is for.
type Workflows interface {
	Get(ctx context.Context, owner domain.UserID, id domain.WorkflowID) (domain.Workflow, error)
}

type Deps struct {
	Repo      ports.HookRepository
	Workflows Workflows
	Box       ports.SecretBox
	// Enabled says whether an admin lets hooks take calls; nil for never.
	Enabled func() bool
	// PerHook limits each hook's calls, a minute say; Misses, each address's wrong secrets.
	PerHook ports.RateLimiter
	Misses  ports.RateLimiter
	// PublicURL is the instance's, which the addresses start with.
	PublicURL string
	Logger    logging.Logger
	Now       func() time.Time
}

type Service struct {
	deps Deps
	log  logging.Logger

	mu        sync.Mutex
	listeners map[key]*listener
	blocked   map[string]time.Time // addresses refused until then
}

type key struct {
	workflow domain.WorkflowID
	node     domain.NodeID
}

type listener struct {
	calls chan ports.HookCall
}

func New(deps Deps) *Service {
	if deps.Logger == nil {
		deps.Logger = logging.Noop()
	}

	if deps.Now == nil {
		deps.Now = time.Now
	}

	deps.PublicURL = strings.TrimRight(deps.PublicURL, "/")

	return &Service{
		deps:      deps,
		log:       deps.Logger.With("component", "hooks"),
		listeners: map[key]*listener{},
		blocked:   map[string]time.Time{},
	}
}

// Address is a hook as its owner sees it.
type Address struct {
	URL       string
	CreatedAt time.Time
	// Enabled says an admin lets hooks take calls; Listening, that its workflow is on to
	// take them.
	Enabled, Listening bool
}

func (s *Service) enabled() bool {
	return s.deps.Enabled != nil && s.deps.Enabled()
}

// Address is the owner's trigger's address, made the first time it is asked for.
func (s *Service) Address(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID, node domain.NodeID) (Address, error) {
	if err := s.check(ctx, owner, workflow, node); err != nil {
		return Address{}, err
	}

	h, err := s.deps.Repo.Hook(ctx, workflow, node)
	if err == nil {
		secret, err := s.deps.Box.Open(ctx, h.Sealed, aad(workflow, node))
		if err != nil {
			return Address{}, fmt.Errorf("hooks: open the secret: %w", err)
		}

		return s.address(h, string(secret)), nil
	}

	if !errors.Is(err, domain.ErrNotFound) {
		return Address{}, err
	}

	return s.make(ctx, workflow, node)
}

// Renew gives the trigger a new address: the old one answers no more.
func (s *Service) Renew(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID, node domain.NodeID) (Address, error) {
	if err := s.check(ctx, owner, workflow, node); err != nil {
		return Address{}, err
	}

	a, err := s.make(ctx, workflow, node)
	if err == nil {
		s.log.Info(ctx, "Hook renewed", "owner_id", owner, "workflow_id", workflow, "node_id", node)
	}

	return a, err
}

// check says the owner's workflow, as saved, has node as an Inbound webhook trigger.
func (s *Service) check(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID, node domain.NodeID) error {
	w, err := s.deps.Workflows.Get(ctx, owner, workflow)
	if err != nil {
		return err
	}

	n, ok := w.Node(node)
	if !ok || n.Type != triggers.WebhookType {
		return fmt.Errorf("%w: the saved workflow has no Inbound webhook %q: save it first", domain.ErrNotFound, node)
	}

	return nil
}

func (s *Service) make(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID) (Address, error) {
	secret, err := newSecret()
	if err != nil {
		return Address{}, err
	}

	sealed, err := s.deps.Box.Seal(ctx, []byte(secret), aad(workflow, node))
	if err != nil {
		return Address{}, fmt.Errorf("hooks: seal the secret: %w", err)
	}

	h := domain.Hook{WorkflowID: workflow, NodeID: node, Sealed: sealed, CreatedAt: s.deps.Now().UTC()}
	if err := s.deps.Repo.Put(ctx, h, hash(secret)); err != nil {
		return Address{}, err
	}

	return s.address(h, secret), nil
}

func (s *Service) address(h domain.Hook, secret string) Address {
	s.mu.Lock()
	_, listening := s.listeners[key{h.WorkflowID, h.NodeID}]
	s.mu.Unlock()

	return Address{
		URL: s.deps.PublicURL + Path + secret, CreatedAt: h.CreatedAt,
		Enabled: s.enabled(), Listening: listening,
	}
}

// Call takes a call to the hook secret names, from address, for a run: its ID, given
// before the run starts, or why it was refused.
func (s *Service) Call(ctx context.Context, secret, address string, body []byte, query map[string][]string) (domain.ExecutionID, error) {
	if !s.enabled() {
		return "", domain.ErrHooksOff
	}

	if s.isBlocked(address) {
		return "", fmt.Errorf("%w: too many calls to addresses that are not hooks", domain.ErrRateLimited)
	}

	h, err := s.deps.Repo.ByHash(ctx, hash(secret))
	if errors.Is(err, domain.ErrNotFound) {
		s.missed(address)
		return "", domain.ErrNotFound
	}

	if err != nil {
		return "", err
	}

	if s.deps.PerHook != nil && !s.deps.PerHook.Allow(string(h.WorkflowID)+"/"+string(h.NodeID)) {
		return "", fmt.Errorf("%w: this hook takes no more calls this minute", domain.ErrRateLimited)
	}

	read, err := bodyOf(body)
	if err != nil {
		return "", err
	}

	c := ports.HookCall{
		Execution: domain.ExecutionID(uuid.Must(uuid.NewV7()).String()),
		Body:      read, Query: queryOf(query), At: s.deps.Now().UTC(),
	}

	if err := s.deliver(key{h.WorkflowID, h.NodeID}, c); err != nil {
		return "", err
	}

	s.log.Debug(ctx, "Hook called", "workflow_id", h.WorkflowID, "node_id", h.NodeID, "execution_id", c.Execution)

	return c.Execution, nil
}

func (s *Service) deliver(k key, c ports.HookCall) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	l, ok := s.listeners[k]
	if !ok {
		return domain.ErrHookIdle
	}

	select {
	case l.calls <- c:
		return nil
	default:
		return fmt.Errorf("%w: %d calls wait for this workflow's runs already", domain.ErrRateLimited, waiting)
	}
}

// Listen hands the trigger its calls until cancel; a trigger started again takes them
// from then on.
func (s *Service) Listen(workflow domain.WorkflowID, node domain.NodeID) (<-chan ports.HookCall, func()) {
	k := key{workflow, node}
	l := &listener{calls: make(chan ports.HookCall, waiting)}

	s.mu.Lock()
	s.listeners[k] = l
	s.mu.Unlock()

	return l.calls, func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if s.listeners[k] == l {
			delete(s.listeners, k)
		}
	}
}

func (s *Service) isBlocked(address string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	until, ok := s.blocked[address]
	if ok && !s.deps.Now().Before(until) {
		delete(s.blocked, address)
		return false
	}

	return ok
}

// missed counts a wrong secret against the address, which past its limit is refused for a
// while, whatever it calls.
func (s *Service) missed(address string) {
	if s.deps.Misses == nil || s.deps.Misses.Allow(address) {
		return
	}

	now := s.deps.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for a, until := range s.blocked {
		if !now.Before(until) {
			delete(s.blocked, a)
		}
	}

	s.blocked[address] = now.Add(blockedFor)
}

// bodyOf is a call's body: its JSON, read, numbers exact; else its text; nil for none.
func bodyOf(raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err == nil && !dec.More() {
		return v, nil
	}

	if utf8.Valid(raw) {
		return string(raw), nil
	}

	return nil, fmt.Errorf("%w: the body is neither JSON nor text", domain.ErrInvalidInput)
}

// queryOf is the query by name: a value, or the values in a list.
func queryOf(q map[string][]string) map[string]any {
	out := make(map[string]any, len(q))

	for name, values := range q {
		switch len(values) {
		case 0:
			out[name] = ""
		case 1:
			out[name] = values[0]
		default:
			list := make([]any, len(values))
			for i, v := range values {
				list[i] = v
			}

			out[name] = list
		}
	}

	return out
}

// newSecret is 256 random bits, URL-safe.
func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("hooks: random: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hash is how a secret is found: random 256 bits, so a fast hash does.
func hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// aad binds a sealed secret to its hook.
func aad(workflow domain.WorkflowID, node domain.NodeID) []byte {
	return []byte("hook:" + string(workflow) + "/" + string(node))
}
