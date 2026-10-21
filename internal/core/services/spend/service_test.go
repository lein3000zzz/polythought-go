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

// usage is a repository in memory, its months added to and claimed as Postgres does; it
// notes how each add's context was as it came.
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

func key(owner domain.UserID, month time.Time) string { return string(owner) + month.String() }

func (u *usage) Add(ctx context.Context, owner domain.UserID, d domain.AgentUsage, _ time.Time) (domain.AgentUsage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	deadline, bounded := ctx.Deadline()
	u.ctxs = append(u.ctxs, seen{err: ctx.Err(), deadline: deadline, bounded: bounded})

	if u.fails != nil {
		return domain.AgentUsage{}, u.fails
	}

	k := key(owner, d.Month)
	r := u.rows[k]
	r.Month, r.InputTokens, r.OutputTokens = d.Month, r.InputTokens+d.InputTokens, r.OutputTokens+d.OutputTokens
	r.Dollars, r.Calls, r.CostedCalls = r.Dollars.Add(d.Dollars), r.Calls+d.Calls, r.CostedCalls+d.CostedCalls
	u.rows[k] = r

	return r, nil
}

func (u *usage) Month(_ context.Context, owner domain.UserID, month time.Time) (domain.AgentUsage, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	r, ok := u.rows[key(owner, month)]
	if !ok {
		return domain.AgentUsage{Month: month, Dollars: decimal.Zero}, nil
	}

	return r, nil
}

func (u *usage) ClaimNotice(_ context.Context, owner domain.UserID, month time.Time, n domain.SpendNotice, was, caps string, _ time.Time) (bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	k := key(owner, month)
	r := u.rows[k]
	r.Month = month

	at := &r.Warned
	if n == domain.NoticeCapped {
		at = &r.Capped
	}

	if *at != was {
		return false, nil
	}

	*at = caps
	u.rows[k] = r

	return true, nil
}

// users are a user in memory, with their caps and choices.
type users struct {
	mu   sync.Mutex
	user domain.User
}

func (u *users) ByID(_ context.Context, id domain.UserID) (domain.User, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	if id != u.user.ID {
		return domain.User{}, domain.ErrNotFound
	}

	return u.user, nil
}

func (u *users) SetKillStopsAgents(_ context.Context, _ domain.UserID, on bool) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.user.KillStopsAgents = on

	return nil
}

func (u *users) set(change func(*domain.User)) {
	u.mu.Lock()
	defer u.mu.Unlock()

	change(&u.user)
}

// notices are what the owner was told.
type notices struct {
	mu   sync.Mutex
	sent []domain.SpendAlert
}

func (n *notices) AgentSpendNoticed(_ domain.UserID, a domain.SpendAlert) {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.sent = append(n.sent, a)
}

func (n *notices) all() []domain.SpendAlert {
	n.mu.Lock()
	defer n.mu.Unlock()

	return append([]domain.SpendAlert(nil), n.sent...)
}

type fixture struct {
	s        *spend.Service
	usage    *usage
	users    *users
	notices  *notices
	now      *time.Time
	instance *domain.AgentCaps
}

func setup(at time.Time) fixture {
	f := fixture{
		usage: &usage{rows: map[string]domain.AgentUsage{}}, users: &users{user: domain.User{ID: "alice"}},
		notices: &notices{}, now: &at, instance: &domain.AgentCaps{},
	}

	f.s = spend.New(spend.Deps{
		Usage: f.usage, Users: f.users, Notices: f.notices, Logger: logging.Noop(),
		Caps: func() domain.AgentCaps { return *f.instance },
		Now:  func() time.Time { return *f.now },
	})

	return f
}

var alice = ports.SpendRun{OwnerID: "alice", WorkflowID: "wf-1", NodeID: "ask"}

func cost(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}

func n(v int64) *int64 { return &v }

