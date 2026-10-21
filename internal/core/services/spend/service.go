// Package spend counts what agent nodes spend with their owners' own keys (D268 to
// D277): every call's tokens, and the dollars a provider reports when it does (D270), by
// user and UTC month. Caps come in 4d2.
package spend

import (
	"context"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.AgentMeter = (*Service)(nil)

// recordFor bounds recording a call: past the run's cancelling, but not for ever, as the
// executor's own writes are; a stopped workflow waits for its nodes.
const recordFor = 10 * time.Second

type Deps struct {
	Usage  ports.AgentUsageRepository
	Logger logging.Logger
	Now    func() time.Time // optional
}

type Service struct {
	usage ports.AgentUsageRepository
	log   logging.Logger
	now   func() time.Time
}

func New(d Deps) *Service {
	now := d.Now
	if now == nil {
		now = time.Now
	}

	return &Service{usage: d.Usage, log: d.Logger.With("component", "spend"), now: now}
}

func (s *Service) Run(run ports.SpendRun) ports.SpendBudget {
	return &budget{s: s, run: run}
}

// Month is owner's spend this month.
func (s *Service) Month(ctx context.Context, owner domain.UserID) (domain.AgentUsage, error) {
	return s.usage.Month(ctx, owner, domain.MonthOf(s.now()))
}

type budget struct {
	s   *Service
	run ports.SpendRun
}

// Admit lets every call go, for now: caps are 4d2's.
func (b *budget) Admit(context.Context, ports.LLMRequest) (ports.SpendHold, string, error) {
	return &hold{b: b, month: domain.MonthOf(b.s.now())}, "", nil
}

// hold is a call's, its month the one it was let go in: one let go at 23:59 on the last
// day counts in that month.
type hold struct {
	b     *budget
	month time.Time
	once  sync.Once
}

func (h *hold) Record(ctx context.Context, usage ports.LLMUsage) {
	h.once.Do(func() {
		delta := domain.AgentUsage{
			Month: h.month, InputTokens: max(usage.InputTokens, 0), OutputTokens: max(usage.OutputTokens, 0),
			Dollars: decimal.Zero, Calls: 1,
		}

		// A free model's 0 is a cost reported too.
		if usage.Cost != nil && !usage.Cost.IsNegative() {
			delta.Dollars, delta.CostedCalls = *usage.Cost, 1
		}

		// The call is made, and billed: a run cancelled since still counts it.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordFor)
		defer cancel()

		run := h.b.run
		if _, err := h.b.s.usage.Add(ctx, run.OwnerID, delta, h.b.s.now()); err != nil {
			h.b.s.log.Error(ctx, "Agent spend not recorded", "user_id", run.OwnerID, "workflow_id", run.WorkflowID,
				"node_id", run.NodeID, "input_tokens", delta.InputTokens, "output_tokens", delta.OutputTokens, logging.Err(err))
		}
	})
}

func (h *hold) Release() {
	h.once.Do(func() {})
}
