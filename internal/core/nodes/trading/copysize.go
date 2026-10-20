// Package trading holds the nodes that trade: Copy sizing works out a copy of a followed
// trader's trade, Place order places it (D93 to D99).
package trading

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/chance"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	CopySizeType = "trading.copySize"
	PortSkipped  = "skipped"
)

// How Copy sizing sizes a buy (D97).
const (
	SizeFixed        = "fixed"
	SizeMultiple     = "multiple"
	SizeProportional = "proportional"
)

var (
	one       = decimal.NewFromInt(1)
	hundredth = decimal.RequireFromString("0.01")
)

// Books is what the trading nodes read of a workflow's books.
type Books interface {
	Holding(ctx context.Context, key domain.HoldingKey) (domain.Holding, error)
	Holdings(ctx context.Context, workflow domain.WorkflowID) ([]domain.Holding, error)
	Spent(ctx context.Context, workflow domain.WorkflowID, mode domain.TradingMode, since time.Time) (decimal.Decimal, error)
}

// Accounts is how a workflow trades now, and what it has to size copies by: the trading
// service's (D191, D198).
type Accounts interface {
	Mode(ctx context.Context, workflow domain.WorkflowID) (domain.TradingMode, error)
	Worth(ctx context.Context, owner domain.UserID, workflow domain.WorkflowID) (decimal.Decimal, error)
	// Resting is the cash the workflow's resting buys hold, by market, in its mode now.
	Resting(ctx context.Context, workflow domain.WorkflowID) (map[domain.MarketID]decimal.Decimal, error)
}

// Sources are where Copy sizing looks things up.
type Sources struct {
	Books      Books
	Accounts   Accounts
	Rules      ports.TradingRulesReader
	Markets    ports.MarketReader // a token's market, when the item does not say
	Positions  ports.PositionReader
	Portfolios ports.PortfolioReader
}

var _ engine.Node = (*CopySize)(nil)

// CopySize works out a copy of each trade Wallet activity passes on: how much to buy or
// sell, at what price (D94, D95, D97). Copies it cannot make go out of skipped, saying why
// (D96, D98).
type CopySize struct {
	src Sources
}

func NewCopySize(src Sources) *CopySize {
	return &CopySize{src: src}
}

func (*CopySize) Type() engine.NodeType {
	return engine.NodeType{
		Name:  CopySizeType,
		Label: "Copy sizing",
		Description: "Works out a copy of each trade a followed trader made: how much to buy, or how much of " +
			"your copy to sell, and at what price.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, PortSkipped},
		Items:   &engine.Items{Passed: true, Fields: copyFields()},
		Params: []engine.ParamSpec{
			{
				Name: "sizing", Label: "Buy size", Kind: engine.ParamString, Default: SizeFixed,
				Options: []string{SizeFixed, SizeMultiple, SizeProportional},
				Description: "fixed: Dollars a buy. multiple: Multiple times what they spent. proportional: the " +
					"share of their portfolio they spent, of what the workflow is worth: its cash (the paper account's, " +
					"or live its wallet's pUSD) and what its holdings cost.",
			},
			{Name: "dollars", Label: "Dollars", Kind: engine.ParamDecimal, Default: "10", Description: "A fixed buy's dollars."},
			{
				Name: "multiple", Label: "Multiple", Kind: engine.ParamDecimal, Default: "0.1",
				Description: "For multiple: 0.1 buys a tenth of what they spent.",
			},
			{
				Name: "slippage", Label: "Slippage", Kind: engine.ParamDecimal, Default: "0.02",
				Description: "How much worse than their price per share a copy may pay, or take when it sells.",
			},
		},
	}
}

func copyFields() []engine.Field {
	opt := func(f engine.Field) engine.Field { return shape.Optional(f) }

	return []engine.Field{
		opt(engine.Field{
			Name: "copy", Kind: engine.FieldObject, Description: "The order that copies the trade, for Place order.",
			Fields: []engine.Field{
				shape.OneOf("side", "buy or sell.", string(domain.SideBuy), string(domain.SideSell)),
				shape.Text("tokenId", "The outcome's token ID."),
				shape.Text("marketId", "The market's condition ID."),
				shape.Decimal("price", "The limit: their price, worse by at most Slippage, on the market's tick."),
				shape.Nullable(shape.Decimal("dollars", "A buy's dollars; null for a sell.")),
				shape.Nullable(shape.Decimal("shares", "A sell's shares; null for a buy.")),
				shape.OneOf("type", "FAK: what the book has within the price, at once.", string(domain.OrderFAK)),
			},
		}),
		opt(engine.Field{
			Name: "skipped", Kind: engine.FieldObject, Description: "Out of skipped: why there is no copy.",
			Fields: []engine.Field{shape.Text("reason", "Why, like nothing held to sell.")},
		}),
	}
}

