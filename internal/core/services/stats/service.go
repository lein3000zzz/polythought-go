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
	Results(ctx context.Context, owner domain.UserID) (map[domain.WorkflowID]trading.Result, error)
}

// Workflow is how one workflow is doing.
type Workflow struct {
	ID     domain.WorkflowID
	Name   string
	Active bool
	// Trading is what it made in the mode it trades in now; nil where it has not traded
	// in it.
	Trading *trading.Result
	Runs    domain.RunStats
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
	// Day and Week are all their runs, started in the last day and the last week.
	Day, Week domain.RunCounts
	At        time.Time
}

type Service struct {
	workflows Workflows
	runs      Runs
	trading   Trading
	now       func() time.Time
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(workflows Workflows, runs Runs, t Trading, opts ...Option) *Service {
	s := &Service{workflows: workflows, runs: runs, trading: t, now: func() time.Time { return time.Now().UTC() }}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Overview is how the principal's workflows are doing.
func (s *Service) Overview(ctx context.Context, p auth.Principal) (Overview, error) {
	owner, now := p.User.ID, s.now()

	var workflows []domain.Workflow

	for f := (ports.WorkflowFilter{PageFilter: ports.PageFilter{Limit: 100}}); ; {
		page, err := s.workflows.List(ctx, owner, f)
		if err != nil {
			return Overview{}, err
		}

		workflows = append(workflows, page.Items...)

		if page.NextCursor == "" {
			break
		}

		f.Cursor = page.NextCursor
	}

	runs, err := s.runs.RunStats(ctx, owner, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil {
		return Overview{}, err
	}

	results, err := s.trading.Results(ctx, owner)
	if err != nil {
		return Overview{}, err
	}

	runsOf := make(map[domain.WorkflowID]domain.RunStats, len(runs))
	for _, r := range runs {
		runsOf[r.WorkflowID] = r
	}

	out := Overview{Workflows: make([]Workflow, 0, len(workflows)), At: now}

	for _, w := range workflows {
		st := Workflow{ID: w.ID, Name: w.Name, Active: w.Active, Runs: runsOf[w.ID]}
		st.Runs.WorkflowID = w.ID

		if r, ok := results[w.ID]; ok {
			st.Trading = &r

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
