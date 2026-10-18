package trading

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	RiskGuardType = "trading.riskGuard"
	PortBlocked   = "blocked"
)

// day is how far back the daily cap counts (D102).
const day = 24 * time.Hour

var _ engine.Node = (*RiskGuard)(nil)

var errNoCopy = errors.New("no copy to guard: put Copy sizing before it")

// RiskGuard holds copies of buys to the workflow's limits: caps a copy, a market and a
// day, a price band and categories. A copy over a cap is shrunk to fit (D100); one it
// cannot let through goes out of blocked, saying why (D96). Sells always go ahead (D101).
type RiskGuard struct {
	books   Books
	rules   ports.TradingRulesReader
	markets ports.MarketReader // a market's tags, for categories
	now     func() time.Time
}

func NewRiskGuard(books Books, rules ports.TradingRulesReader, markets ports.MarketReader, now func() time.Time) *RiskGuard {
	return &RiskGuard{books: books, rules: rules, markets: markets, now: now}
}

func (*RiskGuard) Type() engine.NodeType {
	return engine.NodeType{
		Name:  RiskGuardType,
		Label: "Risk guard",
		Description: "Holds copies of buys to the workflow's limits: caps a copy, a market and a day, a price " +
			"band and categories. Copies over a cap are made smaller; sells always go ahead.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, PortBlocked},
		Items:   &engine.Items{Passed: true, Fields: guardFields()},
		Params: []engine.ParamSpec{
			{Name: "maxPerCopy", Label: "Max $ a copy", Kind: engine.ParamDecimal, Description: "The most one copy spends."},
			{
				Name: "maxPerMarket", Label: "Max $ a market", Kind: engine.ParamDecimal,
				Description: "The most the workflow holds in one market, at cost, all traders' copies together.",
			},
			{
				Name: "maxPerDay", Label: "Max $ a day", Kind: engine.ParamDecimal,
				Description: "The most the workflow's buys spend in 24 hours.",
			},
			{Name: "minPrice", Label: "Least price", Kind: engine.ParamDecimal, Description: "Buys at a lower price are blocked."},
			{Name: "maxPrice", Label: "Most price", Kind: engine.ParamDecimal, Description: "Buys at a higher price are blocked."},
			{
				Name: "categories", Label: "Only categories", Kind: engine.ParamList,
				Description: "Buys only in markets with one of these tags, like Politics; empty: any.",
			},
			{
				Name: "notCategories", Label: "Not categories", Kind: engine.ParamList,
				Description: "No buys in markets with any of these tags, like Sports.",
			},
		},
	}
}

func guardFields() []engine.Field {
	return []engine.Field{
		shape.Optional(engine.Field{
			Name: "guard", Kind: engine.FieldObject, Description: "When a cap made the copy smaller: by how much, and which.",
			Fields: []engine.Field{
				shape.Decimal("from", "The copy's dollars before."),
				shape.Text("reason", "Which cap, and what it left."),
			},
		}),
		shape.Optional(engine.Field{
			Name: "blocked", Kind: engine.FieldObject, Description: "Out of blocked: why the copy may not go ahead.",
			Fields: []engine.Field{shape.Text("reason", "Which limit, and how it was reached.")},
		}),
	}
}

// limits are the guard's settings: nil when not set.
type limits struct {
	perCopy, perMarket, perDay *decimal.Decimal
	minPrice, maxPrice         *decimal.Decimal
	only, not                  []string
}

func (g *RiskGuard) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	// What this run's copies would add, so the ones after them see it too.
	var (
		added    decimal.Decimal
		inMarket = map[domain.MarketID]decimal.Decimal{}
		spent    *decimal.Decimal
		held     map[domain.MarketID]decimal.Decimal
	)

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		l, err := readLimits(p)
		if err != nil {
			return nil, err
		}

		copied, ok := item.JSON["copy"].(map[string]any)
		if !ok {
			out.Fail(item, src, errNoCopy)
			continue
		}

		order := engine.Params(copied)
		side, _ := order.String("side")

		if domain.Side(side) != domain.SideBuy { // a sell lowers what is at risk (D101)
			out.Add(engine.DefaultPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{src}})
			continue
		}

		c, err := readCopy(order)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		if spent == nil && l.perDay != nil {
			s, err := g.books.Spent(ctx, rc.WorkflowID, mode, g.now().Add(-day))
			if err != nil {
				out.Fail(item, src, err)
				continue
			}

			spent = &s
		}

		if held == nil && l.perMarket != nil {
			if held, err = g.held(ctx, rc.WorkflowID); err != nil {
				out.Fail(item, src, err)
				continue
			}
		}

		used := usage{day: added, market: inMarket[c.market]}
		if spent != nil {
			used.day = used.day.Add(*spent)
		}

		used.market = used.market.Add(held[c.market])

		dollars, note, block, err := g.check(ctx, l, c, used)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		j := maps.Clone(item.JSON)

		if block != "" {
			j["blocked"] = map[string]any{"reason": block}
			out.Add(PortBlocked, engine.Item{JSON: j, Source: []engine.ItemRef{src}})

			continue
		}

		if note != "" {
			shrunk := maps.Clone(copied)
			shrunk["dollars"] = dollars
			j["copy"] = shrunk
			j["guard"] = map[string]any{"from": c.dollars, "reason": note}
		}

		added = added.Add(dollars)
		inMarket[c.market] = inMarket[c.market].Add(dollars)

		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// usage is what counts against the caps before a copy: the day's buys, and what the
