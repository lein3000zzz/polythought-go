package trading_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func address(lower string) domain.WalletAddress {
	w, err := domain.ParseAddress(lower)
	if err != nil {
		panic(err)
	}

	return w
}

var alice = address("0x5f3c00000000000000000000000000000000a1b2")

// books is a workflow's books: how it trades, a paper account, if open, or live its
// wallet's cash, holdings, and what its buys spent of late. It is the trading service
// for the nodes too: its mode, and what it is worth.
type books struct {
	live     bool
	cash     string // live: the wallet's pUSD
	account  *domain.PaperAccount
	holdings []domain.Holding
	spent    string
	since    *time.Time // what Spent was asked
	modes    *[]domain.TradingMode
	resting  map[domain.MarketID]decimal.Decimal // what resting buys hold
}

func (b books) Resting(context.Context, domain.WorkflowID) (map[domain.MarketID]decimal.Decimal, error) {
	return b.resting, nil
}

func (b books) mode() domain.TradingMode {
	if b.live {
		return domain.ModeLive
	}

	return domain.ModePaper
}

func (b books) Mode(context.Context, domain.WorkflowID) (domain.TradingMode, error) {
	return b.mode(), nil
}

// Worth is as the trading service works it out (D97, D198): the paper account's cash,
// 1000 before it opens, or live the wallet's, and what the holdings in the mode cost.
func (b books) Worth(context.Context, domain.UserID, domain.WorkflowID) (decimal.Decimal, error) {
	worth := d("1000")

	switch {
	case b.live:
		worth = d(b.cash)
	case b.account == nil:
		return worth, nil
	default:
		worth = b.account.Cash
	}

	for _, h := range b.holdings {
		if h.Mode == b.mode() {
			worth = worth.Add(h.Cost)
		}
	}

	return worth, nil
}

func (b books) Holding(_ context.Context, k domain.HoldingKey) (domain.Holding, error) {
	for _, h := range b.holdings {
		if h.Key() == k {
			return h, nil
		}
	}

	return domain.Holding{}, domain.ErrNotFound
}

func (b books) Holdings(context.Context, domain.WorkflowID) ([]domain.Holding, error) {
	return b.holdings, nil
}

func (b books) Spent(_ context.Context, _ domain.WorkflowID, mode domain.TradingMode, since time.Time) (decimal.Decimal, error) {
	if b.since != nil {
		*b.since = since
	}

	if b.modes != nil {
		*b.modes = append(*b.modes, mode)
	}

	if b.spent == "" {
		return decimal.Zero, nil
	}

	return d(b.spent), nil
}

type rules struct{}

func (rules) TradingRules(_ context.Context, m domain.MarketID) (domain.TradingRules, error) {
	return domain.TradingRules{MarketID: m, TickSize: d("0.01"), MinOrderSize: d("5")}, nil
}

type markets struct{}

func (markets) ListMarkets(_ context.Context, f ports.MarketFilter) (ports.Page[domain.Market], error) {
	if len(f.TokenIDs) == 1 && f.TokenIDs[0] == "t-yes" {
		return ports.Page[domain.Market]{Items: []domain.Market{{ID: "0xfound"}}}, nil
	}

	return ports.Page[domain.Market]{}, nil
}

func (markets) GetMarket(_ context.Context, id domain.MarketID) (domain.Market, error) {
	if id == "0xm" {
		return domain.Market{ID: id, Tags: []string{"Politics", "World"}}, nil
	}

	return domain.Market{}, domain.ErrNotFound
}

func (markets) GetMarketBySlug(context.Context, string) (domain.Market, error) {
	return domain.Market{}, domain.ErrNotFound
}

// theirs is what the Data API says a trader holds and is worth.
type theirs struct {
	left  string // shares of t-yes they hold now
	value string
}

func (th theirs) Positions(context.Context, ports.PositionFilter) (ports.Page[domain.Position], error) {
	if th.left == "" {
		return ports.Page[domain.Position]{}, nil
	}

	return ports.Page[domain.Position]{Items: []domain.Position{{TokenID: "t-yes", Size: d(th.left)}}}, nil
}

func (th theirs) PortfolioValue(context.Context, domain.WalletAddress) (decimal.Decimal, error) {
	if th.value == "" {
		return decimal.Zero, errors.New("the data API is down")
	}

	return d(th.value), nil
}

func tradeItem(side domain.Side, price, dollars, shares string, row map[string]any) engine.Item {
	trader := map[string]any{"label": "Alice", "address": string(alice), "name": "BOOOST", "scale": nil, "maxDollars": nil}
	for k, v := range row {
		trader[k] = v
	}

	return engine.Item{JSON: map[string]any{
		"trader": trader, "side": string(side), "role": "taker", "tokenId": "t-yes", "marketId": "0xm",
		"price": d(price), "dollars": d(dollars), "shares": d(shares),
	}}
}

