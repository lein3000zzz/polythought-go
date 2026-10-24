package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// The tools that change how the user's workflows trade (D406): a workflow's mode and
// wallet, the kill switch, a paper account's reset, cancelling an order. What reaches real
// money may wait for the user to confirm it, as the user says on Settings.

// Trader is what the trading tools change: the trading service.
type Trader interface {
	SetSetup(ctx context.Context, p auth.Principal, id domain.WorkflowID, c trading.SetupChange) (trading.Setup, error)
	SetKillSwitch(ctx context.Context, p auth.Principal, on bool) (trading.KillSwitch, error)
	Reset(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.PaperAccount, error)
	Cancel(ctx context.Context, p auth.Principal, w domain.WorkflowID, id domain.OrderID) (domain.PlacedOrder, error)
}

type setTradingIn struct {
	Workflow string `json:"workflow" jsonschema:"the workflow's ID"`
	Mode     string `json:"mode" jsonschema:"paper or live"`
	Wallet   string `json:"wallet,omitempty" jsonschema:"live: the wallet it trades from, by its ID, as list_wallets gives it"`
	// Confirmed is the user's yes to real money (D201), as the app's dialog asks it.
	Confirmed bool `json:"confirmed,omitempty" jsonschema:"live: the user said yes to its orders spending real money from this wallet; set it only as the user told you"`
}

type killSwitchIn struct {
	On bool `json:"on" jsonschema:"true stops live trading at once; false lets it go on again"`
}

type killSwitchOut struct {
	On        bool       `json:"on"`
	Since     *time.Time `json:"since,omitempty"`
	Cancelled int        `json:"cancelled,omitempty" jsonschema:"the live orders resting on the book it cancelled"`
	Left      int        `json:"left,omitempty" jsonschema:"those that still rest, Polymarket not cancelling them"`
}

type workflowRef struct {
	Workflow string `json:"workflow" jsonschema:"the workflow's ID"`
}

type paperOut struct {
	Cash        string    `json:"cash"`
	StartedWith string    `json:"startedWith"`
	ResetAt     time.Time `json:"resetAt"`
}

type cancelIn struct {
	Workflow string `json:"workflow" jsonschema:"the workflow's ID"`
	Order    string `json:"order" jsonschema:"the order's ID, as list_orders gives it"`
}

func (s *Server) addTradeTools(srv *mcp.Server) {
	add(s, srv, Tool[setTradingIn, setupOut]{
		Name: "set_trading", Title: "Set how a workflow trades",
		Description: "Have a workflow trade on paper, or live from one of the user's wallets that can trade, spending its pUSD. " +
			"Paper needs the paper scope, live the live scope. Going live, or to another wallet, waits for the user to " +
			"confirm it where they have it wait, as get_confirmation then says; where they do not, it needs confirmed. " +
			"Back on paper, its live orders resting on the book are cancelled.",
		Handle: s.setTrading,
	})

	add(s, srv, Tool[killSwitchIn, killSwitchOut]{
		Name: "set_kill_switch", Title: "Stop or resume live trading",
		Description: "The user's kill switch. On, with any scope, at once: every live order of their workflows is refused " +
			"and those resting on the book cancelled; paper trades on. Off, with the live scope, lets live trading go on " +
			"again, and waits for the user to confirm it where they have it wait.",
		Handle: s.setKillSwitch,
	})

	add(s, srv, Tool[workflowRef, paperOut]{
		Name: "reset_paper_account", Title: "Reset a paper account", Scope: domain.ScopePaper, Destructive: true,
		Description: "Start a workflow's paper account again: its paper orders, holdings and results are gone, its cash back " +
			"to what an account starts with. Its resting paper orders are cancelled. It cannot be undone.",
		Handle: s.resetPaper,
	})

	add(s, srv, Tool[cancelIn, orderOut]{
		Name: "cancel_order", Title: "Cancel an order",
		Description: "Cancel one of a workflow's orders resting on the book: a paper one with the paper scope, a live one " +
			"with the live scope, at Polymarket. The answer is the order as it is then.",
		Handle: s.cancelOrder,
	})
}

func (s *Server) setTrading(ctx context.Context, p auth.Principal, in setTradingIn) (setupOut, error) {
	setup, err := s.trader.SetSetup(ctx, p, domain.WorkflowID(in.Workflow), trading.SetupChange{
		Mode: domain.TradingMode(in.Mode), WalletID: domain.WalletID(in.Wallet), Confirmed: in.Confirmed,
	})
	if err != nil {
		return setupOut{}, err
	}

	out := setupOut{
		Mode: string(setup.Mode), HoldsLive: setup.HoldsLive, Resting: setup.Resting, LiveAvailable: setup.LiveAvailable, Why: setup.Why,
	}

	if setup.Wallet != nil {
		out.Wallet = string(setup.Wallet.ID)
	}

	return out, nil
}

func (s *Server) setKillSwitch(ctx context.Context, p auth.Principal, in killSwitchIn) (killSwitchOut, error) {
	ks, err := s.trader.SetKillSwitch(ctx, p, in.On)
	if err != nil {
		return killSwitchOut{}, err
	}

	return killSwitchOut{On: ks.On, Since: optionalTime(ks.Since), Cancelled: ks.Cancelled, Left: ks.Left}, nil
}

func (s *Server) resetPaper(ctx context.Context, p auth.Principal, in workflowRef) (paperOut, error) {
	a, err := s.trader.Reset(ctx, p, domain.WorkflowID(in.Workflow))
	if err != nil {
		return paperOut{}, err
	}

	return paperOut{Cash: a.Cash.String(), StartedWith: a.StartedWith.String(), ResetAt: a.ResetAt}, nil
}

func (s *Server) cancelOrder(ctx context.Context, p auth.Principal, in cancelIn) (orderOut, error) {
	o, err := s.trader.Cancel(ctx, p, domain.WorkflowID(in.Workflow), domain.OrderID(in.Order))
	if err != nil {
		return orderOut{}, err
	}

	return orderOf(o), nil
}
