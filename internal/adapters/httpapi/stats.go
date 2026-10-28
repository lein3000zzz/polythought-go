package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
)

// StatsService is how the user's workflows are doing, together (D429).
type StatsService interface {
	Overview(ctx context.Context, p auth.Principal) (stats.Overview, error)
}

type runCountsDTO struct {
	Runs   int `json:"runs"`
	Failed int `json:"failed"`
} // @name RunCounts

type lastRunDTO struct {
	ID         string     `json:"id"`
	Status     string     `json:"status" enums:"running,waiting,succeeded,failed,cancelled"`
	StartedAt  time.Time  `json:"startedAt" format:"date-time"`
	FinishedAt *time.Time `json:"finishedAt,omitempty" validate:"optional" format:"date-time"`
} // @name LastRun

type runStatsDTO struct {
	// Day and Week are the runs started in the last day and the last week, by hand too.
	Day  runCountsDTO `json:"day"`
	Week runCountsDTO `json:"week"`
	// Last is the latest run; absent when none is kept.
	Last *lastRunDTO `json:"last,omitempty" validate:"optional"`
} // @name RunStats

type tradingResultDTO struct {
	// Mode is the one it trades in now, which these are about.
	Mode string `json:"mode" enums:"paper,live"`
	// Profit is as its trading page has it: on paper its worth less what it started
	// with, live what its holdings made, Realized of those sold and Unrealized of those
	// held, at their marks. Dollars.
	Profit     string `json:"profit" example:"12.4"`
	Realized   string `json:"realized"`
	Unrealized string `json:"unrealized"`
	Positions  int    `json:"positions"` // open holdings
	// Unpriced are those of them at their cost: no price is known for them now.
	Unpriced int    `json:"unpriced"`
	Volume   string `json:"volume"` // the dollars its filled orders traded
	Orders   int    `json:"orders"` // that filled
	Wins     int    `json:"wins"`   // closed holdings that made money
	Closed   int    `json:"closed"` // holdings sold out of, or settled
} // @name TradingResult

type extremeDTO struct {
	Profit string    `json:"profit" example:"18.2"`
	At     time.Time `json:"at" format:"date-time"`
} // @name PnLExtreme

type workflowStatsDTO struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
	// Trading is what it made in the mode it trades in now; absent until it has traded in it.
	Trading *tradingResultDTO `json:"trading,omitempty" validate:"optional"`
	// High and Low are its highest and lowest profit in that mode, with when, as sampled
	// and as it stands now; on paper since its account's last reset (D430). With Trading.
	High *extremeDTO `json:"high,omitempty" validate:"optional"`
	Low  *extremeDTO `json:"low,omitempty" validate:"optional"`
	Runs runStatsDTO `json:"runs"`
} // @name WorkflowStats

type statsTotalsDTO struct {
	Workflows  int    `json:"workflows"` // that have traded in the mode
	Profit     string `json:"profit"`
	Realized   string `json:"realized"`
	Unrealized string `json:"unrealized"`
	Positions  int    `json:"positions"`
	Unpriced   int    `json:"unpriced"`
	Volume     string `json:"volume"`
	Orders     int    `json:"orders"`
	Wins       int    `json:"wins"`
	Closed     int    `json:"closed"`
} // @name StatsTotals

type traderResultDTO struct {
	Mode string `json:"mode" enums:"paper,live"`
	// Trader is the wallet copied; absent for the orders copied from no one.
	Trader     string `json:"trader,omitempty" validate:"optional"`
	Label      string `json:"label,omitempty" validate:"optional"`
	Workflows  int    `json:"workflows"` // that copy them in the mode
	Profit     string `json:"profit"`
	Realized   string `json:"realized"`
	Unrealized string `json:"unrealized"`
	Positions  int    `json:"positions"`
	Volume     string `json:"volume"`
	Orders     int    `json:"orders"`
	Wins       int    `json:"wins"`
	Closed     int    `json:"closed"`
} // @name TraderResult

