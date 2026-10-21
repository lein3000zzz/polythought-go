package spend_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/spend"
)

// usage is a repository in memory, its months added to as Postgres does; it notes how
// each add's context was as it came.
type usage struct {
	mu    sync.Mutex
	rows  map[string]domain.AgentUsage
	ctxs  []seen
	fails error
}

type seen struct {
	err      error
	deadline time.Time
	bounded  bool
}

func (u *usage) Add(ctx context.Context, owner domain.UserID, d domain.AgentUsage, _ time.Time) (domain.AgentUsage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	deadline, bounded := ctx.Deadline()
	u.ctxs = append(u.ctxs, seen{err: ctx.Err(), deadline: deadline, bounded: bounded})

	if u.fails != nil {
		return domain.AgentUsage{}, u.fails
	}

	k := string(owner) + d.Month.String()
	r := u.rows[k]
	r.Month, r.InputTokens, r.OutputTokens = d.Month, r.InputTokens+d.InputTokens, r.OutputTokens+d.OutputTokens
	r.Dollars, r.Calls, r.CostedCalls = r.Dollars.Add(d.Dollars), r.Calls+d.Calls, r.CostedCalls+d.CostedCalls
	u.rows[k] = r

	return r, nil
}

func (u *usage) Month(_ context.Context, owner domain.UserID, month time.Time) (domain.AgentUsage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	r, ok := u.rows[string(owner)+month.String()]
	if !ok {
		return domain.AgentUsage{Month: month, Dollars: decimal.Zero}, nil
	}

	return r, nil
}

func setup(at time.Time) (*spend.Service, *usage, *time.Time) {
	u := &usage{rows: map[string]domain.AgentUsage{}}
	now := at

	return spend.New(spend.Deps{Usage: u, Logger: logging.Noop(), Now: func() time.Time { return now }}), u, &now
}

var alice = ports.SpendRun{OwnerID: "alice", WorkflowID: "wf-1", NodeID: "ask"}

func cost(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

func TestRecord(t *testing.T) {
	ctx := context.Background()
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	s, u, _ := setup(time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC))
	b := s.Run(alice)

	for _, call := range []ports.LLMUsage{
		{InputTokens: 100, OutputTokens: 20},
		{InputTokens: 50, OutputTokens: 5, Cost: cost("0.0012")},
		{InputTokens: 10, OutputTokens: 1, Cost: cost("0")}, // a free model's cost is reported too
	} {
		hold, refused, err := b.Admit(ctx, ports.LLMRequest{})
		require.NoError(t, err)
		require.Empty(t, refused, "no caps yet")
		hold.Record(ctx, call)
	}

	got, err := s.Month(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, october, got.Month)
	assert.Equal(t, int64(160), got.InputTokens)
	assert.Equal(t, int64(26), got.OutputTokens)
	assert.Equal(t, "0.0012", got.Dollars.String(), "what providers reported, nothing made up (D270)")
	assert.Equal(t, 3, got.Calls)
	assert.Equal(t, 2, got.CostedCalls)

	t.Run("once a hold, released or recorded", func(t *testing.T) {
		hold, _, _ := b.Admit(ctx, ports.LLMRequest{})
		hold.Release()
		hold.Record(ctx, ports.LLMUsage{InputTokens: 1000})

		hold, _, _ = b.Admit(ctx, ports.LLMRequest{})
		hold.Record(ctx, ports.LLMUsage{InputTokens: 1})
		hold.Record(ctx, ports.LLMUsage{InputTokens: 1000})
		hold.Release()

		got, _ := s.Month(ctx, "alice")
		assert.Equal(t, int64(161), got.InputTokens)
		assert.Equal(t, 4, got.Calls)
	})

	t.Run("a cancelled run still counts what it was billed", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		hold, _, _ := b.Admit(cancelled, ports.LLMRequest{})
		cancel()
		hold.Record(cancelled, ports.LLMUsage{OutputTokens: 7})

		last := u.ctxs[len(u.ctxs)-1]
		assert.NoError(t, last.err, "recorded on a context the run's cancelling does not reach")
		require.True(t, last.bounded, "but not for ever: a stopped workflow waits for its nodes")
		assert.WithinDuration(t, time.Now().Add(10*time.Second), last.deadline, 2*time.Second)
	})

	t.Run("a failure to record is logged, the answer standing", func(t *testing.T) {
		u.fails = errors.New("database down")
		defer func() { u.fails = nil }()

		hold, _, _ := b.Admit(ctx, ports.LLMRequest{})
		assert.NotPanics(t, func() { hold.Record(ctx, ports.LLMUsage{InputTokens: 1}) })
	})
}

func TestMonths(t *testing.T) {
	ctx := context.Background()

	s, _, now := setup(time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC))
	b := s.Run(alice)

	hold, _, _ := b.Admit(ctx, ports.LLMRequest{})
	*now = time.Date(2026, 11, 1, 0, 1, 0, 0, time.UTC) // it answers after midnight
	hold.Record(ctx, ports.LLMUsage{InputTokens: 9})

	november, _ := s.Month(ctx, "alice")
	assert.Zero(t, november.InputTokens, "a call let go in October counts in October")

	*now = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	october, _ := s.Month(ctx, "alice")
	assert.Equal(t, int64(9), october.InputTokens)

	other, _ := s.Month(ctx, "bob")
	assert.Zero(t, other.Calls, "each user's own")
}
