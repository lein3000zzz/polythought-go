package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// The tool that says who switched a workflow and why (D433), as its History page does.

// History is who switched each workflow and why: the history service.
type History interface {
	List(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, groups []domain.SwitchGroup, page ports.PageFilter) (ports.Page[domain.Switch], error)
}

type historyIn struct {
	Workflow string   `json:"workflow" jsonschema:"the workflow's ID"`
	Kinds    []string `json:"kinds,omitempty" jsonschema:"some kinds alone: on_off, trading, reset, kill_switch; none, all"`
	page
}

type switcherOut struct {
	Kind      string `json:"kind" jsonschema:"agent or token"`
	ID        string `json:"id" jsonschema:"the agent's grant, or the token"`
	Name      string `json:"name"`
	Confirmed bool   `json:"confirmed,omitempty" jsonschema:"the user confirmed it first"`
}

type switchOut struct {
	Kind        string       `json:"kind" jsonschema:"on or off; paper, live, or wallet: live from another wallet; reset: its paper account; kill_on or kill_off: the user's kill switch, as it traded live"`
	By          *switcherOut `json:"by,omitempty" jsonschema:"the agent or token that did it; absent when the user did, or Polythought"`
	Polythought bool         `json:"polythought,omitempty" jsonschema:"Polythought did it itself, as reason says"`
	Reason      string       `json:"reason,omitempty" jsonschema:"Polythought's reason"`
	Wallet      string       `json:"wallet,omitempty" jsonschema:"live and wallet: the address it trades from"`
	At          time.Time    `json:"at"`
	Untrusted   Untrusted    `json:"untrusted,omitempty" jsonschema:"reason: why the agent or token said it did it, in its own words"`
}

type switchesOut struct {
	Switches []switchOut `json:"switches"`
	Notice   string      `json:"notice,omitempty"`
	Next     string      `json:"next,omitempty" jsonschema:"the cursor for the page after; absent at the end"`
}

func (s *Server) addHistoryTools(srv *mcp.Server) {
	add(s, srv, Tool[historyIn, switchesOut]{
		Name: "get_workflow_history", Title: "Get who switched a workflow", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "Who switched a workflow and why, newest first: on and off, paper or live and from which wallet, its " +
			"paper account reset, and the user's kill switch as it traded live; by the user, an agent or token by name, " +
			"or Polythought itself. Agents' and tokens' reasons are their own words, under untrusted. Give a reason " +
			"yourself when you switch_workflow, set_trading, set_kill_switch or reset_paper_account.",
		Handle: s.getWorkflowHistory,
	})
}

func (s *Server) getWorkflowHistory(ctx context.Context, p auth.Principal, in historyIn) (switchesOut, error) {
	groups := make([]domain.SwitchGroup, len(in.Kinds))
	for i, k := range in.Kinds {
		groups[i] = domain.SwitchGroup(k)
	}

	got, err := s.history.List(ctx, p, domain.WorkflowID(in.Workflow), groups, in.filter())
	if err != nil {
		return switchesOut{}, err
	}

	out := switchesOut{Switches: make([]switchOut, len(got.Items)), Next: got.NextCursor}

	for i, sw := range got.Items {
		o := switchOut{Kind: string(sw.Kind), Polythought: sw.Polythought, Wallet: string(sw.WalletAddress), At: sw.At}

		if !sw.By.IsUser() {
			kind, id := "agent", string(sw.By.GrantID)
			if sw.By.GrantID == "" {
				kind, id = "token", string(sw.By.TokenID)
			}

			o.By = &switcherOut{Kind: kind, ID: id, Name: sw.By.Name, Confirmed: sw.By.Confirmed}
		}

		switch {
		case sw.Reason == "":
		case sw.Polythought:
			o.Reason = sw.Reason
		default:
			o.Untrusted, out.Notice = Untrusted{"reason": sw.Reason}, Notice
		}

		out.Switches[i] = o
	}

	return out, nil
}
