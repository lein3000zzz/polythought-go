package trading

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func buy(typ domain.OrderType, price, shares, dollars string) domain.OrderRequest {
	r := domain.OrderRequest{Mode: domain.ModePaper, TokenID: "t", Side: domain.SideBuy, Type: typ, Price: d(price)}
	if shares != "" {
		r.Shares = d(shares)
	}

	if dollars != "" {
		r.Dollars = d(dollars)
	}

	return r
}

func TestRequest(t *testing.T) {
	for name, tc := range map[string]struct {
		req  domain.OrderRequest
		want string
	}{
		"a buy of dollars":         {buy(domain.OrderFAK, "0.5", "", "10"), ""},
		"a buy of shares":          {buy(domain.OrderFOK, "0.5", "10", ""), ""},
		"both":                     {buy(domain.OrderFOK, "0.5", "10", "5"), "not both"},
		"neither":                  {buy(domain.OrderFOK, "0.5", "", ""), "at least 0.01"},
		"too little to round":      {buy(domain.OrderFOK, "0.5", "0.004", ""), "at least 0.01"},
		"a price out of range":     {buy(domain.OrderFOK, "1", "10", ""), "not between 0 and 1"},
		"a resting order on paper": {buy(domain.OrderGTC, "0.5", "10", ""), ""},
		"post-only and resting": {func() domain.OrderRequest {
			r := buy(domain.OrderGTD, "0.5", "10", "")
			r.PostOnly = true
			return r
		}(), ""},
		"no type": {buy("", "0.5", "10", ""), "not an order type"},
		"a sell of dollars": {func() domain.OrderRequest {
			r := buy(domain.OrderFAK, "0.5", "", "10")
			r.Side = domain.SideSell
			return r
		}(), "not dollars"},
		"post-only on paper": {func() domain.OrderRequest {
			r := buy(domain.OrderFAK, "0.5", "10", "")
			r.PostOnly = true
			return r
		}(), "post-only"},
	} {
		t.Run(name, func(t *testing.T) {
			_, reason := request(tc.req)
			if tc.want == "" {
				assert.Empty(t, reason)
			} else {
				assert.Contains(t, reason, tc.want)
			}
		})
	}

	got, _ := request(buy(domain.OrderFAK, "0.5", "10.129", ""))
	assert.Equal(t, "10.12", got.Shares.String(), "sizes to hundredths, down")
}

func TestKeepsRules(t *testing.T) {
	rules := domain.TradingRules{TickSize: d("0.01"), MinOrderSize: d("5")}

	assert.Empty(t, keepsRules(buy(domain.OrderFAK, "0.55", "5", ""), rules))
	assert.Contains(t, keepsRules(buy(domain.OrderFAK, "0.555", "10", ""), rules), "not on the market's ticks of 0.01")
	assert.Contains(t, keepsRules(buy(domain.OrderFAK, "0.995", "10", ""), rules), "not on the market's ticks")
	assert.Contains(t, keepsRules(buy(domain.OrderFAK, "0.5", "4.99", ""), rules), "4.99 shares is under the market's least order of 5")
	assert.Contains(t, keepsRules(buy(domain.OrderFAK, "0.5", "", "2"), rules), "4 shares is under", "dollars at the limit")
}

var book = domain.OrderBook{
	Asks: []domain.Level{{Price: d("0.50"), Size: d("10")}, {Price: d("0.52"), Size: d("20")}, {Price: d("0.60"), Size: d("100")}},
	Bids: []domain.Level{{Price: d("0.48"), Size: d("5")}, {Price: d("0.45"), Size: d("50")}},
}

var noFees = domain.FeeSchedule{}

