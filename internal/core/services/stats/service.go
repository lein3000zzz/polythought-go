// Package stats is how a user's workflows are doing, together (D429): what each made
// trading, in the mode it trades in now, and how its runs went lately. For the workflow
// cards, the dashboard and agents.
package stats

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// Workflows lists a user's workflows: the workflow repository.
type Workflows interface {
	List(ctx context.Context, owner domain.UserID, f ports.WorkflowFilter) (ports.Page[domain.Workflow], error)
}

// Runs counts their runs: the execution repository.
type Runs interface {
	RunStats(ctx context.Context, owner domain.UserID, day, week time.Time) ([]domain.RunStats, error)
}

// Trading is what they made: the trading service.
type Trading interface {
	Results(ctx context.Context, owner domain.UserID) (trading.Results, error)
}

// Owners are the users who have traded, whose workflows the sampling looks at: the ledger.
type Owners interface {
	TradingOwners(ctx context.Context) ([]domain.UserID, error)
}

// Extreme is a highest or a lowest profit and loss, with when (D430).
type Extreme struct {
	Profit decimal.Decimal
	At     time.Time
}

// Workflow is how one workflow is doing.
type Workflow struct {
	ID     domain.WorkflowID
	Name   string
	Active bool
	// Trading is what it made in the mode it trades in now; nil where it has not traded
	// in it. High and Low are its highest and lowest since its results started, as the
	// sampling found them and as it stands now; with Trading alone.
	Trading   *trading.Result
	High, Low *Extreme
	Runs      domain.RunStats
}

// Totals add up the workflows trading in one mode: paper and live are never added
// together.
type Totals struct {
	Workflows  int
	Profit     decimal.Decimal
	Realized   decimal.Decimal
	Unrealized decimal.Decimal
	Positions  int
	Unpriced   int
	Volume     decimal.Decimal
	Orders     int
	Wins       int
	Closed     int
}

// Overview is how all of a user's workflows are doing.
type Overview struct {
	Workflows []Workflow // by name
	Paper     Totals
	Live      Totals
	// Traders are what copying each trader came to, across the workflows, in each mode.
	Traders []trading.TraderResult
	// Day and Week are all their runs, started in the last day and the last week.
	Day, Week domain.RunCounts
	At        time.Time
}

// Deps are what the stats come from. Without Extremes and Owners, no workflow has a
// highest or a lowest, and nothing is sampled.
type Deps struct {
	Workflows Workflows
	Runs      Runs
	Trading   Trading
	Extremes  ports.PnLExtremes
	Owners    Owners
	Logger    logging.Logger
	Now       func() time.Time // nil: the clock
}

type Service struct {
	d Deps
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}

	if d.Logger == nil {
		d.Logger = logging.Noop()
	}

	d.Logger = d.Logger.With("component", "stats")

	return &Service{d: d}
}

// Overview is how the principal's workflows are doing.
func (s *Service) Overview(ctx context.Context, p auth.Principal) (Overview, error) {
	owner, now := p.User.ID, s.d.Now()

	var workflows []domain.Workflow

	for f := (ports.WorkflowFilter{PageFilter: ports.PageFilter{Limit: 100}}); ; {
		page, err := s.d.Workflows.List(ctx, owner, f)
		if err != nil {
			return Overview{}, err
		}

		workflows = append(workflows, page.Items...)

		if page.NextCursor == "" {
			break
		}

		f.Cursor = page.NextCursor
	}

	runs, err := s.d.Runs.RunStats(ctx, owner, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		return Overview{}, err
	}

	results, err := s.d.Trading.Results(ctx, owner)
	if err != nil {
		return Overview{}, err
	}

	kept := map[domain.WorkflowID]domain.PnLExtremes{}

	if s.d.Extremes != nil {
		all, err := s.d.Extremes.Owner(ctx, owner)
		if err != nil {
			return Overview{}, err
		}

		for _, e := range all {
			if r, ok := results.Workflows[e.WorkflowID]; ok && r.Mode == e.Mode {
				kept[e.WorkflowID] = e
			}
		}
	}

	runsOf := make(map[domain.WorkflowID]domain.RunStats, len(runs))
	for _, r := range runs {
		runsOf[r.WorkflowID] = r
	}

	out := Overview{Workflows: make([]Workflow, 0, len(workflows)), Traders: results.Traders, At: now}

	for _, w := range workflows {
		st := Workflow{ID: w.ID, Name: w.Name, Active: w.Active, Runs: runsOf[w.ID]}
		st.Runs.WorkflowID = w.ID

		if r, ok := results.Workflows[w.ID]; ok {
			st.Trading = &r

			if s.d.Extremes != nil {
				e, found := kept[w.ID]
				st.High, st.Low = extremes(e, found, r, now)
			}

			switch r.Mode {
			case domain.ModePaper:
				add(&out.Paper, r)
			case domain.ModeLive:
				add(&out.Live, r)
			}
		}

		out.Day.Runs, out.Day.Failed = out.Day.Runs+st.Runs.Day.Runs, out.Day.Failed+st.Runs.Day.Failed
		out.Week.Runs, out.Week.Failed = out.Week.Runs+st.Runs.Week.Runs, out.Week.Failed+st.Runs.Week.Failed
		out.Workflows = append(out.Workflows, st)
	}

	slices.SortFunc(out.Workflows, func(a, b Workflow) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(string(a.ID), string(b.ID)))
	})

	return out, nil
}

