package mcpserver

import (
	"context"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
)

// The tool that says how the user's workflows are doing, together (D429), as the workflow
// cards and the dashboard do.

// Stats is how the user's workflows are doing: the stats service.
type Stats interface {
	Overview(ctx context.Context, p auth.Principal) (stats.Overview, error)
}

type statsIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"one workflow's ID, for its alone"`
	page
}

type runCountsOut struct {
	Runs   int `json:"runs"`
	Failed int `json:"failed"`
}

type lastRunOut struct {
	ID         string     `json:"id" jsonschema:"the execution's ID, as list_executions gives it"`
	Status     string     `json:"status" jsonschema:"running, waiting, succeeded, failed or cancelled"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

type resultOut struct {
	Mode       string `json:"mode" jsonschema:"paper or live: the mode it trades in now, which these are about"`
	Profit     string `json:"profit" jsonschema:"dollars: on paper its worth less what it started with, live what its holdings made, as get_trading says"`
	Realized   string `json:"realized" jsonschema:"of the holdings it sold or that settled"`
	Unrealized string `json:"unrealized" jsonschema:"of those it holds, at the market's prices now"`
	Positions  int    `json:"positions" jsonschema:"holdings open"`
	Unpriced   int    `json:"unpriced,omitempty" jsonschema:"of them, at their cost: no price is known for them now"`
	Volume     string `json:"volume" jsonschema:"the dollars its filled orders traded"`
	Orders     int    `json:"orders" jsonschema:"that filled"`
	Wins       int    `json:"wins" jsonschema:"closed holdings that made money"`
	Closed     int    `json:"closed" jsonschema:"holdings sold out of, or settled"`
}

type workflowStatsOut struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Active  bool         `json:"active"`
	Trading *resultOut   `json:"trading,omitempty" jsonschema:"what it made; absent until it traded in the mode it trades in now"`
	Day     runCountsOut `json:"runsDay" jsonschema:"its runs started in the last day, by hand too, and of them those that failed"`
	Week    runCountsOut `json:"runsWeek" jsonschema:"in the last week"`
	Last    *lastRunOut  `json:"lastRun,omitempty" jsonschema:"its latest run; absent when none is kept"`
}

type totalsOut struct {
	Workflows int    `json:"workflows" jsonschema:"that have traded in the mode"`
	Profit    string `json:"profit"`
	Realized  string `json:"realized"`
	Positions int    `json:"positions"`
	Volume    string `json:"volume"`
	Orders    int    `json:"orders"`
	Wins      int    `json:"wins"`
	Closed    int    `json:"closed"`
}

// tradersShown bounds the traders a result gives: the most profitable.
const tradersShown = 50

type traderStatsOut struct {
	Mode      string    `json:"mode" jsonschema:"paper or live"`
	Trader    string    `json:"trader,omitempty" jsonschema:"the wallet copied; absent for the orders copied from no one"`
	Workflows int       `json:"workflows" jsonschema:"that copy them in the mode"`
	Profit    string    `json:"profit" jsonschema:"dollars, realized and unrealized together"`
	Realized  string    `json:"realized"`
	Positions int       `json:"positions"`
	Volume    string    `json:"volume"`
	Orders    int       `json:"orders"`
	Wins      int       `json:"wins"`
	Closed    int       `json:"closed"`
	Untrusted Untrusted `json:"untrusted,omitempty" jsonschema:"label: the name the trader goes by, as the workflow took it"`
}

type statsOut struct {
	Workflows []workflowStatsOut `json:"workflows"`
	Traders   []traderStatsOut   `json:"traders,omitempty" jsonschema:"on the first page: what copying each trader came to across the workflows, the 50 most profitable first"`
	Notice    string             `json:"notice,omitempty"`
	Next      string             `json:"next,omitempty" jsonschema:"the cursor for the page after; absent at the end"`
	Paper     totalsOut          `json:"paper" jsonschema:"all the workflows trading on paper; never added to live"`
	Live      totalsOut          `json:"live" jsonschema:"all those trading live"`
	Day       runCountsOut       `json:"runsDay" jsonschema:"all their runs in the last day"`
	Week      runCountsOut       `json:"runsWeek"`
	At        time.Time          `json:"at"`
}

