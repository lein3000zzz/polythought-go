package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// The tools that read the user's money (D402): their wallets and what is in them, and how
// their workflows trade, on paper and live. Amounts are dollars, as decimal strings.

// Wallets are the user's linked wallets: the wallets service.
type Wallets interface {
	List(ctx context.Context, p auth.Principal) ([]domain.Wallet, error)
	CredentialsInfo(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.CredentialsInfo, error)
}

// Trades are how the user's workflows trade, and their wallets' cash: the trading service.
type Trades interface {
	Setup(ctx context.Context, p auth.Principal, id domain.WorkflowID) (trading.Setup, error)
	Summary(ctx context.Context, p auth.Principal, id domain.WorkflowID, mode domain.TradingMode) (trading.Summary, error)
	Orders(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.PlacedOrder], error)
	OrderLog(ctx context.Context, p auth.Principal, f ports.OrderLogFilter) (ports.Page[trading.LogEntry], error)
	WalletCash(ctx context.Context, p auth.Principal, wallet domain.WalletID) (decimal.Decimal, error)
}

type walletOut struct {
	ID        string `json:"id"`
	Label     string `json:"label,omitempty"`
	Address   string `json:"address"`
	CanTrade  bool   `json:"canTrade" jsonschema:"it has Polymarket credentials and a key to sign orders with"`
	Key       string `json:"key" jsonschema:"what signs its orders: imported, a private key; session, an authorised session key; none"`
	Heartbeat bool   `json:"heartbeat,omitempty" jsonschema:"Polymarket cancels its resting orders when the server goes quiet"`
}

type walletList struct {
	Wallets []walletOut `json:"wallets"`
}

type walletIn struct {
	ID string `json:"id" jsonschema:"the wallet's ID, as list_wallets gives it"`
}

type walletDetails struct {
	walletOut

	Signer        string `json:"signer,omitempty" jsonschema:"the address that signs its orders"`
	SignatureType int    `json:"signatureType,omitempty" jsonschema:"how Polymarket checks its signatures: 0 its own key, 1 and 2 a proxy wallet's, 3 a deposit wallet's"`
	Cash          string `json:"cash,omitempty" jsonschema:"its pUSD on Polymarket, what live orders spend"`
	CashNote      string `json:"cashNote,omitempty" jsonschema:"why its cash is not known"`
}

type tradingIn struct {
	Workflow string `json:"workflow" jsonschema:"the workflow's ID"`
	Mode     string `json:"mode,omitempty" jsonschema:"paper or live: whose results; the mode it trades in now, unless said"`
}

type setupOut struct {
	Mode          string `json:"mode" jsonschema:"paper or live: how its orders go now"`
	Wallet        string `json:"wallet,omitempty" jsonschema:"the wallet it trades live from"`
	HoldsLive     bool   `json:"holdsLive,omitempty" jsonschema:"it holds live shares, so its wallet stays"`
	Resting       int    `json:"resting,omitempty" jsonschema:"its live orders resting on the book"`
	LiveAvailable bool   `json:"liveAvailable" jsonschema:"the instance places live orders now"`
	Why           string `json:"why,omitempty" jsonschema:"why live is not available"`
}

