// Package history is who switched each workflow and why (D433): kept as the switches
// happen, read by the workflow's owner and their agents, forgotten after a while.
package history

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

type Service struct {
	repo ports.WorkflowHistory
	log  logging.Logger
	now  func() time.Time
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(repo ports.WorkflowHistory, log logging.Logger, opts ...Option) *Service {
	if log == nil {
		log = logging.Noop()
	}

	s := &Service{repo: repo, log: log.With("component", "history"), now: func() time.Time { return time.Now().UTC() }}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// By is a switch the principal made: the agent or token by its name, with why it said it
// did, or the user.
func By(p auth.Principal, workflow domain.WorkflowID, kind domain.SwitchKind) domain.Switch {
	return domain.Switch{WorkflowID: workflow, Kind: kind, By: p.Actor(""), Reason: p.Reason}
}

// Itself is a switch Polythought made, saying why.
func Itself(workflow domain.WorkflowID, kind domain.SwitchKind, why string) domain.Switch {
	return domain.Switch{WorkflowID: workflow, Kind: kind, Polythought: true, Reason: why}
}

// Switched keeps a switch. One not kept is logged: the switch stands whatever, and the
// call that made it may have ended.
func (s *Service) Switched(ctx context.Context, sw domain.Switch) {
	sw.ID = domain.SwitchID(uuid.Must(uuid.NewV7()).String())
	sw.Reason = clip(strings.TrimSpace(sw.Reason), domain.SwitchReasonMost)
	sw.By.How = ""

	if sw.At.IsZero() {
		sw.At = s.now()
	}

	if err := s.repo.Record(context.WithoutCancel(ctx), sw); err != nil {
		s.log.Warn(ctx, "Workflow's switch not kept in its history", "workflow_id", sw.WorkflowID, "kind", sw.Kind, logging.Err(err))
	}
}

// List is a workflow's history, newest first, narrowed to some groups of kinds; none,
// every kind.
func (s *Service) List(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, groups []domain.SwitchGroup, page ports.PageFilter) (ports.Page[domain.Switch], error) {
	f := ports.SwitchFilter{PageFilter: page}

	for _, g := range groups {
		kinds, ok := domain.SwitchGroups[g]
		if !ok {
			return ports.Page[domain.Switch]{}, fmt.Errorf("%w: a kind is on_off, trading, reset or kill_switch, not %q", domain.ErrInvalidInput, g)
		}

		f.Kinds = append(f.Kinds, kinds...)
	}

	return s.repo.List(ctx, p.User.ID, workflow, f)
}

// Forget forgets the switches older than keep: how many.
func (s *Service) Forget(ctx context.Context, keep time.Duration) (int64, error) {
	return s.repo.DeleteBefore(ctx, s.now().Add(-keep))
}

// clip is text cut to most runes.
func clip(text string, most int) string {
	if r := []rune(text); len(r) > most {
		return string(r[:most])
	}

	return text
}