type workflowsOverviewDTO struct {
	Workflows []workflowStatsDTO `json:"workflows"`
	// Traders are what copying each trader came to across the workflows, in each mode,
	// the most profitable first, copied from no one last.
	Traders []traderResultDTO `json:"traders"`
	// Paper and Live add up the workflows trading in each mode, never one into the other.
	Paper statsTotalsDTO `json:"paper"`
	Live  statsTotalsDTO `json:"live"`
	// Day and Week are all their runs, started in the last day and the last week.
	Day  runCountsDTO `json:"day"`
	Week runCountsDTO `json:"week"`
	At   time.Time    `json:"at" format:"date-time"`
} // @name WorkflowsOverview

func toRunCounts(c domain.RunCounts) runCountsDTO {
	return runCountsDTO{Runs: c.Runs, Failed: c.Failed}
}

func toTotals(t stats.Totals) statsTotalsDTO {
	return statsTotalsDTO{
		Workflows: t.Workflows, Profit: t.Profit.String(), Realized: t.Realized.String(), Unrealized: t.Unrealized.String(),
		Positions: t.Positions, Unpriced: t.Unpriced, Volume: t.Volume.String(), Orders: t.Orders, Wins: t.Wins, Closed: t.Closed,
	}
}

func toOverviewDTO(o stats.Overview) workflowsOverviewDTO {
	out := workflowsOverviewDTO{
		Workflows: make([]workflowStatsDTO, len(o.Workflows)),
		Traders:   make([]traderResultDTO, len(o.Traders)),
		Paper:     toTotals(o.Paper), Live: toTotals(o.Live), Day: toRunCounts(o.Day), Week: toRunCounts(o.Week), At: o.At,
	}

	for i, t := range o.Traders {
		out.Traders[i] = traderResultDTO{
			Mode: string(t.Mode), Trader: string(t.Trader), Label: t.Label, Workflows: t.Workflows,
			Profit: t.Profit.String(), Realized: t.Realized.String(), Unrealized: t.Unrealized.String(), Positions: t.Positions,
			Volume: t.Volume.String(), Orders: t.Orders, Wins: t.Wins, Closed: t.Closed,
		}
	}

	for i, w := range o.Workflows {
		dto := workflowStatsDTO{
			ID: string(w.ID), Name: w.Name, Active: w.Active,
			Runs: runStatsDTO{Day: toRunCounts(w.Runs.Day), Week: toRunCounts(w.Runs.Week)},
		}

		if l := w.Runs.Last; l != nil {
			dto.Runs.Last = &lastRunDTO{ID: string(l.ID), Status: string(l.Status), StartedAt: l.StartedAt, FinishedAt: l.FinishedAt}
		}

		if r := w.Trading; r != nil {
			dto.Trading = &tradingResultDTO{
				Mode: string(r.Mode), Profit: r.Profit.String(), Realized: r.Realized.String(), Unrealized: r.Unrealized.String(),
				Positions: r.Positions, Unpriced: r.Unpriced, Volume: r.Volume.String(), Orders: r.Orders, Wins: r.Wins, Closed: r.Closed,
			}
		}

		dto.High, dto.Low = toExtreme(w.High), toExtreme(w.Low)
		out.Workflows[i] = dto
	}

	return out
}

func toExtreme(e *stats.Extreme) *extremeDTO {
	if e == nil {
		return nil
	}

	return &extremeDTO{Profit: e.Profit.String(), At: e.At}
}

// workflowsStats says how all the user's workflows are doing (D429): what each made
// trading, in the mode it trades in now, and how its runs went lately, with the totals
// on paper and live apart.
//
//	@Summary	How the workflows are doing
//	@ID			getWorkflowsStats
//	@Tags		workflows
//	@Success	200	{object}	workflowsOverviewDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/stats [get]
func (s *Server) workflowsStats(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	o, err := s.stats.Overview(ctx, p)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toOverviewDTO(o))
}

func (s *Server) hasStats(ctx context.Context, c *app.RequestContext) {
	if s.stats == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}
