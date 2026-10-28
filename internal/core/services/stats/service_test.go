package stats_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

var (
	now   = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "alice"}}
)

// workflows pages two at a time, as a repository would, alice's only.
type workflows []domain.Workflow

func (w workflows) List(_ context.Context, owner domain.UserID, f ports.WorkflowFilter) (ports.Page[domain.Workflow], error) {
	if owner != "alice" {
		return ports.Page[domain.Workflow]{}, nil
	}

	start := 0
	if f.Cursor == "next" {
		start = 2
	}

	end := min(start+2, len(w))
	page := ports.Page[domain.Workflow]{Items: w[start:end]}

	if end < len(w) {
		page.NextCursor = "next"
	}

	return page, nil
}

type runs struct{ day, week time.Time }

func (r *runs) RunStats(_ context.Context, _ domain.UserID, day, week time.Time) ([]domain.RunStats, error) {
	r.day, r.week = day, week

	return []domain.RunStats{
		{WorkflowID: "wf-copy", Day: domain.RunCounts{Runs: 4, Failed: 1}, Week: domain.RunCounts{Runs: 30, Failed: 2},
			Last: &domain.LastRun{ID: "ex-9", Status: domain.ExecutionFailed, StartedAt: now.Add(-5 * time.Minute)}},
		{WorkflowID: "wf-alert", Day: domain.RunCounts{Runs: 2}, Week: domain.RunCounts{Runs: 9}},
	}, nil
}

type results map[domain.WorkflowID]trading.Result

func (r results) Results(context.Context, domain.UserID) (map[domain.WorkflowID]trading.Result, error) {
	return r, nil
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestOverview(t *testing.T) {
	r := &runs{}
	svc := stats.New(
		workflows{
			{ID: "wf-copy", Name: "copy Alice", Active: true},
			{ID: "wf-alert", Name: "Alert"},
			{ID: "wf-live", Name: "live one", Active: true},
			{ID: "wf-paper", Name: "Backtest"},
		},
		r,
		results{
			"wf-copy":  {Mode: domain.ModePaper, Profit: d("12.40"), Realized: d("10"), Unrealized: d("2.40"), Positions: 3, Orders: 7, Volume: d("90")},
			"wf-paper": {Mode: domain.ModePaper, Profit: d("-2.5"), Realized: d("-2.5"), Orders: 2, Volume: d("10"), Closed: 1},
			"wf-live":  {Mode: domain.ModeLive, Profit: d("1.25"), Unrealized: d("1.25"), Positions: 1, Unpriced: 1, Orders: 1, Volume: d("5")},
		},
		stats.WithClock(func() time.Time { return now }),
	)

	o, err := svc.Overview(context.Background(), alice)
	require.NoError(t, err)

	assert.Equal(t, now.Add(-24*time.Hour), r.day)
	assert.Equal(t, now.Add(-7*24*time.Hour), r.week)

	require.Len(t, o.Workflows, 4, "both pages")

	names := make([]string, len(o.Workflows))
	for i, w := range o.Workflows {
		names[i] = w.Name
	}

	assert.Equal(t, []string{"Alert", "Backtest", "copy Alice", "live one"}, names, "by name, whatever the case")

	alert, copied := o.Workflows[0], o.Workflows[2]
	assert.Nil(t, alert.Trading, "it does not trade")
	assert.Equal(t, 2, alert.Runs.Day.Runs)
	assert.Nil(t, alert.Runs.Last)
	assert.Equal(t, domain.ExecutionFailed, copied.Runs.Last.Status)
	assert.Equal(t, "12.4", copied.Trading.Profit.String())
	assert.Equal(t, domain.WorkflowID("wf-paper"), o.Workflows[1].Runs.WorkflowID, "no runs: still its own")

	assert.Equal(t, 2, o.Paper.Workflows)
	assert.Equal(t, "9.9", o.Paper.Profit.String(), "paper alone")
	assert.Equal(t, 9, o.Paper.Orders)
	assert.Equal(t, "100", o.Paper.Volume.String())
	assert.Equal(t, 1, o.Live.Workflows)
	assert.Equal(t, "1.25", o.Live.Profit.String(), "live never added to paper")
	assert.Equal(t, 1, o.Live.Unpriced)

	assert.Equal(t, domain.RunCounts{Runs: 6, Failed: 1}, o.Day)
	assert.Equal(t, domain.RunCounts{Runs: 39, Failed: 2}, o.Week)
	assert.Equal(t, now, o.At)

	empty, err := svc.Overview(context.Background(), auth.Principal{User: domain.User{ID: "bob"}})
	require.NoError(t, err)
	assert.Empty(t, empty.Workflows, "only the user's own")
}