type accountOut struct {
	Mode        string     `json:"mode"`
	Open        bool       `json:"open" jsonschema:"paper: an order has been placed, so the account is open"`
	Cash        string     `json:"cash"`
	CashKnown   bool       `json:"cashKnown,omitempty" jsonschema:"live: its wallet's pUSD could be read"`
	StartedWith string     `json:"startedWith,omitempty" jsonschema:"paper: what it started with"`
	Worth       string     `json:"worth" jsonschema:"its cash and what its holdings are worth now"`
	Profit      string     `json:"profit"`
	Held        string     `json:"held,omitempty" jsonschema:"paper: cash its resting buys hold back"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	ResetAt     *time.Time `json:"resetAt,omitempty"`
}

type traderOut struct {
	Trader     string `json:"trader,omitempty" jsonschema:"the address copied; absent for orders copied from no one"`
	Label      string `json:"label,omitempty" jsonschema:"the user's name for them"`
	Copies     int    `json:"copies"`
	Volume     string `json:"volume"`
	Realized   string `json:"realized"`
	Unrealized string `json:"unrealized"`
	Wins       int    `json:"wins"`
	Closed     int    `json:"closed"`
}

type holdingOut struct {
	TokenID    string    `json:"tokenId"`
	Market     string    `json:"market,omitempty"`
	Trader     string    `json:"trader,omitempty"`
	Shares     string    `json:"shares"`
	Cost       string    `json:"cost" jsonschema:"what they cost, fees included"`
	Mark       string    `json:"mark,omitempty" jsonschema:"a share's price now, when known"`
	Value      string    `json:"value"`
	Realized   string    `json:"realized"`
	Unrealized string    `json:"unrealized"`
	Untrusted  Untrusted `json:"untrusted,omitempty" jsonschema:"the market's question and the outcome's label"`
}

type tradingOut struct {
	Setup      setupOut     `json:"setup"`
	Account    accountOut   `json:"account"`
	Traders    []traderOut  `json:"traders"`
	Holdings   []holdingOut `json:"holdings" jsonschema:"what it holds now"`
	KillSwitch bool         `json:"killSwitch" jsonschema:"the user's kill switch is on: no live orders go"`
	Notice     string       `json:"notice"`
}

type ordersIn struct {
	Workflow string `json:"workflow" jsonschema:"the workflow's ID"`
	page
}

type orderOut struct {
	ID           string     `json:"id"`
	Mode         string     `json:"mode"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason,omitempty" jsonschema:"why it was refused, or did not fill"`
	Side         string     `json:"side"`
	Type         string     `json:"type"`
	TokenID      string     `json:"tokenId"`
	Market       string     `json:"market,omitempty"`
	Price        string     `json:"price"`
	Shares       string     `json:"shares,omitempty"`
	Dollars      string     `json:"dollars,omitempty"`
	Filled       string     `json:"filled" jsonschema:"shares filled"`
	FilledFor    string     `json:"filledFor" jsonschema:"what the fills came to, in dollars"`
	Fees         string     `json:"fees"`
	Trader       string     `json:"trader,omitempty" jsonschema:"the address it copied"`
	Execution    string     `json:"execution,omitempty"`
	Node         string     `json:"node,omitempty"`
	Workflow     string     `json:"workflow,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	Untrusted    Untrusted  `json:"untrusted,omitempty" jsonschema:"the market's question and the outcome's label"`
	WorkflowName string     `json:"workflowName,omitempty" jsonschema:"its workflow, as it was named when deleted"`
}

type orderList struct {
	Orders []orderOut `json:"orders"`
	Next   string     `json:"next,omitempty"`
	Notice string     `json:"notice,omitempty"`
}

type logIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"one workflow's"`
	Wallet   string `json:"wallet,omitempty" jsonschema:"one wallet's"`
	Status   string `json:"status,omitempty" jsonschema:"only those with this status: open, filled, partial, cancelled, refused, failed"`
	page
}

