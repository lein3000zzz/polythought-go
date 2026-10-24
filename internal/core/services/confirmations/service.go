// Package confirmations holds the live actions tokens and agents ask for until their users
// confirm them (D385, D406): going live, switching on or running a workflow that trades
// live, changing one that is on, turning the kill switch off. A confirmed one is carried
// out as it was asked, in the background, and how it ended kept.
package confirmations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	// recent is how many a user sees, the newest first.
	recent = 50
	// maxPending is how many may wait for one user at once.
	maxPending = 20
	// carryFor bounds carrying one out: a run by hand waits for its execution to end.
	carryFor = 10 * time.Minute
)

// Do carries out a confirmed action as p, the token or the agent that asked: what came of
// it, said for the user, such as an execution's ID.
type Do func(ctx context.Context, p auth.Principal, c domain.Confirmation) (string, error)

// Users records which actions a user lets go unconfirmed.
type Users interface {
	SetUnconfirmed(ctx context.Context, id domain.UserID, u domain.Unconfirmed) error
}

type Service struct {
	repo   ports.ConfirmationRepository
	users  Users
	log    logging.Logger
	now    func() time.Time
	expiry func() time.Duration

	mu sync.Mutex
	do map[domain.ConfirmKind]Do
	wg sync.WaitGroup
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithExpiry is how long one waits for its user, read as each is made: an hour without it.
func WithExpiry(f func() time.Duration) Option {
	return func(s *Service) { s.expiry = f }
}

func New(repo ports.ConfirmationRepository, users Users, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		repo: repo, users: users, log: log.With("component", "confirmations"),
		now:    func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		expiry: func() time.Duration { return time.Hour },
		do:     map[domain.ConfirmKind]Do{},
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Handle says how confirmed actions of kind are carried out.
func (s *Service) Handle(kind domain.ConfirmKind, do Do) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.do[kind] = do
}

// Hold is nil when p's action of kind goes ahead: the user's own, one the user confirmed,
// or one the user lets go unconfirmed. Otherwise it keeps the action waiting for the user,
// what says what it does and payload what carrying it out needs, and says so with a
// *domain.AwaitingError.
func (s *Service) Hold(ctx context.Context, p auth.Principal, kind domain.ConfirmKind, wf domain.WorkflowID, what string, payload any) error {
	if p.Itself() || p.Confirmed || !p.User.Unconfirmed.Waits(kind) {
		return nil
	}

	now := s.now()

	n, err := s.repo.CountPending(ctx, p.User.ID, now)
	if err != nil {
		return err
	}

	if n >= maxPending {
		return fmt.Errorf("%w: %d live actions wait for the user already; they confirm or refuse them in Polythought",
			domain.ErrRateLimited, n)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	c := domain.Confirmation{
		ID: domain.ConfirmationID(uuid.Must(uuid.NewV7()).String()), OwnerID: p.User.ID, Kind: kind, WorkflowID: wf,
		GrantID: p.GrantID, TokenID: p.TokenID, Agent: p.Agent, Scopes: p.Scopes, What: what, Payload: data,
		Status: domain.ConfirmPending, CreatedAt: now, ExpiresAt: now.Add(s.expiry()),
	}

	if err := s.repo.Create(ctx, c); err != nil {
		return err
	}

	s.log.Info(ctx, "Live action waits for its user", "user_id", p.User.ID, "confirmation_id", c.ID, "kind", kind,
		"workflow_id", wf, "grant_id", p.GrantID, "token_id", p.TokenID)

	return &domain.AwaitingError{Confirmation: c}
}

// Recent are the user's, the newest first, each with its status now.
func (s *Service) Recent(ctx context.Context, p auth.Principal) ([]domain.Confirmation, error) {
	list, err := s.repo.Recent(ctx, p.User.ID, recent)
	if err != nil {
		return nil, err
	}

	now := s.now()
	for i := range list {
		list[i].Status = list[i].StatusAt(now)
	}

	return list, nil
}

// Get is one of the user's, with its status now.
func (s *Service) Get(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	c, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.Confirmation{}, err
	}

	c.Status = c.StatusAt(s.now())

	return c, nil
}

// Confirm carries out a waiting action as it was asked, at a browser only. It goes on in
// the background; Get says how it ended.
func (s *Service) Confirm(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	c, err := s.decide(ctx, p, id, domain.ConfirmConfirmed)
	if err != nil {
		return domain.Confirmation{}, err
	}

	s.mu.Lock()
	do := s.do[c.Kind]
	s.mu.Unlock()

	// As it was asked: the user now, the token's or the agent's scopes then.
	asker := auth.Principal{User: p.User, GrantID: c.GrantID, TokenID: c.TokenID, Agent: c.Agent, Scopes: c.Scopes, Confirmed: true}

	s.wg.Add(1)

	go func() {
		defer s.wg.Done()

		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), carryFor)
		defer cancel()

		status, result := domain.ConfirmDone, ""

		if do == nil {
			status, result = domain.ConfirmFailed, "it cannot be carried out on this server"
		} else if out, err := do(ctx, asker, c); err != nil {
			status, result = domain.ConfirmFailed, err.Error()
		} else {
			result = out
		}

		if err := s.repo.Finish(ctx, c.ID, status, result); err != nil {
			s.log.Error(ctx, "Confirmed action's end not kept", "confirmation_id", c.ID, logging.Err(err))
		}

		s.log.Info(ctx, "Confirmed action carried out", "user_id", c.OwnerID, "confirmation_id", c.ID, "kind", c.Kind, "status", status)
	}()

	return c, nil
}

// Refuse ends a waiting action without carrying it out, at a browser only.
func (s *Service) Refuse(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	c, err := s.decide(ctx, p, id, domain.ConfirmRefused)
	if err == nil {
		s.log.Info(ctx, "Live action refused", "user_id", c.OwnerID, "confirmation_id", c.ID, "kind", c.Kind)
	}

	return c, err
}

func (s *Service) decide(ctx context.Context, p auth.Principal, id domain.ConfirmationID, status domain.ConfirmStatus) (domain.Confirmation, error) {
	if !p.ViaSession() {
		return domain.Confirmation{}, fmt.Errorf("%w: only the user, at a browser, confirms or refuses", domain.ErrForbidden)
	}

	c, err := s.repo.Decide(ctx, p.User.ID, id, status, s.now())
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		return c, err
	}

	now, err := s.Get(ctx, p, id)
	if err != nil {
		return domain.Confirmation{}, err
	}

	return domain.Confirmation{}, fmt.Errorf("%w: it is %s already", domain.ErrConflict, now.Status)
}

// SetUnconfirmed records which of the user's tokens' and agents' live actions go ahead
// without them confirming, at a browser only.
func (s *Service) SetUnconfirmed(ctx context.Context, p auth.Principal, u domain.Unconfirmed) error {
	if !p.ViaSession() {
		return fmt.Errorf("%w: only at a browser", domain.ErrForbidden)
	}

	if err := s.users.SetUnconfirmed(ctx, p.User.ID, u); err != nil {
		return err
	}

	s.log.Info(ctx, "Live actions to confirm changed", "user_id", p.User.ID, "go_live", !u.GoLive, "run", !u.Run,
		"change", !u.Change, "kill_off", !u.KillOff)

	return nil
}

// Wait waits for the confirmed actions being carried out, or for ctx.
func (s *Service) Wait(ctx context.Context) error {
	done := make(chan struct{})

	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
