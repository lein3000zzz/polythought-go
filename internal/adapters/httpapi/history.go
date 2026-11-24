package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// HistoryService is who switched each workflow and why (D433).
type HistoryService interface {
	List(ctx context.Context, p auth.Principal, workflow domain.WorkflowID, groups []domain.SwitchGroup, page ports.PageFilter) (ports.Page[domain.Switch], error)
	AdminList(ctx context.Context, p auth.Principal, owner domain.UserID, workflow domain.WorkflowID, groups []domain.SwitchGroup, page ports.PageFilter) (ports.Page[domain.Switch], error)
}

type switcherDTO struct {
	Kind string `json:"kind" enums:"agent,token"`
	// ID is the agent's grant, or the token.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Confirmed says you confirmed it first, as it waited for you (D406).
	Confirmed bool `json:"confirmed,omitempty" validate:"optional"`
} // @name Switcher

type switchWalletDTO struct {
	ID      string `json:"id"`
	Address string `json:"address"` // kept, though the wallet go
} // @name SwitchWallet

type switchDTO struct {
	ID string `json:"id"`
	// Kind is what it did: on or off; hold, an admin holding it off, or release, letting
	// it be switched on again (D435); paper, live or another wallet (wallet); reset, its
	// paper account; kill_on or kill_off, your kill switch, as it traded live.
	Kind string `json:"kind" enums:"on,off,hold,release,paper,live,wallet,reset,kill_on,kill_off"`
	// By is the agent or token that did it; absent when you did, an admin or Polythought.
	By *switcherDTO `json:"by,omitempty" validate:"optional"`
	// Polythought says Polythought did it itself, Reason saying why.
	Polythought bool `json:"polythought"`
	// Admin says an admin did it, Reason saying why (D435).
	Admin bool `json:"admin"`
	// Reason is why: the agent's or token's own words, as it gave them, or Polythought's;
	// absent when none was given.
	Reason string `json:"reason,omitempty" validate:"optional"`
	// Wallet is the one it trades live from, for live and wallet.
	Wallet *switchWalletDTO `json:"wallet,omitempty" validate:"optional"`
	At     time.Time        `json:"at" format:"date-time"`
} // @name WorkflowSwitch

type historyPage struct {
	Switches   []switchDTO `json:"switches"`
	NextCursor string      `json:"nextCursor,omitempty" validate:"optional"`
} // @name WorkflowHistoryPage

func toSwitchDTO(sw domain.Switch) switchDTO {
	out := switchDTO{ID: string(sw.ID), Kind: string(sw.Kind), Polythought: sw.Polythought, Admin: sw.Admin, Reason: sw.Reason, At: sw.At}

	if !sw.By.IsUser() {
		kind, id := "agent", string(sw.By.GrantID)
		if sw.By.GrantID == "" {
			kind, id = "token", string(sw.By.TokenID)
		}

		out.By = &switcherDTO{Kind: kind, ID: id, Name: sw.By.Name, Confirmed: sw.By.Confirmed}
	}

	if sw.WalletID != "" || sw.WalletAddress != "" {
		out.Wallet = &switchWalletDTO{ID: string(sw.WalletID), Address: string(sw.WalletAddress)}
	}

	return out
}

// reasoned is the caller with why its call says it acts, from reason (D433), for the
// workflow's history.
func reasoned(c *app.RequestContext) auth.Principal {
	p, _ := principal(c)
	p.Reason = strings.TrimSpace(c.Query("reason"))

	return p
}

// workflowHistory lists who switched a workflow and why (D433), newest first: on and off,
// paper or live and from which wallet, paper resets and your kill switch.
//
//	@Summary	A workflow's history
//	@ID			getWorkflowHistory
//	@Tags		workflows
//	@Param		id		path		string	true	"Workflow ID"
//	@Param		kinds	query		string	false	"Some kinds alone, comma-separated: on_off, trading, reset, kill_switch"
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	historyPage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/history [get]
func (s *Server) workflowHistory(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.history.List(ctx, p, domain.WorkflowID(c.Param("id")), kindsOf(c), f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toHistoryPage(page))
}

// kindsOf are the groups of kinds the history asks for; none, every kind.
func kindsOf(c *app.RequestContext) []domain.SwitchGroup {
	var groups []domain.SwitchGroup

	for g := range strings.SplitSeq(c.Query("kinds"), ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups = append(groups, domain.SwitchGroup(g))
		}
	}

	return groups
}

func toHistoryPage(page ports.Page[domain.Switch]) historyPage {
	out := historyPage{Switches: make([]switchDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, sw := range page.Items {
		out.Switches[i] = toSwitchDTO(sw)
	}

	return out
}

func (s *Server) hasHistory(ctx context.Context, c *app.RequestContext) {
	if s.history == nil {
		s.fail(ctx, c, domain.ErrNotFound)
		c.Abort()

		return
	}

	c.Next(ctx)
}
