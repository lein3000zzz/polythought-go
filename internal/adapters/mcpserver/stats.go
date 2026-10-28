package mcpserver

import (
	"context"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/stats"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// The tool that says how the user's workflows are doing, together (D429), as the workflow
// cards and the dashboard do.

// Stats is how the user's workflows are doing, and for an admin every user's (D431): the
// stats service.
type Stats interface {
	Overview(ctx context.Context, p auth.Principal) (stats.Overview, error)
	Instance(ctx context.Context, p auth.Principal) (stats.Instance, error)
	OverviewOf(ctx context.Context, p auth.Principal, id domain.UserID) (domain.User, stats.Overview, error)
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

type extremeOut struct {
	Profit string    `json:"profit" jsonschema:"dollars"`
	At     time.Time `json:"at" jsonschema:"when it was: sampled every few minutes, or now"`
}

type workflowStatsOut struct {
	ID        string       `json:"id"`
	Name      string       `json:"name,omitempty" jsonschema:"absent for another user's: under untrusted"`
	Untrusted Untrusted    `json:"untrusted,omitempty" jsonschema:"name: another user's workflow's, as they gave it"`
	Active    bool         `json:"active"`
	Trading   *resultOut   `json:"trading,omitempty" jsonschema:"what it made; absent until it traded in the mode it trades in now"`
	High      *extremeOut  `json:"high,omitempty" jsonschema:"its highest profit in that mode, on paper since the account's last reset; with trading"`
	Low       *extremeOut  `json:"low,omitempty" jsonschema:"its lowest, likewise"`
	Day       runCountsOut `json:"runsDay" jsonschema:"its runs started in the last day, by hand too, and of them those that failed"`
	Week      runCountsOut `json:"runsWeek" jsonschema:"in the last week"`
	Last      *lastRunOut  `json:"lastRun,omitempty" jsonschema:"its latest run; absent when none is kept"`
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

type instanceIn struct {
	User string `json:"user,omitempty" jsonschema:"a user's ID, as list_users gives it: their workflows, as get_workflow_stats gives a user's own"`
	page
}

type userStatsOut struct {
	ID        string       `json:"id"`
	Role      string       `json:"role"`
	Disabled  bool         `json:"disabled,omitempty"`
	Workflows int          `json:"workflows" jsonschema:"all they have"`
	Active    int          `json:"active"`
	Paper     totalsOut    `json:"paper"`
	Live      totalsOut    `json:"live"`
	Day       runCountsOut `json:"runsDay"`
	Week      runCountsOut `json:"runsWeek"`
}

type instanceOut struct {
	Users     []userStatsOut     `json:"users,omitempty" jsonschema:"those with workflows, each one's added up, by ID; never their emails"`
	Workflows []workflowStatsOut `json:"workflows,omitempty" jsonschema:"the user's asked for, by name"`
	Traders   []traderStatsOut   `json:"traders,omitempty" jsonschema:"the user's asked for, on the first page, as get_workflow_stats gives them"`
	Notice    string             `json:"notice,omitempty"`
	Next      string             `json:"next,omitempty" jsonschema:"the cursor for the page after; absent at the end"`
	Paper     totalsOut          `json:"paper" jsonschema:"every user's trading on paper, or the user's asked for; never added to live"`
	Live      totalsOut          `json:"live"`
	Day       runCountsOut       `json:"runsDay"`
	Week      runCountsOut       `json:"runsWeek"`
	At        time.Time          `json:"at"`
}

func (s *Server) addStatsTools(srv *mcp.Server) {
	add(s, srv, Tool[statsIn, statsOut]{
		Name: "get_workflow_stats", Title: "Get how the workflows are doing", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "How the user's workflows are doing, as the Workflows page and its dashboard show it: for each, what it " +
			"made in the mode it trades in now, its profit realized and unrealized, its highest and lowest with when, " +
			"open positions, volume and wins, and " +
			"its runs and failures in the last day and week, with its latest run. Paper and live totals come apart, " +
			"never added together. The first page also gives what copying each trader came to across the workflows. " +
			"A page of workflows at a time, by name; or one workflow by its ID. Traders' labels are under untrusted.",
		Handle: s.getWorkflowStats,
	})

	add(s, srv, Tool[instanceIn, instanceOut]{
		Name: "get_instance_stats", Title: "Get how every user's workflows are doing", Scope: domain.ScopeAdmin, ReadOnly: true,
		Description: "For an admin, as Admin → Stats shows it: every user with workflows, by ID, never their emails, each " +
			"one's paper and live totals and runs in the last day and week, a page at a time; and the instance's totals, " +
			"paper and live apart. Given a user's ID, their workflows instead, as get_workflow_stats gives a user's own; " +
			"their workflows' names and traders' labels are under untrusted.",
		Handle: s.getInstanceStats,
	})
}

func (s *Server) getInstanceStats(ctx context.Context, p auth.Principal, in instanceIn) (instanceOut, error) {
	if in.User != "" {
		_, o, err := s.stats.OverviewOf(ctx, p, domain.UserID(in.User))
		if err != nil {
			return instanceOut{}, err
		}

		out := instanceOut{
			Workflows: []workflowStatsOut{},
			Paper:     totalsOf(o.Paper), Live: totalsOf(o.Live),
			Day: runCountsOut(o.Day), Week: runCountsOut(o.Week), At: o.At,
		}

		if in.Cursor == "" {
			out.Traders = tradersOf(o.Traders)
		}

		list, next := pageOf(o.Workflows, in.page)
		for _, w := range list {
			ws := workflowStatsOf(w)
			ws.Name, ws.Untrusted = "", Untrusted{"name": w.Name}
			out.Workflows = append(out.Workflows, ws)
		}

		out.Next = next
		if len(out.Workflows) > 0 || len(out.Traders) > 0 {
			out.Notice = Notice
		}

		return out, nil
	}

	all, err := s.stats.Instance(ctx, p)
	if err != nil {
		return instanceOut{}, err
	}

	out := instanceOut{
		Users: []userStatsOut{},
		Paper: totalsOf(all.Paper), Live: totalsOf(all.Live),
		Day: runCountsOut(all.Day), Week: runCountsOut(all.Week), At: all.At,
	}

	list, next := pageOf(all.Users, in.page)
	for _, t := range list {
		out.Users = append(out.Users, userStatsOut{
			ID: string(t.User.ID), Role: string(t.User.Role), Disabled: t.User.Disabled(), Workflows: t.Workflows, Active: t.Active,
			Paper: totalsOf(t.Paper), Live: totalsOf(t.Live), Day: runCountsOut(t.Day), Week: runCountsOut(t.Week),
		})
	}

	out.Next = next

	return out, nil
}

// pageOf is the page of a list in memory the cursor asks for, and the cursor after it.
func pageOf[T any](list []T, in page) ([]T, string) {
	from, limit := min(in.offset(), len(list)), in.filter().Limit
	to := min(from+limit, len(list))

	if to < len(list) {
		return list[from:to], strconv.Itoa(to)
	}

	return list[from:to], ""
}

// tradersOf are the most profitable of them, their labels under untrusted.
func tradersOf(traders []trading.TraderResult) []traderStatsOut {
	var out []traderStatsOut

	for _, t := range traders[:min(len(traders), tradersShown)] {
		tr := traderStatsOut{
			Mode: string(t.Mode), Trader: string(t.Trader), Workflows: t.Workflows, Profit: t.Profit.String(),
			Realized: t.Realized.String(), Positions: t.Positions, Volume: t.Volume.String(), Orders: t.Orders,
			Wins: t.Wins, Closed: t.Closed,
		}
		if t.Label != "" {
			tr.Untrusted = Untrusted{"label": t.Label}
		}

		out = append(out, tr)
	}

	return out
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
		out.Traders, out.Notice = tradersOf(o.Traders), Notice
	}

	shown, next := pageOf(list, in.page)
	for _, w := range shown {
		out.Workflows = append(out.Workflows, workflowStatsOf(w))
	}

	out.Next = next

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

	if e := w.High; e != nil {
		out.High = &extremeOut{Profit: e.Profit.String(), At: e.At}
	}

	if e := w.Low; e != nil {
		out.Low = &extremeOut{Profit: e.Profit.String(), At: e.At}
	}

	return out
}
