package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/trading"
)

// TradingService shows a workflow's books to its owner: its paper account, its orders
// and its holdings (D91).
type TradingService interface {
	PaperAccount(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.PaperAccount, bool, decimal.Decimal, error)
	Orders(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.PlacedOrder], error)
	Holdings(ctx context.Context, p auth.Principal, id domain.WorkflowID) ([]domain.Holding, error)
	Summary(ctx context.Context, p auth.Principal, id domain.WorkflowID) (trading.Summary, error)
	Reset(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.PaperAccount, error)
}

// Amounts are text, as items carry decimals: exact.

type paperAccountDTO struct {
	// Open is false until the workflow's first paper order; the rest is then what an
	// account would start with.
	Open        bool       `json:"open"`
	Cash        string     `json:"cash" example:"989.65"`
	StartedWith string     `json:"startedWith" example:"1000"`
	StartedAt   *time.Time `json:"startedAt,omitempty" validate:"optional" format:"date-time"`
	// ResetAt is when it was last started over: orders before it are history.
	ResetAt *time.Time `json:"resetAt,omitempty" validate:"optional" format:"date-time"`
} // @name PaperAccount

type orderFillDTO struct {
	Price   string `json:"price"`
	Shares  string `json:"shares"`
	Dollars string `json:"dollars"`
	Fee     string `json:"fee"` // in dollars, on top of a buy, out of a sell
} // @name OrderFill

type orderDTO struct {
	ID          string `json:"id"`
	ExecutionID string `json:"executionId,omitempty" validate:"optional"`
	NodeID      string `json:"nodeId"`
	Mode        string `json:"mode" enums:"paper,live"`
	// Trader is the wallet whose trade it copies, with their label then; absent for an
	// order copied from no one.
	Trader      string `json:"trader,omitempty" validate:"optional"`
	TraderLabel string `json:"traderLabel,omitempty" validate:"optional"`
	TokenID     string `json:"tokenId"`
	MarketID    string `json:"marketId,omitempty" validate:"optional"`
	Side        string `json:"side" enums:"buy,sell"`
	Type        string `json:"type" enums:"FOK,FAK,GTC,GTD,SETTLE"` // SETTLE: a holding closed by its market's resolution
	PostOnly    bool   `json:"postOnly"`
	// Price is the limit: the most a buy pays per share, the least a sell takes.
	Price string `json:"price"`
	// Shares asked for, or for a buy of dollars, Dollars.
	Shares  string         `json:"shares,omitempty" validate:"optional"`
	Dollars string         `json:"dollars,omitempty" validate:"optional"`
	Status  string         `json:"status" enums:"filled,partial,unfilled,refused"`
	Reason  string         `json:"reason,omitempty" validate:"optional"`
	Fills   []orderFillDTO `json:"fills"`
	At      time.Time      `json:"at" format:"date-time"`
} // @name Order

type orderPage struct {
	Orders     []orderDTO `json:"orders"`
	NextCursor string     `json:"nextCursor,omitempty" validate:"optional"`
} // @name OrderPage

type holdingDTO struct {
	Mode        string `json:"mode" enums:"paper,live"`
	Trader      string `json:"trader,omitempty" validate:"optional"`
	TraderLabel string `json:"traderLabel,omitempty" validate:"optional"`
	TokenID     string `json:"tokenId"`
	MarketID    string `json:"marketId,omitempty" validate:"optional"`
	Shares      string `json:"shares"`
	// Cost is what the shares held cost, fees included; Realized is the profit or loss
	// of those sold.
	Cost      string    `json:"cost"`
	Realized  string    `json:"realized"`
	UpdatedAt time.Time `json:"updatedAt" format:"date-time"`
} // @name Holding

type holdingList struct {
	Holdings []holdingDTO `json:"holdings"`
} // @name HoldingList