func TestFill(t *testing.T) {
	t.Run("a buy of dollars takes the best asks first, up to its limit", func(t *testing.T) {
		fills, status, _ := fill(buy(domain.OrderFAK, "0.52", "", "10"), book, noFees, time.Time{})

		assert.Equal(t, domain.OrderFilled, status)
		require.Len(t, fills, 2)
		assert.Equal(t, "10", fills[0].Shares.String())
		assert.Equal(t, "5", fills[0].Dollars.String())
		assert.Equal(t, "9.61", fills[1].Shares.String(), "$5 more at 0.52, to the hundredth")
		assert.Equal(t, "4.9972", fills[1].Dollars.String())
	})

	t.Run("a FOK the book cannot fill within its price fills nothing", func(t *testing.T) {
		fills, status, reason := fill(buy(domain.OrderFOK, "0.52", "40", ""), book, noFees, time.Time{})

		assert.Equal(t, domain.OrderUnfilled, status)
		assert.Empty(t, fills)
		assert.Contains(t, reason, "wholly")
	})

	t.Run("a FAK fills what it can and cancels the rest", func(t *testing.T) {
		fills, status, _ := fill(buy(domain.OrderFAK, "0.52", "40", ""), book, noFees, time.Time{})

		assert.Equal(t, domain.OrderPartial, status)
		require.Len(t, fills, 2)
		assert.Equal(t, "20", fills[1].Shares.String())
	})

	t.Run("a sell takes the best bids", func(t *testing.T) {
		sell := buy(domain.OrderFOK, "0.45", "20", "")
		sell.Side = domain.SideSell

		fills, status, _ := fill(sell, book, noFees, time.Time{})
		assert.Equal(t, domain.OrderFilled, status)
		require.Len(t, fills, 2)
		assert.Equal(t, "0.48", fills[0].Price.String())
		assert.Equal(t, "15", fills[1].Shares.String())
	})

	t.Run("nothing within its price", func(t *testing.T) {
		_, status, reason := fill(buy(domain.OrderFAK, "0.40", "10", ""), book, noFees, time.Time{})
		assert.Equal(t, domain.OrderUnfilled, status)
		assert.Contains(t, reason, "nothing within its price")
	})

	t.Run("fills pay the taker fee", func(t *testing.T) {
		fills, _, _ := fill(buy(domain.OrderFOK, "0.50", "10", ""), book, domain.FeeSchedule{Rate: d("0.04"), Exponent: d("1")}, time.Time{})
		require.Len(t, fills, 1)
		assert.Equal(t, "0.1", fills[0].Fee.String(), "10 × 0.04 × 0.5 × 0.5")
	})
}

func TestFee(t *testing.T) {
	crypto := domain.FeeSchedule{Rate: d("0.07"), Exponent: d("1")}

	assert.Equal(t, "1.75", fee(d("100"), d("0.5"), crypto).String(), "the docs' peak")
	assert.Equal(t, "0.00069", fee(d("1"), d("0.01"), crypto).String(), "0.000693, to 5 decimals")
	assert.True(t, fee(d("0.01"), d("0.001"), crypto).IsZero(), "too small to charge")
	assert.True(t, fee(d("100"), d("0.5"), domain.FeeSchedule{}).IsZero(), "fee-free markets")
	assert.Equal(t, "0.875", fee(d("100"), d("0.5"), domain.FeeSchedule{Rate: d("0.07"), Exponent: d("1.5")}).String(),
		"the price part raised to the exponent")
}

func TestApply(t *testing.T) {
	order := func(side domain.Side, shares, dollars, fee string) domain.PlacedOrder {
		return domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{Side: side, TraderLabel: "Alice", MarketID: "0xm"},
			Fills:        []domain.OrderFill{{Shares: d(shares), Dollars: d(dollars), Fee: d(fee)}},
		}
	}

	cash, h := apply(order(domain.SideBuy, "20", "10", "0.2"), domain.Holding{})
	assert.Equal(t, "-10.2", cash.String(), "the fee on top of a buy")
	assert.Equal(t, "20", h.Shares.String())
	assert.Equal(t, "10.2", h.Cost.String())
	assert.Equal(t, "Alice", h.TraderLabel)

	cash, h = apply(order(domain.SideSell, "5", "4", "0.1"), h)
	assert.Equal(t, "3.9", cash.String(), "the fee out of a sell")
	assert.Equal(t, "15", h.Shares.String())
	assert.Equal(t, "7.65", h.Cost.String(), "a quarter of the cost goes with a quarter of the shares")
	assert.Equal(t, "1.35", h.Realized.String(), "3.9 brought, 2.55 it cost")

	_, h = apply(order(domain.SideSell, "15", "6", "0"), h)
	assert.True(t, h.Shares.IsZero())
	assert.True(t, h.Cost.IsZero(), "sold out: no cost left")
	assert.Equal(t, "-0.3", h.Realized.String())
}