// asking is a request that may write up to out tokens, its input a few bytes.
func asking(out int64) ports.LLMRequest {
	return ports.LLMRequest{MaxTokens: out, Messages: []ports.LLMMessage{{Role: ports.LLMUser, Text: "hi"}}}
}

func admit(t *testing.T, b ports.SpendBudget, req ports.LLMRequest) ports.SpendHold {
	t.Helper()

	h, refused, err := b.Admit(context.Background(), req)
	require.NoError(t, err)
	require.Empty(t, refused)

	return h
}

func refused(t *testing.T, b ports.SpendBudget, req ports.LLMRequest) string {
	t.Helper()

	h, why, err := b.Admit(context.Background(), req)
	require.NoError(t, err)
	require.Nil(t, h)
	require.NotEmpty(t, why, "refused")

	return why
}

var midOctober = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func TestRecord(t *testing.T) {
	ctx := context.Background()
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	f := setup(midOctober)
	b := f.s.Run(alice)

	for _, call := range []ports.LLMUsage{
		{InputTokens: 100, OutputTokens: 20},
		{InputTokens: 50, OutputTokens: 5, Cost: cost("0.0012")},
		{InputTokens: 10, OutputTokens: 1, Cost: cost("0")}, // a free model's cost is reported too
	} {
		admit(t, b, asking(100)).Record(ctx, call)
	}

	got, err := f.s.Spend(ctx, "alice")
	require.NoError(t, err)
	assert.Equal(t, october, got.Usage.Month)
	assert.Equal(t, october.AddDate(0, 1, 0), got.Resets)
	assert.Equal(t, int64(160), got.Usage.InputTokens)
	assert.Equal(t, int64(26), got.Usage.OutputTokens)
	assert.Equal(t, "0.0012", got.Usage.Dollars.String(), "what providers reported, nothing made up (D270)")
	assert.Equal(t, 3, got.Usage.Calls)
	assert.Equal(t, 2, got.Usage.CostedCalls)

	t.Run("once a hold, released or recorded", func(t *testing.T) {
		h := admit(t, b, asking(100))
		h.Release()
		h.Record(ctx, ports.LLMUsage{InputTokens: 1000})

		h = admit(t, b, asking(100))
		h.Record(ctx, ports.LLMUsage{InputTokens: 1})
		h.Record(ctx, ports.LLMUsage{InputTokens: 1000})
		h.Release()

		got, _ := f.s.Spend(ctx, "alice")
		assert.Equal(t, int64(161), got.Usage.InputTokens)
		assert.Equal(t, 4, got.Usage.Calls)
	})

	t.Run("a cancelled run still counts what it was billed", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		h, _, _ := b.Admit(cancelled, asking(100))
		cancel()
		h.Record(cancelled, ports.LLMUsage{OutputTokens: 7})

		last := f.usage.ctxs[len(f.usage.ctxs)-1]
		assert.NoError(t, last.err, "recorded on a context the run's cancelling does not reach")
		require.True(t, last.bounded, "but not for ever: a stopped workflow waits for its nodes")
		assert.WithinDuration(t, time.Now().Add(10*time.Second), last.deadline, 2*time.Second)
	})

	t.Run("a failure to record is logged, the answer standing, the hold let go", func(t *testing.T) {
		f.usage.fails = errors.New("database down")
		admit(t, b, asking(100)).Record(ctx, ports.LLMUsage{InputTokens: 1})
		f.usage.fails = nil
	})

	t.Run("the choice of the kill switch", func(t *testing.T) {
		require.NoError(t, f.s.SetKillStopsAgents(ctx, "alice", true))
		got, _ := f.s.Spend(ctx, "alice")
		assert.True(t, got.KillStopsAgents)
	})
}

