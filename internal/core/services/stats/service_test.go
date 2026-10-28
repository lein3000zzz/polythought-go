package stats_test

import (
	"context"
	"errors"
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

func (r results) Results(context.Context, domain.UserID) (trading.Results, error) {
	return trading.Results{Workflows: r, Traders: []trading.TraderResult{{Mode: domain.ModePaper, Trader: "0xa1", Label: "Alice", Workflows: 2}}}, nil
}

// extremes keeps what is recorded as it comes, and gives alice what it is told she has.
type extremes struct {
	kept     []domain.PnLExtremes
	recorded map[domain.WorkflowID]domain.PnLSample
}

func (e *extremes) Record(_ context.Context, samples []domain.PnLSample) error {
	for _, s := range samples {
		e.recorded[s.WorkflowID] = s
	}

	return nil
}

func (e *extremes) Owner(_ context.Context, owner domain.UserID) ([]domain.PnLExtremes, error) {
	if owner != "alice" {
		return nil, nil
	}

	return e.kept, nil
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var (
	started = now.Add(-30 * 24 * time.Hour)
	reset   = now.Add(-24 * time.Hour)
)

func TestOverview(t *testing.T) {
	r := &runs{}
	svc := stats.New(stats.Deps{
		Workflows: workflows{
			{ID: "wf-copy", Name: "copy Alice", Active: true},
			{ID: "wf-alert", Name: "Alert"},
			{ID: "wf-live", Name: "live one", Active: true},
			{ID: "wf-paper", Name: "Backtest"},
		},
		Runs: r,
		Trading: results{
			"wf-copy":  {Mode: domain.ModePaper, Since: started, Profit: d("12.40"), Realized: d("10"), Unrealized: d("2.40"), Positions: 3, Orders: 7, Volume: d("90")},
			"wf-paper": {Mode: domain.ModePaper, Since: reset, Profit: d("-2.5"), Realized: d("-2.5"), Orders: 2, Volume: d("10"), Closed: 1},
			"wf-live":  {Mode: domain.ModeLive, Profit: d("1.25"), Unrealized: d("1.25"), Positions: 1, Unpriced: 1, Orders: 1, Volume: d("5")},
		},
		Extremes: &extremes{kept: []domain.PnLExtremes{
			{WorkflowID: "wf-copy", Mode: domain.ModePaper, Since: started, High: d("18.2"), HighAt: now.Add(-time.Hour), Low: d("13"), LowAt: now.Add(-2 * time.Hour)},
			{WorkflowID: "wf-paper", Mode: domain.ModePaper, Since: started, High: d("40"), HighAt: now.Add(-48 * time.Hour), Low: d("-9"), LowAt: now.Add(-48 * time.Hour)},
			{WorkflowID: "wf-live", Mode: domain.ModePaper, Since: started, High: d("7"), HighAt: now.Add(-time.Hour), Low: d("-1"), LowAt: now.Add(-time.Hour)},
		}},
		Now: func() time.Time { return now },
	})

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

	assert.Nil(t, alert.High, "nothing traded, nothing high")
	assert.Equal(t, &stats.Extreme{Profit: d("18.2"), At: now.Add(-time.Hour)}, copied.High, "kept, higher than now")
	assert.Equal(t, &stats.Extreme{Profit: d("12.40"), At: now}, copied.Low, "now lower than kept")

	backtest, live := o.Workflows[1], o.Workflows[3]
	assert.Equal(t, &stats.Extreme{Profit: d("-2.5"), At: now}, backtest.High, "kept before the reset: now alone")
	assert.Equal(t, &stats.Extreme{Profit: d("-2.5"), At: now}, backtest.Low)
	assert.Equal(t, &stats.Extreme{Profit: d("1.25"), At: now}, live.High, "kept on paper, trading live now")

	assert.Equal(t, 2, o.Paper.Workflows)
	assert.Equal(t, "9.9", o.Paper.Profit.String(), "paper alone")
	assert.Equal(t, 9, o.Paper.Orders)
	assert.Equal(t, "100", o.Paper.Volume.String())
	assert.Equal(t, 1, o.Live.Workflows)
	assert.Equal(t, "1.25", o.Live.Profit.String(), "live never added to paper")
	assert.Equal(t, 1, o.Live.Unpriced)

	require.Len(t, o.Traders, 1, "as the trading service sums them")
	assert.Equal(t, "Alice", o.Traders[0].Label)

	assert.Equal(t, domain.RunCounts{Runs: 6, Failed: 1}, o.Day)
	assert.Equal(t, domain.RunCounts{Runs: 39, Failed: 2}, o.Week)
	assert.Equal(t, now, o.At)

	empty, err := svc.Overview(context.Background(), auth.Principal{User: domain.User{ID: "bob"}})
	require.NoError(t, err)
	assert.Empty(t, empty.Workflows, "only the user's own")
}

// byOwner gives each user's results, failing for those it has none for.
type byOwner map[domain.UserID]results

func (b byOwner) Results(_ context.Context, owner domain.UserID) (trading.Results, error) {
	r, ok := b[owner]
	if !ok {
		return trading.Results{}, errors.New("ledger down")
	}

	return trading.Results{Workflows: r}, nil
}

type owners []domain.UserID

func (o owners) TradingOwners(context.Context) ([]domain.UserID, error) { return o, nil }

func TestSampleEvery(t *testing.T) {
	kept := &extremes{recorded: map[domain.WorkflowID]domain.PnLSample{}}
	svc := stats.New(stats.Deps{
		Trading: byOwner{
			"alice": {
				"wf-copy": {Mode: domain.ModePaper, Since: reset, Profit: d("12.4")},
				"wf-held": {Mode: domain.ModeLive, Profit: d("3"), Positions: 2, Unpriced: 1},
			},
			"carol": {"wf-carol": {Mode: domain.ModeLive, Profit: d("-1")}},
		},
		Extremes: kept,
		Owners:   owners{"alice", "bob", "carol"},
		Now:      func() time.Time { return now },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	waits := 0
	svc.SampleEvery(ctx, func() time.Duration {
		waits++
		cancel()

		return time.Hour
	})

	assert.Equal(t, 1, waits, "sampled at once, then waited")
	assert.Equal(t, map[domain.WorkflowID]domain.PnLSample{
		"wf-copy":  {WorkflowID: "wf-copy", Mode: domain.ModePaper, Since: reset, Profit: d("12.4"), At: now},
		"wf-carol": {WorkflowID: "wf-carol", Mode: domain.ModeLive, Profit: d("-1"), At: now},
	}, kept.recorded, "bob's failing spares carol; a position at its cost is no sample")
}