// getPaperAccount returns a workflow's paper account (D89).
//
//	@Summary	Paper account
//	@ID			getPaperAccount
//	@Tags		trading
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	paperAccountDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/paper-account [get]
func (s *Server) getPaperAccount(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	a, open, start, err := s.trading.PaperAccount(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	if !open {
		c.JSON(http.StatusOK, paperAccountDTO{Cash: start.String(), StartedWith: start.String()})
		return
	}

	c.JSON(http.StatusOK, toPaperAccountDTO(a))
}

func toPaperAccountDTO(a domain.PaperAccount) paperAccountDTO {
	out := paperAccountDTO{Open: true, Cash: a.Cash.String(), StartedWith: a.StartedWith.String(), StartedAt: &a.StartedAt}
	if !a.ResetAt.IsZero() {
		out.ResetAt = &a.ResetAt
	}

	return out
}

// listOrders lists the orders a workflow placed, newest first, refused ones with why.
//
//	@Summary	List orders
//	@ID			listOrders
//	@Tags		trading
//	@Param		id		path		string	true	"Workflow ID"
//	@Param		limit	query		int		false	"Page size"	minimum(1)
//	@Param		cursor	query		string	false	"nextCursor of the previous page"
//	@Success	200		{object}	orderPage
//	@Failure	400		{object}	errorBody	"invalid_input"
//	@Failure	401		{object}	errorBody	"unauthenticated"
//	@Failure	404		{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/orders [get]
func (s *Server) listOrders(ctx context.Context, c *app.RequestContext) {
	f, err := pageFilter(c)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	p, _ := principal(c)

	page, err := s.trading.Orders(ctx, p, domain.WorkflowID(c.Param("id")), f)
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := orderPage{Orders: make([]orderDTO, len(page.Items)), NextCursor: page.NextCursor}
	for i, o := range page.Items {
		out.Orders[i] = toOrderDTO(o)
	}

	c.JSON(http.StatusOK, out)
}

// listHoldings lists a workflow's positions, a holding per mode, trader copied and
// outcome (D92), those sold out of included.
//
//	@Summary	List holdings
//	@ID			listHoldings
//	@Tags		trading
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	holdingList
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/holdings [get]
func (s *Server) listHoldings(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	holdings, err := s.trading.Holdings(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	out := holdingList{Holdings: make([]holdingDTO, len(holdings))}
	for i, h := range holdings {
		out.Holdings[i] = toHoldingDTO(h)
	}

	c.JSON(http.StatusOK, out)
}

func toHoldingDTO(h domain.Holding) holdingDTO {
	return holdingDTO{
		Mode: string(h.Mode), Trader: string(h.Trader), TraderLabel: h.TraderLabel,
		TokenID: string(h.TokenID), MarketID: string(h.MarketID),
		Shares: h.Shares.String(), Cost: h.Cost.String(), Realized: h.Realized.String(), UpdatedAt: h.UpdatedAt,
	}
}

func toOrderDTO(o domain.PlacedOrder) orderDTO {
	amount := func(d decimal.Decimal) string {
		if d.IsZero() {
			return ""
		}

		return d.String()
	}

	out := orderDTO{
		ID: string(o.ID), ExecutionID: string(o.ExecutionID), NodeID: string(o.NodeID), Mode: string(o.Mode),
		Trader: string(o.Trader), TraderLabel: o.TraderLabel, TokenID: string(o.TokenID), MarketID: string(o.MarketID),
		Side: string(o.Side), Type: string(o.Type), PostOnly: o.PostOnly, Price: o.Price.String(),
		Shares: amount(o.Shares), Dollars: amount(o.Dollars), Status: string(o.Status), Reason: o.Reason,
		Fills: make([]orderFillDTO, len(o.Fills)), At: o.CreatedAt,
	}

	for i, f := range o.Fills {
		out.Fills[i] = orderFillDTO{Price: f.Price.String(), Shares: f.Shares.String(), Dollars: f.Dollars.String(), Fee: f.Fee.String()}
	}

	return out
}

type accountSummaryDTO struct {
	paperAccountDTO

	// Worth is the cash and what the holdings are worth now; Profit is it less what the
	// account started with.
	Worth  string `json:"worth"`
	Profit string `json:"profit"`
} // @name AccountSummary

type traderSummaryDTO struct {
	// Trader is absent for the orders copied from no one.
	Trader     string `json:"trader,omitempty" validate:"optional"`
	Label      string `json:"label,omitempty" validate:"optional"`
	Copies     int    `json:"copies"` // orders that filled
	Volume     string `json:"volume"` // the dollars they traded
	Realized   string `json:"realized"`
	Unrealized string `json:"unrealized"`
	Wins       int    `json:"wins"`   // closed holdings that made money
	Closed     int    `json:"closed"` // holdings sold out of, or settled
} // @name TraderSummary

type holdingSummaryDTO struct {
	holdingDTO

	Question string `json:"question,omitempty" validate:"optional"`
	Outcome  string `json:"outcome,omitempty" validate:"optional"`
	// Mark is a share's price now, the book's midpoint; absent when nobody knows, and
	// Value is then the cost.
	Mark       string `json:"mark,omitempty" validate:"optional"`
	Value      string `json:"value"`
	Unrealized string `json:"unrealized"`
} // @name HoldingSummary

type tradingSummaryDTO struct {
	Account  accountSummaryDTO   `json:"account"`
	Traders  []traderSummaryDTO  `json:"traders"`
	Holdings []holdingSummaryDTO `json:"holdings"` // open ones
} // @name TradingSummary

// getTradingSummary is how a workflow's trading went: its paper account, each trader it
// copies since the account (re)started, and its open holdings marked to market (D104).
//
//	@Summary	Trading results
//	@ID			getTradingSummary
//	@Tags		trading
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	tradingSummaryDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/trading [get]
func (s *Server) getTradingSummary(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	sum, err := s.trading.Summary(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	a := sum.Account
	out := tradingSummaryDTO{
		Account: accountSummaryDTO{
			paperAccountDTO: paperAccountDTO{Open: a.Open, Cash: a.Cash.String(), StartedWith: a.StartedWith.String()},
			Worth:           a.Worth.String(), Profit: a.Profit.String(),
		},
		Traders:  make([]traderSummaryDTO, len(sum.Traders)),
		Holdings: make([]holdingSummaryDTO, len(sum.Holdings)),
	}

	if a.Open {
		out.Account.StartedAt = &a.StartedAt
		if !a.ResetAt.IsZero() {
			out.Account.ResetAt = &a.ResetAt
		}
	}

	for i, t := range sum.Traders {
		out.Traders[i] = traderSummaryDTO{
			Trader: string(t.Trader), Label: t.Label, Copies: t.Copies, Volume: t.Volume.String(),
			Realized: t.Realized.String(), Unrealized: t.Unrealized.String(), Wins: t.Wins, Closed: t.Closed,
		}
	}

	for i, h := range sum.Holdings {
		dto := holdingSummaryDTO{
			holdingDTO: toHoldingDTO(h.Holding), Question: h.Question, Outcome: h.Outcome,
			Value: h.Value.String(), Unrealized: h.Unrealized().String(),
		}

		if h.Mark != nil {
			dto.Mark = h.Mark.String()
		}

		out.Holdings[i] = dto
	}

	c.JSON(http.StatusOK, out)
}

// resetPaperAccount starts a workflow's paper account over (D107): cash back to the Paper
// balance, holdings emptied, the orders before kept, under the reset.
//
//	@Summary	Reset the paper account
//	@ID			resetPaperAccount
//	@Tags		trading
//	@Param		id	path		string	true	"Workflow ID"
//	@Success	200	{object}	paperAccountDTO
//	@Failure	401	{object}	errorBody	"unauthenticated"
//	@Failure	404	{object}	errorBody	"not_found"
//	@Security	SessionCookie
//	@Security	APIToken
//	@Router		/workflows/{id}/paper-account/reset [post]
func (s *Server) resetPaperAccount(ctx context.Context, c *app.RequestContext) {
	p, _ := principal(c)

	a, err := s.trading.Reset(ctx, p, domain.WorkflowID(c.Param("id")))
	if err != nil {
		s.fail(ctx, c, err)
		return
	}

	c.JSON(http.StatusOK, toPaperAccountDTO(a))
}