// sized runs Copy sizing on one item and returns the port it left by and its JSON.
func sized(t *testing.T, b books, th theirs, params map[string]any, item engine.Item) (string, map[string]any) {
	t.Helper()

	n := trading.NewCopySize(trading.Sources{
		Books: b, Accounts: b, Rules: rules{}, Markets: markets{}, Positions: th, Portfolios: th,
	})

	out, err := n.Run(context.Background(), engine.RunContext{
		WorkflowID: "wf", OwnerID: "u", NodeID: "size", Params: engine.LiteralParams(n.Type(), params),
	}, engine.Ports{engine.DefaultPort: {item}})
	require.NoError(t, err)
	require.NoError(t, shape.Check(n.Type(), out), "the fields it adds, as declared")

	for port, items := range out {
		require.Len(t, items, 1)
		return port, items[0].JSON
	}

	require.FailNow(t, "nothing came out")

	return "", nil
}

func copied(t *testing.T, j map[string]any) map[string]any {
	t.Helper()

	c, ok := j["copy"].(map[string]any)
	require.True(t, ok, "a copy: %v", j)

	return c
}

func TestCopySizingBuys(t *testing.T) {
	for name, tc := range map[string]struct {
		params  map[string]any
		row     map[string]any
		books   books
		theirs  theirs
		dollars string // of the copy
	}{
		"fixed, by default":             {dollars: "10"},
		"a multiple of what they spent": {params: map[string]any{"sizing": "multiple", "multiple": "0.1"}, dollars: "25"},
		"scaled by the trader's Size ×": {params: map[string]any{"sizing": "multiple"}, row: map[string]any{"scale": "0.5"}, dollars: "12.5"},
		"capped by the trader's Max $":  {params: map[string]any{"sizing": "multiple"}, row: map[string]any{"maxDollars": "20"}, dollars: "20"},
		"in proportion to their portfolio": {
			params: map[string]any{"sizing": "proportional"}, theirs: theirs{value: "2500"},
			books: books{account: &domain.PaperAccount{Cash: d("500")}, holdings: []domain.Holding{
				{Mode: domain.ModePaper, TokenID: "t-no", Cost: d("100")},
			}},
			dollars: "60", // they spent a tenth of theirs; the account is worth 600
		},
		"in proportion, before the account opens": {
			params: map[string]any{"sizing": "proportional"}, theirs: theirs{value: "2500"}, dollars: "100",
		},
		"in proportion, live: of the wallet's pUSD and the live holdings": {
			params: map[string]any{"sizing": "proportional"}, theirs: theirs{value: "2500"},
			books: books{live: true, cash: "200", account: &domain.PaperAccount{Cash: d("500")}, holdings: []domain.Holding{
				{Mode: domain.ModePaper, TokenID: "t-no", Cost: d("100")},
				{Mode: domain.ModeLive, TokenID: "t-no", Cost: d("50")},
			}},
			dollars: "25", // a tenth of 250: the paper books play no part
		},
	} {
		t.Run(name, func(t *testing.T) {
			port, j := sized(t, tc.books, tc.theirs, tc.params, tradeItem(domain.SideBuy, "0.503", "250", "497.01", tc.row))

			require.Equal(t, engine.DefaultPort, port, "%v", j)

			c := copied(t, j)
			assert.Equal(t, "buy", c["side"])
			assert.True(t, d(tc.dollars).Equal(c["dollars"].(decimal.Decimal)), "dollars: %v", c["dollars"])
			assert.Nil(t, c["shares"])
			assert.True(t, d("0.52").Equal(c["price"].(decimal.Decimal)), "0.503 and 2 cents, down to the tick: %v", c["price"])
			assert.Equal(t, "FAK", c["type"])
			assert.Equal(t, "BOOOST", j["trader"].(map[string]any)["name"], "the trade passes on")
		})
	}
}