func (s *Server) addMoneyTools(srv *mcp.Server) {
	add(s, srv, Tool[struct{}, walletList]{
		Name: "list_wallets", Title: "List wallets", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The user's linked Polymarket wallets: each one's address, and whether it can trade and with what key.",
		Handle:      s.listWallets,
	})

	add(s, srv, Tool[walletIn, walletDetails]{
		Name: "get_wallet", Title: "Get a wallet", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "One wallet: how it signs, and its pUSD on Polymarket, what live orders spend. positions gives what it holds, by its address.",
		Handle:      s.getWallet,
	})

	add(s, srv, Tool[tradingIn, tradingOut]{
		Name: "get_trading", Title: "Get a workflow's trading", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "How a workflow trades and how it has done: paper or live and from which wallet, its account's cash, " +
			"worth and profit, what copying each trader came to, what it holds now, and whether the kill switch is on.",
		Handle: s.getTrading,
	})

	add(s, srv, Tool[ordersIn, orderList]{
		Name: "list_orders", Title: "List a workflow's orders", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "A workflow's orders, paper and live, newest first, a page at a time: each one's status, price, what filled and its fees.",
		Handle:      s.listOrders,
	})

	add(s, srv, Tool[logIn, orderList]{
		Name: "list_live_orders", Title: "List live orders", Scope: domain.ScopeRead, ReadOnly: true,
		Description: "The user's live orders across their workflows, newest first, a page at a time, as the Orders page lists them, " +
			"narrowed to a workflow, a wallet or a status.",
		Handle: s.listLiveOrders,
	})
}

func walletOf(w domain.Wallet) walletOut {
	key := "none"

	switch {
	case w.HasSessionKey:
		key = "session"
	case w.HasKey:
		key = "imported"
	}

	return walletOut{
		ID: string(w.ID), Label: w.Label, Address: string(w.Address), Heartbeat: w.Heartbeat,
		CanTrade: w.HasCredentials && key != "none", Key: key,
	}
}

func (s *Server) listWallets(ctx context.Context, p auth.Principal, _ struct{}) (walletList, error) {
	ws, err := s.wallets.List(ctx, p)
	if err != nil {
		return walletList{}, err
	}

	out := walletList{Wallets: make([]walletOut, len(ws))}
	for i, w := range ws {
		out.Wallets[i] = walletOf(w)
	}

	return out, nil
}

func (s *Server) getWallet(ctx context.Context, p auth.Principal, in walletIn) (walletDetails, error) {
	ws, err := s.wallets.List(ctx, p)
	if err != nil {
		return walletDetails{}, err
	}

	for _, w := range ws {
		if string(w.ID) != in.ID {
			continue
		}

		out := walletDetails{walletOut: walletOf(w)}

		if w.HasCredentials {
			info, err := s.wallets.CredentialsInfo(ctx, p, w.ID)
			if err != nil {
				return walletDetails{}, err
			}

			out.Signer, out.SignatureType = string(info.Signer), int(info.SignatureType)
		}

		cash, err := s.trades.WalletCash(ctx, p, w.ID)

		switch {
		case err == nil:
			out.Cash = cash.String()
		case expected(err), errors.Is(err, domain.ErrLiveTradingOff), errors.Is(err, domain.ErrWalletCannotTrade):
			out.CashNote = err.Error()
		default:
			return walletDetails{}, err
		}

		return out, nil
	}

	return walletDetails{}, fmt.Errorf("%w: no wallet %q; list_wallets lists them", domain.ErrNotFound, in.ID)
}

