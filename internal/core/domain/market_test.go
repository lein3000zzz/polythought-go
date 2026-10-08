package domain_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func TestOrderBookPrices(t *testing.T) {
	level := func(p string) domain.Level { return domain.Level{Price: dec(p), Size: dec("1")} }

	tests := []struct {
		name       string
		book       domain.OrderBook
		wantBid    string // empty means no bid
		wantAsk    string
		wantMid    string // empty means undefined
		wantSpread string
	}{
		{
			name:       "both sides",
			book:       domain.OrderBook{Bids: []domain.Level{level("0.26"), level("0.25")}, Asks: []domain.Level{level("0.27"), level("0.28")}},
			wantBid:    "0.26",
			wantAsk:    "0.27",
			wantMid:    "0.265",
			wantSpread: "0.01",
		},
		{
			name:    "bids only",
			book:    domain.OrderBook{Bids: []domain.Level{level("0.5")}},
			wantBid: "0.5",
		},
		{
			name:    "asks only",
			book:    domain.OrderBook{Asks: []domain.Level{level("0.5")}},
			wantAsk: "0.5",
		},
		{
			name: "empty",
		},
	}

	str := func(d decimal.Decimal, ok bool) string {
		if !ok {
			return ""
		}

		return d.String()
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bid, okBid := tt.book.BestBid()
			ask, okAsk := tt.book.BestAsk()

			assert.Equal(t, tt.wantBid, str(bid.Price, okBid))
			assert.Equal(t, tt.wantAsk, str(ask.Price, okAsk))
			assert.Equal(t, tt.wantMid, str(tt.book.Mid()))
			assert.Equal(t, tt.wantSpread, str(tt.book.Spread()))
		})
	}
}

func TestMarketOutcomes(t *testing.T) {
	market := domain.Market{Outcomes: []domain.Outcome{
		{TokenID: "t1", Label: "Yes", Price: dec("0.3")},
		{TokenID: "t2", Label: "No", Price: dec("0.7")},
	}}

	tests := []struct {
		name   string
		id     domain.TokenID
		want   string
		wantOK bool
	}{
		{name: "first outcome", id: "t1", want: "Yes", wantOK: true},
		{name: "second outcome", id: "t2", want: "No", wantOK: true},
		{name: "unknown token", id: "t3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := market.Outcome(tt.id)

			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got.Label)
		})
	}

	assert.Equal(t, []domain.TokenID{"t1", "t2"}, market.TokenIDs())
}