func TestCopySizingSells(t *testing.T) {
	held := func(shares string) books {
		return books{holdings: []domain.Holding{{
			WorkflowID: "wf", Mode: domain.ModePaper, Trader: alice, TokenID: "t-yes", Shares: d(shares), Cost: d("40"),
		}}}
	}

	t.Run("the same share as they sold (D94)", func(t *testing.T) {
		port, j := sized(t, held("100"), theirs{left: "60"}, nil, tradeItem(domain.SideSell, "0.503", "20.12", "40", nil))

		require.Equal(t, engine.DefaultPort, port)
		c := copied(t, j)
		assert.True(t, d("40").Equal(c["shares"].(decimal.Decimal)), "40 of their 100: 40%% of the copy's 100")
		assert.Nil(t, c["dollars"])
		assert.True(t, d("0.49").Equal(c["price"].(decimal.Decimal)), "0.503 less 2 cents, up to the tick")
	})

	t.Run("all of it when they sold all of theirs", func(t *testing.T) {
		_, j := sized(t, held("33.33"), theirs{}, nil, tradeItem(domain.SideSell, "0.5", "20", "40", nil))
		assert.True(t, d("33.33").Equal(copied(t, j)["shares"].(decimal.Decimal)))
	})

	t.Run("live, what it holds live", func(t *testing.T) {
		b := held("100")
		b.live = true

		port, j := sized(t, b, theirs{left: "60"}, nil, tradeItem(domain.SideSell, "0.5", "20", "40", nil))
		require.Equal(t, trading.PortSkipped, port, "the paper shares are not the live books'")
		assert.Equal(t, "nothing held copying Alice to sell", j["skipped"].(map[string]any)["reason"])

		b.holdings[0].Mode = domain.ModeLive
		_, j = sized(t, b, theirs{left: "60"}, nil, tradeItem(domain.SideSell, "0.5", "20", "40", nil))
		assert.True(t, d("40").Equal(copied(t, j)["shares"].(decimal.Decimal)))
	})

	for name, tc := range map[string]struct {
		books books
		want  string
	}{
		"nothing held":          {books{}, "nothing held copying Alice to sell"},
		"under the least order": {held("10"), "selling 4 shares is under the market's least order of 5 shares"},
	} {
		t.Run(name, func(t *testing.T) {
			port, j := sized(t, tc.books, theirs{left: "60"}, nil, tradeItem(domain.SideSell, "0.5", "20", "40", nil))

			require.Equal(t, trading.PortSkipped, port)
			assert.Equal(t, tc.want, j["skipped"].(map[string]any)["reason"])
		})
	}
}

func TestCopySizingSkipsAndFails(t *testing.T) {
	t.Run("a copy under the market's least order is skipped (D98)", func(t *testing.T) {
		port, j := sized(t, books{}, theirs{}, map[string]any{"dollars": "2"}, tradeItem(domain.SideBuy, "0.5", "100", "200", nil))

		require.Equal(t, trading.PortSkipped, port)
		assert.Equal(t, "a copy of $2.00 is under the market's least order of 5 shares", j["skipped"].(map[string]any)["reason"])
		assert.Nil(t, j["copy"])
	})

	t.Run("a portfolio worth nothing has no share", func(t *testing.T) {
		port, _ := sized(t, books{}, theirs{value: "0"}, map[string]any{"sizing": "proportional"},
			tradeItem(domain.SideBuy, "0.5", "100", "200", nil))
		assert.Equal(t, trading.PortSkipped, port)
	})

	t.Run("the market from the token when the item does not say", func(t *testing.T) {
		item := tradeItem(domain.SideBuy, "0.5", "100", "200", nil)
		item.JSON["marketId"] = nil

		_, j := sized(t, books{}, theirs{}, nil, item)
		assert.Equal(t, "0xfound", copied(t, j)["marketId"])
	})

	t.Run("what it cannot look up fails the item", func(t *testing.T) {
		port, _ := sized(t, books{}, theirs{}, map[string]any{"sizing": "proportional"}, tradeItem(domain.SideBuy, "0.5", "100", "200", nil))
		assert.Equal(t, engine.ErrorPort, port)
	})

	t.Run("a share of the trader's buys, by their Take % (D141)", func(t *testing.T) {
		copiedBuys := 0

		for i := range 200 {
			item := tradeItem(domain.SideBuy, "0.5", "100", "200", map[string]any{"take": d("30")})
			item.JSON["txHash"] = fmt.Sprintf("0xtx%d", i)

			port, j := sized(t, books{}, theirs{}, nil, item)
			if port == engine.DefaultPort {
				copiedBuys++
				continue
			}

			require.Equal(t, trading.PortSkipped, port)
			assert.Equal(t, "not taken: Alice's Take % is 30", j["skipped"].(map[string]any)["reason"])
		}

		assert.InDelta(t, 60, copiedBuys, 25, "about 3 in 10")

		item := tradeItem(domain.SideSell, "0.5", "100", "200", map[string]any{"take": d("0")})
		item.JSON["txHash"] = "0xtx1"

		_, j := sized(t, books{}, theirs{}, nil, item)
		assert.NotContains(t, j["skipped"].(map[string]any)["reason"], "not taken", "sells are always copied")
	})

	t.Run("an item that is no trade fails", func(t *testing.T) {
		port, _ := sized(t, books{}, theirs{}, nil, engine.Item{JSON: map[string]any{"question": "Will it?"}})
		assert.Equal(t, engine.ErrorPort, port)
	})
}