func (c *CopySize) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		t, err := readTrade(item.JSON)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		order, skip, err := c.size(ctx, rc, p, t)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		j := maps.Clone(item.JSON)

		if skip != "" {
			j["skipped"] = map[string]any{"reason": skip}
			out.Add(PortSkipped, engine.Item{JSON: j, Source: []engine.ItemRef{src}})

			continue
		}

		j["copy"] = order
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// size returns the copy's order, or why there is none. The error is for what could not
// be looked up, which fails the item.
func (c *CopySize) size(ctx context.Context, rc engine.RunContext, p engine.Params, t trade) (map[string]any, string, error) {
	slippage, err := p.Decimal("slippage")
	if err != nil {
		return nil, "", err
	}

	// A share of their buys (D141); their sells always, so no copy outstays theirs.
	if t.side == domain.SideBuy && t.take != nil && !chance.Taken(rc.WorkflowID, rc.NodeID, t.txHash, *t.take) {
		return nil, fmt.Sprintf("not taken: %s's Take %% is %s", t.label, t.take), nil
	}

	if t.marketID == "" {
		if t.marketID, err = c.marketOf(ctx, t.tokenID); err != nil {
			return nil, "", err
		}
	}

	rules, err := c.src.Rules.TradingRules(ctx, t.marketID)
	if err != nil {
		return nil, "", fmt.Errorf("the market's rules: %w", err)
	}

	order := map[string]any{
		"side": string(t.side), "tokenId": string(t.tokenID), "marketId": string(t.marketID),
		"dollars": nil, "shares": nil, "type": string(domain.OrderFAK),
	}

	if t.side == domain.SideBuy {
		dollars, skip, err := c.buyDollars(ctx, rc, p, t)
		if err != nil || skip != "" {
			return nil, skip, err
		}

		price := onTick(t.price.Add(slippage), rules.TickSize, false)
		if skip := under(dollars.Div(price), rules); skip != "" {
			return nil, fmt.Sprintf("a copy of $%s is %s", dollars.StringFixed(2), skip), nil
		}

		order["price"], order["dollars"] = price, dollars

		return order, "", nil
	}

	shares, skip, err := c.sellShares(ctx, rc, t)
	if err != nil || skip != "" {
		return nil, skip, err
	}

	if skip := under(shares, rules); skip != "" {
		return nil, fmt.Sprintf("selling %s shares is %s", shares, skip), nil
	}

	order["price"], order["shares"] = onTick(t.price.Sub(slippage), rules.TickSize, true), shares

	return order, "", nil
}

// buyDollars sizes a buy (D97): by its rule, scaled by the trader's Size × and capped by
// their Max $ (D83).
func (c *CopySize) buyDollars(ctx context.Context, rc engine.RunContext, p engine.Params, t trade) (decimal.Decimal, string, error) {
	sizing, err := p.String("sizing")
	if err != nil {
		return decimal.Zero, "", err
	}

	var dollars decimal.Decimal

	switch sizing {
	case SizeFixed:
		if dollars, err = p.Decimal("dollars"); err != nil {
			return decimal.Zero, "", err
		}

	case SizeMultiple:
		multiple, err := p.Decimal("multiple")
		if err != nil {
			return decimal.Zero, "", err
		}

		dollars = t.dollars.Mul(multiple)

	case SizeProportional:
		theirs, err := c.src.Portfolios.PortfolioValue(ctx, t.trader)
		if err != nil {
			return decimal.Zero, "", fmt.Errorf("their portfolio: %w", err)
		}

		if !theirs.IsPositive() {
			return decimal.Zero, "their portfolio is worth nothing the Data API knows of, so there is no share of it", nil
		}

		worth, err := c.src.Accounts.Worth(ctx, rc.OwnerID, rc.WorkflowID)
		if err != nil {
			return decimal.Zero, "", err
		}

		dollars = decimal.Min(t.dollars.Div(theirs), one).Mul(worth)

	default:
		return decimal.Zero, "", fmt.Errorf("%w %q: %q is not one of fixed, multiple, proportional", engine.ErrInvalidParam, "sizing", sizing)
	}

	if t.scale != nil {
		dollars = dollars.Mul(*t.scale)
	}

	if t.maxDollars != nil && dollars.GreaterThan(*t.maxDollars) {
		dollars = *t.maxDollars
	}

	dollars = dollars.RoundDown(2)
	if dollars.LessThan(hundredth) {
		return decimal.Zero, "the copy comes to less than a cent", nil
	}

	return dollars, "", nil
}