// workflow holds in its market.
type usage struct {
	day, market decimal.Decimal
}

type copyOrder struct {
	market  domain.MarketID
	price   decimal.Decimal
	dollars decimal.Decimal
}

// check says what a copy may spend, a note when a cap made it less, or why it is blocked.
func (g *RiskGuard) check(ctx context.Context, l limits, c copyOrder, used usage) (decimal.Decimal, string, string, error) {
	if l.minPrice != nil && c.price.LessThan(*l.minPrice) {
		return decimal.Zero, "", fmt.Sprintf("a price of %s is under the least, %s", c.price, *l.minPrice), nil
	}

	if l.maxPrice != nil && c.price.GreaterThan(*l.maxPrice) {
		return decimal.Zero, "", fmt.Sprintf("a price of %s is over the most, %s", c.price, *l.maxPrice), nil
	}

	if len(l.only) > 0 || len(l.not) > 0 {
		m, err := g.markets.GetMarket(ctx, c.market)
		if err != nil {
			return decimal.Zero, "", "", fmt.Errorf("the market's categories: %w", err)
		}

		if block := categories(l, m.Tags); block != "" {
			return decimal.Zero, "", block, nil
		}
	}

	dollars, note := c.dollars, ""

	for _, lim := range []struct {
		max  *decimal.Decimal
		used decimal.Decimal
		name string
	}{
		{l.perCopy, decimal.Zero, "a copy's"},
		{l.perMarket, used.market, "the market's"},
		{l.perDay, used.day, "the day's"},
	} {
		if lim.max == nil {
			continue
		}

		left := lim.max.Sub(lim.used).RoundDown(2)
		if !left.IsPositive() {
			return decimal.Zero, "", fmt.Sprintf("%s cap of $%s is reached: $%s already", lim.name, lim.max.StringFixed(2),
				lim.used.StringFixed(2)), nil
		}

		if dollars.GreaterThan(left) {
			dollars, note = left, fmt.Sprintf("%s cap of $%s leaves $%s", lim.name, lim.max.StringFixed(2), left.StringFixed(2))
		}
	}

	if note != "" {
		rules, err := g.rules.TradingRules(ctx, c.market)
		if err != nil {
			return decimal.Zero, "", "", fmt.Errorf("the market's rules: %w", err)
		}

		if skip := under(dollars.Div(c.price), rules); skip != "" {
			return decimal.Zero, "", fmt.Sprintf("%s, and $%s is %s", note, dollars.StringFixed(2), skip), nil
		}
	}

	return dollars, note, "", nil
}

// categories says why a market's tags keep a copy out, if they do: matched by name,
// whatever the case.
func categories(l limits, tags []string) string {
	has := func(want string) bool {
		return slices.ContainsFunc(tags, func(t string) bool { return strings.EqualFold(strings.TrimSpace(t), strings.TrimSpace(want)) })
	}

	for _, c := range l.not {
		if has(c) {
			return fmt.Sprintf("its market is in %s, which Not categories leaves out", c)
		}
	}

	if len(l.only) > 0 && !slices.ContainsFunc(l.only, has) {
		return fmt.Sprintf("its market is in none of %s", strings.Join(l.only, ", "))
	}

	return ""
}

// held is what the workflow holds in each market, at cost, all traders together (D103).
func (g *RiskGuard) held(ctx context.Context, w domain.WorkflowID) (map[domain.MarketID]decimal.Decimal, error) {
	holdings, err := g.books.Holdings(ctx, w)
	if err != nil {
		return nil, err
	}

	out := map[domain.MarketID]decimal.Decimal{}
	for _, h := range holdings {
		if h.Mode == mode {
			out[h.MarketID] = out[h.MarketID].Add(h.Cost)
		}
	}

	return out, nil
}

func readCopy(order engine.Params) (copyOrder, error) {
	market, _ := order.String("marketId")
	c := copyOrder{market: domain.MarketID(market)}

	var err error
	if c.price, err = order.Decimal("price"); err != nil {
		return c, fmt.Errorf("the copy's price: %w", err)
	}

	if c.dollars, err = order.Decimal("dollars"); err != nil {
		return c, fmt.Errorf("the copy's dollars: %w", err)
	}

	return c, nil
}

func readLimits(p engine.Params) (limits, error) {
	var l limits

	for name, to := range map[string]**decimal.Decimal{
		"maxPerCopy": &l.perCopy, "maxPerMarket": &l.perMarket, "maxPerDay": &l.perDay,
		"minPrice": &l.minPrice, "maxPrice": &l.maxPrice,
	} {
		if p[name] == nil {
			continue
		}

		d, err := p.Decimal(name)
		if err != nil {
			return l, err
		}

		*to = &d
	}

	for name, to := range map[string]*[]string{"categories": &l.only, "notCategories": &l.not} {
		if p[name] == nil {
			continue
		}

		list, err := p.Strings(name)
		if err != nil {
			return l, err
		}

		*to = list
	}

	return l, nil
}