func TestMonths(t *testing.T) {
	ctx := context.Background()

	f := setup(time.Date(2026, 10, 31, 23, 59, 0, 0, time.UTC))
	b := f.s.Run(alice)

	h := admit(t, b, asking(100))
	*f.now = time.Date(2026, 11, 1, 0, 1, 0, 0, time.UTC) // it answers after midnight
	h.Record(ctx, ports.LLMUsage{InputTokens: 9})

	november, _ := f.s.Spend(ctx, "alice")
	assert.Zero(t, november.Usage.InputTokens, "a call let go in October counts in October")

	*f.now = time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	october, _ := f.s.Spend(ctx, "alice")
	assert.Equal(t, int64(9), october.Usage.InputTokens)
}

func TestMonthlyTokenCaps(t *testing.T) {
	ctx := context.Background()
	f := setup(midOctober)
	*f.instance = domain.AgentCaps{TotalTokens: n(1000)}
	b := f.s.Run(alice)

	admit(t, b, asking(500)).Record(ctx, ports.LLMUsage{InputTokens: 300, OutputTokens: 400})

	why := refused(t, b, asking(400))
	assert.Equal(t, "this call could take up to 401 tokens, past the 1,000 a month your agents may use: 700 used or held", why)

	require.Len(t, f.notices.all(), 1, "the cap notice, on the first refusal")
	assert.Equal(t, domain.SpendAlert{
		Notice: domain.NoticeCapped, What: "tokens", Cap: "1,000", Used: "700", Resets: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}, f.notices.all()[0])

	refused(t, b, asking(400))
	assert.Len(t, f.notices.all(), 1, "once a month")

	admit(t, b, asking(250)).Release()

	t.Run("an admin's own cap over the instance's, and raised, a notice again", func(t *testing.T) {
		f.users.set(func(u *domain.User) { u.AgentCaps.TotalTokens = n(800) })
		refused(t, b, asking(250))
		assert.Len(t, f.notices.all(), 1, "lowered: no new notice")

		f.users.set(func(u *domain.User) { u.AgentCaps.TotalTokens = n(1100) })
		admit(t, b, asking(350)).Release()
		refused(t, b, asking(450))
		assert.Len(t, f.notices.all(), 2, "raised since: told again")

		f.users.set(func(u *domain.User) { u.AgentCaps.TotalTokens = n(0) })
		admit(t, b, asking(1_000_000)).Release()
	})

	t.Run("input and output each", func(t *testing.T) {
		f := setup(midOctober)
		*f.instance = domain.AgentCaps{InputTokens: n(100), OutputTokens: n(50)}
		b := f.s.Run(alice)

		why := refused(t, b, asking(60))
		assert.Contains(t, why, "60 output tokens, past the 50", "its Max tokens")

		big := asking(10)
		big.System = string(make([]byte, 600))
		assert.Contains(t, refused(t, b, big), "input tokens, past the 100")
	})
}

func TestHolds(t *testing.T) {
	ctx := context.Background()
	f := setup(midOctober)
	*f.instance = domain.AgentCaps{TotalTokens: n(1000)}

	first := admit(t, f.s.Run(alice), asking(600))

	other := f.s.Run(ports.SpendRun{OwnerID: "alice", NodeID: "other"})
	assert.Contains(t, refused(t, other, asking(600)), "601 used or held", "held by a call in flight, any run's")

	first.Record(ctx, ports.LLMUsage{InputTokens: 50, OutputTokens: 50})
	admit(t, other, asking(600)).Release()

	t.Run("calls at once never hold past the cap together", func(t *testing.T) {
		f := setup(midOctober)
		*f.instance = domain.AgentCaps{TotalTokens: n(1000)}
		b := f.s.Run(alice)

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			holds   []ports.SpendHold
			refusal int
		)

		for range 10 {
			wg.Go(func() {
				h, why, err := b.Admit(ctx, asking(199))
				assert.NoError(t, err)

				mu.Lock()
				defer mu.Unlock()

				if why != "" {
					refusal++
				} else {
					holds = append(holds, h)
				}
			})
		}

		wg.Wait()
		assert.Len(t, holds, 5, "five of 200 fit 1,000")
		assert.Equal(t, 5, refusal)
	})
}