func add(t *Totals, r trading.Result) {
	t.Workflows++
	t.Profit, t.Realized, t.Unrealized = t.Profit.Add(r.Profit), t.Realized.Add(r.Realized), t.Unrealized.Add(r.Unrealized)
	t.Volume = t.Volume.Add(r.Volume)
	t.Positions += r.Positions
	t.Unpriced += r.Unpriced
	t.Orders += r.Orders
	t.Wins += r.Wins
	t.Closed += r.Closed
}

// extremes are a workflow's highest and lowest, what was kept and where it stands now
// counting together; those kept counting from another start, as before a paper reset, do
// not.
func extremes(e domain.PnLExtremes, kept bool, r trading.Result, now time.Time) (high, low *Extreme) {
	high, low = &Extreme{Profit: r.Profit, At: now}, &Extreme{Profit: r.Profit, At: now}

	if !kept || !e.Since.Equal(r.Since) {
		return high, low
	}

	if e.High.GreaterThanOrEqual(r.Profit) {
		high = &Extreme{Profit: e.High, At: e.HighAt}
	}

	if e.Low.LessThanOrEqual(r.Profit) {
		low = &Extreme{Profit: e.Low, At: e.LowAt}
	}

	return high, low
}

// SampleEvery samples the profit and loss of every workflow that trades, keeping each its
// highest and lowest (D430): at once, then after each wait every gives, asked again each
// time, until ctx is done.
func (s *Service) SampleEvery(ctx context.Context, every func() time.Duration) {
	if s.d.Extremes == nil || s.d.Owners == nil {
		return
	}

	for {
		s.sample(ctx)

		select {
		case <-ctx.Done():
			return
		case <-time.After(every()):
		}
	}
}

func (s *Service) sample(ctx context.Context) {
	owners, err := s.d.Owners.TradingOwners(ctx)
	if err != nil {
		s.d.Logger.Warn(ctx, "P&L not sampled", logging.Err(err))
		return
	}

	for _, owner := range owners {
		if ctx.Err() != nil {
			return
		}

		results, err := s.d.Trading.Results(ctx, owner)
		if err != nil {
			s.d.Logger.Warn(ctx, "P&L not sampled", "user_id", owner, logging.Err(err))
			continue
		}

		now := s.d.Now()
		samples := make([]domain.PnLSample, 0, len(results.Workflows))

		for id, r := range results.Workflows {
			// Positions at their cost, no price known now, would make a highest or a
			// lowest the market never had.
			if r.Unpriced > 0 {
				continue
			}

			samples = append(samples, domain.PnLSample{WorkflowID: id, Mode: r.Mode, Since: r.Since, Profit: r.Profit, At: now})
		}

		if err := s.d.Extremes.Record(ctx, samples); err != nil && ctx.Err() == nil {
			s.d.Logger.Warn(ctx, "P&L not kept", "user_id", owner, logging.Err(err))
		}
	}
}