func (s *Server) addStatsTools(srv *mcp.Server) {
	add(s, srv, Tool[statsIn, statsOut]{
		Name: "get_workflow_stats", Title: "Get how the workflows are doing", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "How the user's workflows are doing, as the Workflows page and its dashboard show it: for each, what it " +
			"made in the mode it trades in now, its profit realized and unrealized, open positions, volume and wins, and " +
			"its runs and failures in the last day and week, with its latest run. Paper and live totals come apart, " +
			"never added together. The first page also gives what copying each trader came to across the workflows. " +
			"A page of workflows at a time, by name; or one workflow by its ID. Traders' labels are under untrusted.",
		Handle: s.getWorkflowStats,
	})
}

func (s *Server) getWorkflowStats(ctx context.Context, p auth.Principal, in statsIn) (statsOut, error) {
	o, err := s.stats.Overview(ctx, p)
	if err != nil {
		return statsOut{}, err
	}

	out := statsOut{
		Workflows: []workflowStatsOut{},
		Paper:     totalsOf(o.Paper), Live: totalsOf(o.Live),
		Day: runCountsOut(o.Day), Week: runCountsOut(o.Week), At: o.At,
	}

	list := o.Workflows

	if in.Workflow != "" {
		list = nil

		for _, w := range o.Workflows {
			if string(w.ID) == in.Workflow {
				list = append(list, w)
			}
		}

		if len(list) == 0 {
			return statsOut{}, domain.ErrNotFound
		}
	}

	if in.Cursor == "" && in.Workflow == "" && len(o.Traders) > 0 {
		for _, t := range o.Traders[:min(len(o.Traders), tradersShown)] {
			tr := traderStatsOut{
				Mode: string(t.Mode), Trader: string(t.Trader), Workflows: t.Workflows, Profit: t.Profit.String(),
				Realized: t.Realized.String(), Positions: t.Positions, Volume: t.Volume.String(), Orders: t.Orders,
				Wins: t.Wins, Closed: t.Closed,
			}
			if t.Label != "" {
				tr.Untrusted = Untrusted{"label": t.Label}
			}

			out.Traders = append(out.Traders, tr)
		}

		out.Notice = Notice
	}

	from, limit := min(in.offset(), len(list)), in.filter().Limit
	to := min(from+limit, len(list))

	for _, w := range list[from:to] {
		out.Workflows = append(out.Workflows, workflowStatsOf(w))
	}

	if to < len(list) {
		out.Next = strconv.Itoa(to)
	}

	return out, nil
}

func totalsOf(t stats.Totals) totalsOut {
	return totalsOut{
		Workflows: t.Workflows, Profit: t.Profit.String(), Realized: t.Realized.String(), Positions: t.Positions,
		Volume: t.Volume.String(), Orders: t.Orders, Wins: t.Wins, Closed: t.Closed,
	}
}

func workflowStatsOf(w stats.Workflow) workflowStatsOut {
	out := workflowStatsOut{ID: string(w.ID), Name: w.Name, Active: w.Active, Day: runCountsOut(w.Runs.Day), Week: runCountsOut(w.Runs.Week)}

	if l := w.Runs.Last; l != nil {
		out.Last = &lastRunOut{ID: string(l.ID), Status: string(l.Status), StartedAt: l.StartedAt, FinishedAt: l.FinishedAt}
	}

	if r := w.Trading; r != nil {
		out.Trading = &resultOut{
			Mode: string(r.Mode), Profit: r.Profit.String(), Realized: r.Realized.String(), Unrealized: r.Unrealized.String(),
			Positions: r.Positions, Unpriced: r.Unpriced, Volume: r.Volume.String(), Orders: r.Orders, Wins: r.Wins, Closed: r.Closed,
		}
	}

	return out
}
