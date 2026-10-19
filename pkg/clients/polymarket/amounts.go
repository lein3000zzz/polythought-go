package polymarket

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/shopspring/decimal"
)

// An order's amounts are in millionths (e6) of dollars or shares, as the exchange counts
// them. They follow @polymarket/client (D149): exact, rounded as it rounds, and what is
// off the market's grid is refused, not rounded onto it (D150).

// ErrOffGrid is a price or a size the market's grid does not take.
var ErrOffGrid = errors.New("polymarket: off the market's grid")

// Amounts are what an order gives (Maker) and gets (Taker), in millionths: dollars for
// the side that pays, shares for the side that sells.
type Amounts struct {
	Maker, Taker uint64
}

// grid is how many decimals a market's sizes and amounts have, by its tick size: sizes
// always 2, amounts as many as price and size together.
type grid struct {
	tick   decimal.Decimal
	size   int32
	amount int32
}

var grids = map[string]int32{"0.1": 3, "0.01": 4, "0.005": 5, "0.0025": 6, "0.001": 5, "0.0001": 6}

func gridOf(tick decimal.Decimal) (grid, error) {
	amount, ok := grids[tick.String()]
	if !ok {
		return grid{}, fmt.Errorf("%w: no tick size %s", ErrOffGrid, tick)
	}

	return grid{tick: tick, size: 2, amount: amount}, nil
}

// price checks a price is on the grid, within [tick, 1 - tick], as the exchange does.
func (g grid) price(p decimal.Decimal) error {
	switch {
	case p.LessThan(g.tick) || p.GreaterThan(decimal.NewFromInt(1).Sub(g.tick)):
		return fmt.Errorf("%w: a price of %s is outside %s to %s", ErrOffGrid, p, g.tick, decimal.NewFromInt(1).Sub(g.tick))
	case !p.Mod(g.tick).IsZero():
		return fmt.Errorf("%w: a price of %s is not on the tick of %s", ErrOffGrid, p, g.tick)
	}

	return nil
}

// quantity checks a size, or a dollar amount, has at most the grid's 2 decimals.
func (g grid) quantity(what string, q decimal.Decimal) error {
	switch {
	case !q.IsPositive():
		return fmt.Errorf("%w: %s of %s", ErrOffGrid, what, q)
	case !q.Truncate(g.size).Equal(q):
		return fmt.Errorf("%w: %s of %s has more than %d decimals", ErrOffGrid, what, q, g.size)
	}

	return nil
}

// LimitAmounts are a limit order's amounts: size shares at price each, on a market of
// tick. A buy gives price × size dollars and gets size shares; a sell the other way.
func LimitAmounts(side Side, price, size, tick decimal.Decimal) (Amounts, error) {
	g, err := gridOf(tick)
	if err != nil {
		return Amounts{}, err
	}

	if err := g.price(price); err != nil {
		return Amounts{}, err
	}

	if err := g.quantity("a size", size); err != nil {
		return Amounts{}, err
	}

	shares, worth := scaled(size), g.times(scaled(size), scaled(price), false)

	switch side {
	case SideBuy:
		return amounts(worth, shares)
	case SideSell:
		return amounts(shares, worth)
	default:
		return Amounts{}, fmt.Errorf("polymarket: no side %q", side)
	}
}

// MarketAmounts are a market order's amounts, at prices no worse than price: a buy gives
// amount dollars, a sell amount shares, and each gets what that is worth at price.
// protect rounds what it gets up, as the official client does when given a worst price,
// so the order's own price stays within it.
func MarketAmounts(side Side, amount, price, tick decimal.Decimal, protect bool) (Amounts, error) {
	g, err := gridOf(tick)
	if err != nil {
		return Amounts{}, err
	}

	if err := g.price(price); err != nil {
		return Amounts{}, err
	}

	what := "dollars"
	if side == SideSell {
		what = "shares"
	}

	if err := g.quantity(what, amount); err != nil {
		return Amounts{}, err
	}

	switch side {
	case SideBuy:
		return amounts(scaled(amount), g.over(scaled(amount), scaled(price), protect))
	case SideSell:
		return amounts(scaled(amount), g.times(scaled(amount), scaled(price), protect))
	default:
		return Amounts{}, fmt.Errorf("polymarket: no side %q", side)
	}
}

var million = big.NewInt(1_000_000)

// scaled is d in millionths; d has at most 6 decimals once on the grid.
func scaled(d decimal.Decimal) *big.Int {
	return d.Shift(6).BigInt()
}

// quantum is a millionths step of the grid's amount decimals.
func (g grid) quantum() *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(6-g.amount)), nil)
}

// times is amount × price, both in millionths, to the grid's amount decimals.
func (g grid) times(amount, price *big.Int, up bool) *big.Int {
	q := g.quantum()
	den := new(big.Int).Mul(million, q)

	return new(big.Int).Mul(divide(new(big.Int).Mul(amount, price), den, up), q)
}

// over is amount ÷ price, both in millionths, to the grid's amount decimals.
func (g grid) over(amount, price *big.Int, up bool) *big.Int {
	q := g.quantum()
	den := new(big.Int).Mul(price, q)

	return new(big.Int).Mul(divide(new(big.Int).Mul(amount, million), den, up), q)
}

func divide(num, den *big.Int, up bool) *big.Int {
	quo, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if up && rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}

	return quo
}

func amounts(maker, taker *big.Int) (Amounts, error) {
	if !maker.IsUint64() || !taker.IsUint64() {
		return Amounts{}, errors.New("polymarket: amounts too large")
	}

	return Amounts{Maker: maker.Uint64(), Taker: taker.Uint64()}, nil
}