func TestDollarCaps(t *testing.T) {
	ctx := context.Background()
	f := setup(midOctober)
	*f.instance = domain.AgentCaps{Dollars: cost("1.00")}
	b := f.s.Run(alice)

	admit(t, b, asking(100)).Record(ctx, ports.LLMUsage{InputTokens: 10, Cost: cost("0.99")})
	admit(t, b, asking(100_000)).Record(ctx, ports.LLMUsage{InputTokens: 10, Cost: cost("0.02")})

	why := refused(t, b, asking(100))
	assert.Equal(t, "$1.01 spent, your agents' cap this month", why, "known only once a call ends: one may pass it (D270)")

	t.Run("calls with no reported cost count against tokens only", func(t *testing.T) {
		f := setup(midOctober)
		*f.instance = domain.AgentCaps{Dollars: cost("1.00")}
		b := f.s.Run(alice)

		for range 3 {
			admit(t, b, asking(100)).Record(ctx, ports.LLMUsage{InputTokens: 1000, OutputTokens: 1000})
		}
	})
}

func TestRunCaps(t *testing.T) {
	ctx := context.Background()
	f := setup(midOctober)

	b := f.s.Run(ports.SpendRun{OwnerID: "alice", NodeID: "ask", Caps: domain.AgentCaps{TotalTokens: n(500), Dollars: cost("0.10")}})
	admit(t, b, asking(200)).Record(ctx, ports.LLMUsage{InputTokens: 100, OutputTokens: 200})

	assert.Equal(t, "this call could take up to 201 tokens, past the 500 this node may use in one run: 300 used or held",
		refused(t, b, asking(200)))
	assert.Empty(t, f.notices.all(), "the item's reason says it: no notice")

	admit(t, f.s.Run(alice), asking(200)).Release()

	admit(t, b, asking(100)).Record(ctx, ports.LLMUsage{Cost: cost("0.10")})
	assert.Equal(t, "$0.10 spent, this node's cap for one run", refused(t, b, asking(1)))
}

func TestKillSwitch(t *testing.T) {
	f := setup(midOctober)
	f.users.set(func(u *domain.User) { u.LiveStoppedAt = midOctober })

	b := f.s.Run(alice)
	admit(t, b, asking(10)).Release()

	f.users.set(func(u *domain.User) { u.KillStopsAgents = true })
	assert.Equal(t, "your kill switch is on, and you chose that it stops your agents too", refused(t, b, asking(10)))

	f.users.set(func(u *domain.User) { u.LiveStoppedAt = time.Time{} })
	admit(t, b, asking(10)).Release()
}

func TestWarnings(t *testing.T) {
	ctx := context.Background()
	f := setup(midOctober)
	*f.instance = domain.AgentCaps{TotalTokens: n(1000), Dollars: cost("20")}
	b := f.s.Run(alice)

	admit(t, b, asking(10)).Record(ctx, ports.LLMUsage{InputTokens: 700})
	assert.Empty(t, f.notices.all())

	admit(t, b, asking(10)).Record(ctx, ports.LLMUsage{InputTokens: 100})
	require.Len(t, f.notices.all(), 1, "past 80%")
	assert.Equal(t, domain.SpendAlert{
		Notice: domain.NoticeWarned, What: "tokens", Cap: "1,000", Used: "800", Resets: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
	}, f.notices.all()[0])

	admit(t, b, asking(10)).Record(ctx, ports.LLMUsage{InputTokens: 50, Cost: cost("17")})
	assert.Len(t, f.notices.all(), 1, "once a month, whichever cap crossed first")

	f.users.set(func(u *domain.User) { u.AgentCaps.TotalTokens = n(2000) })
	admit(t, b, asking(10)).Record(ctx, ports.LLMUsage{InputTokens: 10})
	require.Len(t, f.notices.all(), 2, "raised, then past 80% of another: told again")
	assert.Equal(t, "dollars", f.notices.all()[1].What)
	assert.Equal(t, "$17.00", f.notices.all()[1].Used)
}