// sellShares is the same share of the copy as they sold of theirs (D94): their holding
// before the sale is what the Data API says they hold now, and what they sold.
func (c *CopySize) sellShares(ctx context.Context, rc engine.RunContext, t trade) (decimal.Decimal, string, error) {
	mode, err := c.src.Accounts.Mode(ctx, rc.WorkflowID)
	if err != nil {
		return decimal.Zero, "", err
	}

	held, err := c.src.Books.Holding(ctx, domain.HoldingKey{WorkflowID: rc.WorkflowID, Mode: mode, Trader: t.trader, TokenID: t.tokenID})
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return decimal.Zero, "", err
	}

	if !held.Shares.IsPositive() {
		return decimal.Zero, fmt.Sprintf("nothing held copying %s to sell", t.label), nil
	}

	page, err := c.src.Positions.Positions(ctx, ports.PositionFilter{Wallet: t.trader, MarketIDs: []domain.MarketID{t.marketID}})
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("their positions: %w", err)
	}

	left := decimal.Zero
	for _, pos := range page.Items {
		if pos.TokenID == t.tokenID {
			left = pos.Size
		}
	}

	if !left.IsPositive() { // they sold all they had
		return held.Shares, "", nil
	}

	share := t.shares.Div(left.Add(t.shares))

	return held.Shares.Mul(share).RoundDown(2), "", nil
}

func (c *CopySize) marketOf(ctx context.Context, token domain.TokenID) (domain.MarketID, error) {
	page, err := c.src.Markets.ListMarkets(ctx, ports.MarketFilter{TokenIDs: []domain.TokenID{token}, Limit: 1})
	if err != nil {
		return "", fmt.Errorf("the token's market: %w", err)
	}

	if len(page.Items) == 0 {
		return "", fmt.Errorf("no market trades token %s", token)
	}

	return page.Items[0].ID, nil
}

// onTick puts a price on the market's tick, the way that keeps a copy within its
// slippage: a buy's down, a sell's up, and inside the prices the market takes.
func onTick(price, tick decimal.Decimal, up bool) decimal.Decimal {
	if !tick.IsPositive() {
		return price
	}

	steps := price.Div(tick).Floor()
	if up {
		steps = price.Div(tick).Ceil()
	}

	return decimal.Max(tick, decimal.Min(one.Sub(tick), steps.Mul(tick)))
}

// under says how shares fall short of the market's least order, if they do (D98).
func under(shares decimal.Decimal, rules domain.TradingRules) string {
	if least := rules.MinOrderSize; least.IsPositive() && shares.LessThan(least) {
		return fmt.Sprintf("under the market's least order of %s shares", least)
	}

	return ""
}

// trade is a Wallet activity item, as far as copying it needs.
type trade struct {
	side            domain.Side
	tokenID         domain.TokenID
	marketID        domain.MarketID
	price           decimal.Decimal
	dollars, shares decimal.Decimal
	trader          domain.WalletAddress
	label           string
	txHash          string
	scale           *decimal.Decimal
	maxDollars      *decimal.Decimal
	take            *decimal.Decimal
}

var errNotATrade = errors.New("not a trade of Wallet activity: Copy sizing copies those")

func readTrade(j map[string]any) (trade, error) {
	item := engine.Params(j)

	var (
		t   trade
		err error
	)

	side, _ := item.String("side")
	t.side = domain.Side(side)

	token, _ := item.String("tokenId")
	t.tokenID = domain.TokenID(token)

	market, _ := item.String("marketId")
	t.marketID = domain.MarketID(market)

	t.txHash, _ = item.String("txHash")

	if t.price, err = item.Decimal("price"); err != nil {
		return t, errNotATrade
	}

	if t.dollars, err = item.Decimal("dollars"); err != nil {
		return t, errNotATrade
	}

	if t.shares, err = item.Decimal("shares"); err != nil {
		return t, errNotATrade
	}

	who, ok := j["trader"].(map[string]any)
	if !ok || (t.side != domain.SideBuy && t.side != domain.SideSell) || t.tokenID == "" {
		return t, errNotATrade
	}

	row := engine.Params(who)

	address, err := row.String("address")
	if err != nil {
		return t, errNotATrade
	}

	if t.trader, err = domain.ParseAddress(address); err != nil {
		return t, errNotATrade
	}

	t.label, _ = row.String("label")

	for name, to := range map[string]**decimal.Decimal{"scale": &t.scale, "maxDollars": &t.maxDollars, "take": &t.take} {
		if row[name] == nil {
			continue
		}

		d, err := row.Decimal(name)
		if err != nil {
			return t, fmt.Errorf("the trader's %s: %w", name, err)
		}

		*to = &d
	}

	return t, nil
}
