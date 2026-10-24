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
	"unicode/utf8"

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

// Notifier tells the user of what waits for them, each way they chose, and says there how
// it was decided: the notify service.
type Notifier interface {
	// ConfirmationAsked gives the Telegram message's ID, 0 when none went.
	ConfirmationAsked(ctx context.Context, c domain.Confirmation) (int, error)
	ConfirmationClosed(ctx context.Context, c domain.Confirmation)
}

// Users records which actions a user lets go unconfirmed, and whether they are asked in the
// agent's window too.
type Users interface {
	SetUnconfirmed(ctx context.Context, id domain.UserID, u domain.Unconfirmed) error
	SetAskInAgent(ctx context.Context, id domain.UserID, on bool) error
}

type Service struct {
	repo   ports.ConfirmationRepository
	users  Users
	log    logging.Logger
	now    func() time.Time
	expiry func() time.Duration
	notify Notifier

	mu      sync.Mutex
	do      map[domain.ConfirmKind]Do
	waiting map[domain.ConfirmationID]chan domain.Confirmation // tool calls awaiting the user (D409)
	wg      sync.WaitGroup
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithNotifier tells users of what waits for them, on Telegram with buttons.
func WithNotifier(n Notifier) Option {
	return func(s *Service) { s.notify = n }
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

// Hold is nil when p's action goes ahead: the user's own, one the user confirmed, or one
// the user lets go unconfirmed. Otherwise it keeps the action waiting for the user, as a
// says, and says so with a *domain.AwaitingError.
func (s *Service) Hold(ctx context.Context, p auth.Principal, a domain.Ask) error {
	kind, wf, what := a.Kind, a.WorkflowID, a.What

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

	data, err := json.Marshal(a.Payload)
	if err != nil {
		return err
	}

	c := domain.Confirmation{
		ID: domain.ConfirmationID(uuid.Must(uuid.NewV7()).String()), OwnerID: p.User.ID, Kind: kind, WorkflowID: wf,
		GrantID: p.GrantID, TokenID: p.TokenID, Agent: p.Actor("").Name, Scopes: p.Scopes, What: what, About: a.About, Detail: detailOf(a.Detail), Payload: data,
		Status: domain.ConfirmPending, CreatedAt: now, ExpiresAt: now.Add(s.expiry()),
	}

	if err := s.repo.Create(ctx, c); err != nil {
		return err
	}

	s.tell(ctx, &c)

	s.log.Info(ctx, "Live action waits for its user", "user_id", p.User.ID, "confirmation_id", c.ID, "kind", kind,
		"workflow_id", wf, "grant_id", p.GrantID, "token_id", p.TokenID)

	return &domain.AwaitingError{Confirmation: c}
}

// tell tells the user, the action waiting whatever comes of it, and keeps the message.
func (s *Service) tell(ctx context.Context, c *domain.Confirmation) {
	if s.notify == nil {
		return
	}

	msg, err := s.notify.ConfirmationAsked(ctx, *c)
	if err != nil {
		s.log.Warn(ctx, "User not told of a live action waiting", "confirmation_id", c.ID, logging.Err(err))
	}

	if msg == 0 {
		return
	}

	c.Message = msg
	if err := s.repo.SetMessage(ctx, c.ID, msg); err != nil {
		s.log.Warn(ctx, "Confirmation's message not kept", "confirmation_id", c.ID, logging.Err(err))
	}
}

// closed says, where the user was told, how one was decided or ended.
func (s *Service) closed(ctx context.Context, c domain.Confirmation) {
	if s.notify != nil {
		s.notify.ConfirmationClosed(ctx, c)
	}
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

// Confirm carries out a waiting action as it was asked, at a browser or in the user's
// Telegram chat. A tool call waiting for the answer in the agent's window carries it out
// itself (D409); otherwise it goes on in the background. Get says how it ended.
func (s *Service) Confirm(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	c, err := s.decide(ctx, p, id, domain.ConfirmConfirmed)
	if err != nil {
		return domain.Confirmation{}, err
	}

	s.closed(ctx, c)

	if s.handOff(c) {
		return c, nil
	}

	s.mu.Lock()
	do := s.do[c.Kind]
	s.mu.Unlock()

	// As it was asked: the user now, the token's or the agent's name and scopes then.
	asker := auth.Principal{User: p.User, GrantID: c.GrantID, TokenID: c.TokenID, Scopes: c.Scopes, Confirmed: true, Confirming: &c}
	if c.GrantID != "" {
		asker.Agent = c.Agent
	} else {
		asker.TokenName = c.Agent
	}

	s.wg.Add(1)

	go func() {
		defer s.wg.Done()

		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), carryFor)
		defer cancel()

		var (
			out string
			err error
		)

		if do == nil {
			err = errors.New("it cannot be carried out on this server")
		} else {
			out, err = do(ctx, asker, c)
		}

		s.Finish(ctx, c, out, err)
	}()

	return c, nil
}

// Finish records how a confirmed action ended, carried out by the service or by the tool
// call that waited for it, and says so where the user was told.
func (s *Service) Finish(ctx context.Context, c domain.Confirmation, out string, err error) {
	status, result := domain.ConfirmDone, out
	if err != nil {
		status, result = domain.ConfirmFailed, err.Error()
	}

	if err := s.repo.Finish(ctx, c.ID, status, result); err != nil {
		s.log.Error(ctx, "Confirmed action's end not kept", "confirmation_id", c.ID, logging.Err(err))
	}

	ended := c
	ended.Status, ended.Result = status, result
	s.closed(ctx, ended)

	s.log.Info(ctx, "Confirmed action carried out", "user_id", c.OwnerID, "confirmation_id", c.ID, "kind", c.Kind, "status", status)
}

// Refuse ends a waiting action without carrying it out, at a browser or on Telegram.
func (s *Service) Refuse(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	c, err := s.decide(ctx, p, id, domain.ConfirmRefused)
	if err == nil {
		s.refused(ctx, c)
	}

	return c, err
}

func (s *Service) refused(ctx context.Context, c domain.Confirmation) {
	s.log.Info(ctx, "Live action refused", "user_id", c.OwnerID, "confirmation_id", c.ID, "kind", c.Kind, "via", c.DecidedVia)
	s.closed(ctx, c)
	s.handOff(c)
}

func (s *Service) decide(ctx context.Context, p auth.Principal, id domain.ConfirmationID, status domain.ConfirmStatus) (domain.Confirmation, error) {
	// The user at a browser, or in their own Telegram chat; never a token or an agent.
	if !p.Itself() {
		return domain.Confirmation{}, fmt.Errorf("%w: only the user confirms or refuses, at a browser or on Telegram", domain.ErrForbidden)
	}

	via := domain.ConfirmViaTelegram
	if p.ViaSession() {
		via = domain.ConfirmViaApp
	}

	return s.decideVia(ctx, p, id, status, via)
}

func (s *Service) decideVia(
	ctx context.Context, p auth.Principal, id domain.ConfirmationID, status domain.ConfirmStatus, via domain.ConfirmVia,
) (domain.Confirmation, error) {
	c, err := s.repo.Decide(ctx, p.User.ID, id, status, s.now(), via)
	if err == nil || !errors.Is(err, domain.ErrConflict) {
		return c, err
	}

	now, err := s.Get(ctx, p, id)
	if err != nil {
		return domain.Confirmation{}, err
	}

	// Its buttons, pressed too late, say how it ended.
	if now.Status == domain.ConfirmExpired {
		s.closed(ctx, now)
	}

	return domain.Confirmation{}, fmt.Errorf("%w: it is %s already", domain.ErrConflict, now.Status)
}

// The agent's own window (D409): a tool call that held an action asks the user there,
// where they let it, and waits, a little, for the answer; confirmed, the call carries it
// out itself, so the agent hears what came of it as if it had gone ahead.

// ConfirmInWindow confirms one of p's own waiting actions, as the user answered in its
// window, where they let agents ask there. The caller carries it out, then calls Finish.
func (s *Service) ConfirmInWindow(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	if err := s.mayAsk(ctx, p, id); err != nil {
		return domain.Confirmation{}, err
	}

	// A token's client is a program more often than not, and whatever holds the token would
	// answer its own form (A6): it asks by the link, the user deciding in Polythought.
	if p.GrantID == "" {
		return domain.Confirmation{}, fmt.Errorf("%w: an API token's action is confirmed in Polythought, not in its own window", domain.ErrForbidden)
	}

	c, err := s.decideVia(ctx, p, id, domain.ConfirmConfirmed, domain.ConfirmViaAgent)
	if err == nil {
		s.closed(ctx, c)
	}

	return c, err
}

// RefuseInWindow refuses one of p's own waiting actions, as the user answered in its
// window.
func (s *Service) RefuseInWindow(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (domain.Confirmation, error) {
	if err := s.mayAsk(ctx, p, id); err != nil {
		return domain.Confirmation{}, err
	}

	c, err := s.decideVia(ctx, p, id, domain.ConfirmRefused, domain.ConfirmViaAgent)
	if err == nil {
		s.refused(ctx, c)
	}

	return c, err
}

// mayAsk is nil when the user lets p's window ask, and the action is p's own.
func (s *Service) mayAsk(ctx context.Context, p auth.Principal, id domain.ConfirmationID) error {
	if !p.User.AskInAgent || p.Itself() {
		return fmt.Errorf("%w: the user has not let agents ask in their own window", domain.ErrForbidden)
	}

	c, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return err
	}

	if c.GrantID != p.GrantID || c.TokenID != p.TokenID {
		return domain.ErrNotFound // another agent's, or another token's
	}

	return nil
}

// Await waits for the user to decide one of p's own waiting actions in Polythought, while
// ctx lasts (D409). Confirmed, it is the caller's to carry out, carry says so, and Finish
// says how it ended; refused, the error is domain.ErrConfirmRefused. Waiting no more, it is the
// service's again to carry out when the user confirms it.
func (s *Service) Await(ctx context.Context, p auth.Principal, id domain.ConfirmationID) (c domain.Confirmation, carry bool, err error) {
	if err := s.mayAsk(ctx, p, id); err != nil {
		return domain.Confirmation{}, false, err
	}

	ch := make(chan domain.Confirmation, 1)

	s.mu.Lock()
	if s.waiting == nil {
		s.waiting = map[domain.ConfirmationID]chan domain.Confirmation{}
	}
	s.waiting[id] = ch
	s.mu.Unlock()

	select {
	case c = <-ch:
	case <-ctx.Done():
		s.mu.Lock()
		_, still := s.waiting[id]
		delete(s.waiting, id)
		s.mu.Unlock()

		if still {
			return domain.Confirmation{}, false, ctx.Err()
		}

		c = <-ch // decided as it ended: handed over already
	}

	if c.Status == domain.ConfirmRefused {
		return c, false, domain.ErrConfirmRefused
	}

	return c, true, nil
}

// Awaited says a tool call awaits the user's decision of id, as the agent's window asked.
func (s *Service) Awaited(id domain.ConfirmationID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.waiting[id]

	return ok
}

// handOff gives a decision to the tool call awaiting it, if one is: true when it did.
func (s *Service) handOff(c domain.Confirmation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch, ok := s.waiting[c.ID]
	if ok {
		delete(s.waiting, c.ID)
		ch <- c
	}

	return ok
}

// SetAskInAgent records whether the user's tokens' and agents' live actions are also asked
// in the agent's own window, at a browser only (D409).
func (s *Service) SetAskInAgent(ctx context.Context, p auth.Principal, on bool) error {
	if !p.ViaSession() {
		return fmt.Errorf("%w: only at a browser", domain.ErrForbidden)
	}

	if err := s.users.SetAskInAgent(ctx, p.User.ID, on); err != nil {
		return err
	}

	s.log.Info(ctx, "Asking in the agent's window changed", "user_id", p.User.ID, "on", on)

	return nil
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

// maxDetail is the most of what a held action changes that is kept: past it, the rest is
// said to be there.
const maxDetail = 8 << 10

// detailOf is a held action's detail, cut to maxDetail.
func detailOf(d string) string {
	if len(d) <= maxDetail {
		return d
	}

	cut := d[:maxDetail]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}

	return cut + "\n… and more: open the workflow to see all of it"
}