func (s *Server) getTrading(ctx context.Context, p auth.Principal, in tradingIn) (tradingOut, error) {
	var mode domain.TradingMode

	switch domain.TradingMode(in.Mode) {
	case "":
	case domain.ModePaper, domain.ModeLive:
		mode = domain.TradingMode(in.Mode)
	default:
		return tradingOut{}, invalid("mode is paper or live")
	}

	id := domain.WorkflowID(in.Workflow)

	setup, err := s.trades.Setup(ctx, p, id)
	if err != nil {
		return tradingOut{}, err
	}

	sum, err := s.trades.Summary(ctx, p, id, mode)
	if err != nil {
		return tradingOut{}, err
	}

	out := tradingOut{
		Setup: setupOut{
			Mode: string(setup.Mode), HoldsLive: setup.HoldsLive, Resting: setup.Resting, LiveAvailable: setup.LiveAvailable, Why: setup.Why,
		},
		Account:    accountOf(sum.Account),
		Traders:    make([]traderOut, len(sum.Traders)),
		Holdings:   make([]holdingOut, len(sum.Holdings)),
		KillSwitch: p.User.LiveStopped(),
		Notice:     Notice,
	}

	if setup.Wallet != nil {
		out.Setup.Wallet = string(setup.Wallet.ID)
	}

	for i, t := range sum.Traders {
		out.Traders[i] = traderOut{
			Trader: string(t.Trader), Label: t.Label, Copies: t.Copies, Volume: t.Volume.String(), Realized: t.Realized.String(),
			Unrealized: t.Unrealized.String(), Wins: t.Wins, Closed: t.Closed,
		}
	}

	for i, h := range sum.Holdings {
		out.Holdings[i] = holdingOut{
			TokenID: string(h.TokenID), Market: string(h.MarketID), Trader: string(h.Trader), Shares: h.Shares.String(),
			Cost: h.Cost.String(), Value: h.Value.String(), Realized: h.Realized.String(), Unrealized: h.Unrealized().String(),
			Untrusted: Untrusted{"question": h.Question, "outcome": h.Outcome},
		}

		if h.Mark != nil {
			out.Holdings[i].Mark = h.Mark.String()
		}
	}

	return out, nil
}

func accountOf(a trading.AccountSummary) accountOut {
	out := accountOut{
		Mode: string(a.Mode), Open: a.Open, Cash: a.Cash.String(), CashKnown: a.CashKnown, Worth: a.Worth.String(),
		Profit: a.Profit.String(), StartedAt: optionalTime(a.StartedAt), ResetAt: optionalTime(a.ResetAt),
	}

	if a.Mode == domain.ModePaper {
		out.StartedWith, out.Held = a.StartedWith.String(), a.Held.String()
	}

	return out
}

func (s *Server) listOrders(ctx context.Context, p auth.Principal, in ordersIn) (orderList, error) {
	got, err := s.trades.Orders(ctx, p, domain.WorkflowID(in.Workflow), in.filter())
	if err != nil {
		return orderList{}, err
	}

	out := orderList{Orders: make([]orderOut, len(got.Items)), Next: got.NextCursor}
	for i, o := range got.Items {
		out.Orders[i] = orderOf(o)
	}

	return out, nil
}

func (s *Server) listLiveOrders(ctx context.Context, p auth.Principal, in logIn) (orderList, error) {
	f := ports.OrderLogFilter{
		PageFilter: in.filter(), WorkflowID: domain.WorkflowID(in.Workflow), WalletID: domain.WalletID(in.Wallet),
		Status: domain.OrderStatus(in.Status),
	}

	got, err := s.trades.OrderLog(ctx, p, f)
	if err != nil {
		return orderList{}, err
	}

	out := orderList{Orders: make([]orderOut, len(got.Items)), Next: got.NextCursor, Notice: Notice}

	for i, e := range got.Items {
		o := orderOf(e.PlacedOrder)
		o.WorkflowName = e.Workflow
		o.Untrusted = Untrusted{"question": e.Question, "outcome": e.Outcome}
		out.Orders[i] = o
	}

	return out, nil
}

func orderOf(o domain.PlacedOrder) orderOut {
	shares, dollars, fees := o.Totals()

	out := orderOut{
		ID: string(o.ID), Mode: string(o.Mode), Status: string(o.Status), Reason: o.Reason, Side: string(o.Side),
		Type: string(o.Type), TokenID: string(o.TokenID), Market: string(o.MarketID), Price: o.Price.String(),
		Filled: shares.String(), FilledFor: dollars.String(), Fees: fees.String(), Trader: string(o.Trader),
		Execution: string(o.ExecutionID), Node: string(o.NodeID), Workflow: string(o.WorkflowID), CreatedAt: o.CreatedAt,
		ExpiresAt: optionalTime(o.Expires),
	}

	if !o.Shares.IsZero() {
		out.Shares = o.Shares.String()
	}

	if !o.Dollars.IsZero() {
		out.Dollars = o.Dollars.String()
	}

	return out
}
