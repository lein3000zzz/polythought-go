package trading

import (
	"fmt"
	"math"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

var (
	one       = decimal.NewFromInt(1)
	hundredth = decimal.RequireFromString("0.01") // the smallest step of shares, and of dollars
)

// shareDecimals and centDecimals are what the CLOB takes: sizes to 2 decimals, dollars
// to cents.
const (
	shareDecimals = 2
	centDecimals  = 2
	feeDecimals   = 5
)

// request checks an order before anything is looked up, and evens its amounts to what
// the CLOB takes. A reason is why it is refused.
func request(req domain.OrderRequest) (domain.OrderRequest, string) {
	req.Shares = req.Shares.RoundDown(shareDecimals)
	req.Dollars = req.Dollars.RoundDown(centDecimals)

	switch {
	case req.TokenID == "":
		return req, "it names no outcome token"
	case req.Side != domain.SideBuy && req.Side != domain.SideSell:
		return req, fmt.Sprintf("%q is not a side: buy or sell", req.Side)
	case !req.Price.IsPositive() || !req.Price.LessThan(one):
		return req, fmt.Sprintf("a price of %s is not between 0 and 1", req.Price)
	case req.Side == domain.SideSell && !req.Dollars.IsZero():
		return req, "a sell is of shares, not dollars"
	case req.Shares.IsPositive() == req.Dollars.IsPositive():
		return req, "it needs shares, or for a buy dollars, of at least 0.01, not both"
	}

	switch req.Type {
	case domain.OrderFOK, domain.OrderFAK, domain.OrderGTC, domain.OrderGTD:
	default:
		return req, fmt.Sprintf("%q is not an order type: FOK, FAK, GTC or GTD", req.Type)
	}

	if req.PostOnly && !req.Type.Rests() {
		return req, "only orders that rest on the book, GTC and GTD, can be post-only"
	}

	return req, ""
}

// keepsRules checks an order against its market: prices on the tick, sizes at least the
// least it takes. A reason is why it is refused.
func keepsRules(req domain.OrderRequest, rules domain.TradingRules) string {
	if tick := rules.TickSize; tick.IsPositive() {
		if !req.Price.Mod(tick).IsZero() || req.Price.LessThan(tick) || req.Price.GreaterThan(one.Sub(tick)) {
			return fmt.Sprintf("a price of %s is not on the market's ticks of %s, from %s to %s",
				req.Price, tick, tick, one.Sub(tick))
		}
	}

	shares := req.Shares
	if shares.IsZero() {
		shares = req.Dollars.Div(req.Price) // what a buy of dollars gets at its limit, at least
	}

	if least := rules.MinOrderSize; least.IsPositive() && shares.LessThan(least) {
		return fmt.Sprintf("%s shares is under the market's least order of %s", shares.RoundDown(shareDecimals), least)
	}

	return ""
}

// fill matches an order against the book, as the CLOB would at once: the best price
// first, as far as its limit allows. A FOK that cannot fill wholly fills nothing; a FAK
// fills what it can. The fills pay the market's taker fees (D90).
func fill(req domain.OrderRequest, book domain.OrderBook, fees domain.FeeSchedule) ([]domain.OrderFill, domain.OrderStatus, string) {
	levels, within := book.Asks, func(p decimal.Decimal) bool { return p.LessThanOrEqual(req.Price) }
	if req.Side == domain.SideSell {
		levels, within = book.Bids, func(p decimal.Decimal) bool { return p.GreaterThanOrEqual(req.Price) }
	}

	shares, dollars := req.Shares, req.Dollars

	var (
		fills []domain.OrderFill
		last  decimal.Decimal
	)

	for _, l := range levels {
		if !within(l.Price) || (shares.IsZero() && dollars.IsZero()) {
			break
		}

		take := shares
		if take.IsZero() { // a buy of dollars: as many shares as they buy here
			take = dollars.Div(l.Price).RoundDown(shareDecimals)
		}

		take = decimal.Min(take, l.Size).RoundDown(shareDecimals)
		if !take.IsPositive() {
			break
		}

		cost := take.Mul(l.Price)
		fills = append(fills, domain.OrderFill{Price: l.Price, Shares: take, Dollars: cost, Fee: fee(take, l.Price, fees)})
		last = l.Price

		if req.Shares.IsPositive() {
			shares = shares.Sub(take)
		} else {
			dollars = dollars.Sub(cost)
		}
	}

	// Wholly filled: no shares left, or dollars too few for another hundredth of a share.
	whole := req.Shares.IsPositive() && shares.IsZero() ||
		req.Dollars.IsPositive() && last.IsPositive() && dollars.LessThan(last.Mul(hundredth))

	switch {
	case whole:
		return fills, domain.OrderFilled, ""
	case req.Type == domain.OrderFOK:
		return nil, domain.OrderUnfilled, "the book could not fill it wholly within its price"
	case len(fills) == 0:
		return nil, domain.OrderUnfilled, "the book had nothing within its price"
	default:
		return fills, domain.OrderPartial, "the book filled part of it within its price; the rest was cancelled"
	}
}

// fee is a taker's fee on a fill: shares × rate × (p × (1 − p))^exponent, rounded to 5
// decimals, as Polymarket charges it.
func fee(shares, price decimal.Decimal, s domain.FeeSchedule) decimal.Decimal {
	if !s.Rate.IsPositive() {
		return decimal.Zero
	}

	base := price.Mul(one.Sub(price))

	if e := s.Exponent; !e.IsZero() && !e.Equal(one) {
		b, _ := base.Float64()
		x, _ := e.Float64()
		base = decimal.NewFromFloat(math.Pow(b, x))
	}

	return shares.Mul(s.Rate).Mul(base).Round(feeDecimals)
}

// apply works out what a filled order does to the books: the cash it moves, and the
// holding it leaves, its cost averaged. A sell's profit or loss is what it brought, fees
// off, less the cost of the shares it sold.
func apply(o domain.PlacedOrder, h domain.Holding) (cash decimal.Decimal, after domain.Holding) {
	shares, dollars, fees := o.Totals()

	after = h
	after.TraderLabel, after.MarketID, after.UpdatedAt = o.TraderLabel, o.MarketID, o.CreatedAt

	if o.Side == domain.SideBuy {
		after.Shares = h.Shares.Add(shares)
		after.Cost = h.Cost.Add(dollars).Add(fees)

		return dollars.Add(fees).Neg(), after
	}

	proceeds := dollars.Sub(fees)
	sold := h.Cost
	if shares.LessThan(h.Shares) {
		sold = h.Cost.Mul(shares).DivRound(h.Shares, 6)
	}

	after.Shares = h.Shares.Sub(shares)
	after.Cost = h.Cost.Sub(sold)
	after.Realized = h.Realized.Add(proceeds).Sub(sold)

	return proceeds, after
}
