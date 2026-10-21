package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// Agent spend (D268 to D277): what agent nodes spend with their owners' own keys, counted
// per user per UTC month, every call, failed items' too; and held to caps (4d2).

// AgentMeter counts what agent nodes spend.
type AgentMeter interface {
	// Run opens one node run's spending, for its owner.
	Run(run SpendRun) SpendBudget
}

// SpendRun is one run of an agent node, with the node's own caps for it (D272).
type SpendRun struct {
	OwnerID     domain.UserID
	WorkflowID  domain.WorkflowID
	ExecutionID domain.ExecutionID
	NodeID      domain.NodeID
	Caps        domain.AgentCaps
}

// SpendNotifier tells users their agents reached 80% of a monthly cap, or a cap (D274).
// It must not block.
type SpendNotifier interface {
	AgentSpendNoticed(owner domain.UserID, a domain.SpendAlert)
}

// SpendBudget is one node run's: its calls go through it, a few at once.
type SpendBudget interface {
	// Admit lets a call go, holding what it may take, or says in a sentence why it may
	// not. An error is the meter's own failure.
	Admit(ctx context.Context, req LLMRequest) (hold SpendHold, refused string, err error)
}

// SpendHold is an admitted call's. Either ends it; the first does, the other then does
// nothing.
type SpendHold interface {
	// Record counts what the call took. A failure to is the meter's to log: the call's
	// answer stands.
	Record(ctx context.Context, usage LLMUsage)
	// Release lets the hold go uncounted, the call having failed: the provider says
	// nothing of what a failed call cost.
	Release()
}

// AgentUsageRepository keeps agent spend by user and month.
type AgentUsageRepository interface {
	// Add adds delta to owner's month, delta.Month, and returns the month's new totals.
	Add(ctx context.Context, owner domain.UserID, delta domain.AgentUsage, at time.Time) (domain.AgentUsage, error)
	// Month is owner's spend in the month starting at month: nothing spent when no row.
	Month(ctx context.Context, owner domain.UserID, month time.Time) (domain.AgentUsage, error)
	// ClaimNotice marks the month's notice as sent at caps, if it is still at was, empty
	// for never sent: true for the one caller that does, who then sends it.
	ClaimNotice(ctx context.Context, owner domain.UserID, month time.Time, n domain.SpendNotice, was, caps string, at time.Time) (bool, error)
}
