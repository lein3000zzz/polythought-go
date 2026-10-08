package polymarket

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func prices(ps ...string) pm.Stringified[decimal.Decimal] {
	out := make(pm.Stringified[decimal.Decimal], len(ps))
	for i, p := range ps {
		out[i] = dec(p)
	}

	return out
}

func TestToMarketStatus(t *testing.T) {
	tests := []struct {
		name   string
		market pm.Market
		want   domain.MarketStatus
	}{
		{name: "open", market: pm.Market{Active: true}, want: domain.MarketOpen},
		{name: "closed awaiting resolution", market: pm.Market{Closed: true}, want: domain.MarketClosed},
		{name: "closed while UMA proposal pending", market: pm.Market{Closed: true, UMAResolutionStatus: "proposed"}, want: domain.MarketClosed},
		{name: "v1 resolved", market: pm.Market{Closed: true, UMAResolutionStatus: "resolved"}, want: domain.MarketResolved},
		{name: "v2 resolved", market: pm.Market{Version: "v2", Closed: true, ResolutionStatus: "resolved"}, want: domain.MarketResolved},
		{name: "resolution flag without close stays open", market: pm.Market{UMAResolutionStatus: "resolved"}, want: domain.MarketOpen},
		{name: "archived wins", market: pm.Market{Closed: true, Archived: true, UMAResolutionStatus: "resolved"}, want: domain.MarketArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, toMarketStatus(tt.market))
		})
	}
}

func TestToOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		market pm.Market
		want   []domain.Outcome
	}{
		{
			name: "v1 zips labels, tokens and prices",
			market: pm.Market{
				Version: "v1", Outcomes: []string{"Yes", "No"},
				ClobTokenIDs: []string{"t1", "t2"}, OutcomePrices: prices("0.3", "0.7"),
			},
			want: []domain.Outcome{
				{TokenID: "t1", Label: "Yes", Price: dec("0.3")},
				{TokenID: "t2", Label: "No", Price: dec("0.7")},
			},
		},
		{
			name: "v2 takes position IDs",
			market: pm.Market{
				Version: "v2", Outcomes: []string{"Yes", "No"},
				ClobTokenIDs: []string{"t1", "t2"}, PositionIDs: []string{"p1", "p2"}, OutcomePrices: prices("0.3", "0.7"),
			},
			want: []domain.Outcome{
				{TokenID: "p1", Label: "Yes", Price: dec("0.3")},
				{TokenID: "p2", Label: "No", Price: dec("0.7")},
			},
		},
		{
			name:   "market without an order book keeps labels",
			market: pm.Market{Outcomes: []string{"Yes", "No"}},
			want:   []domain.Outcome{{Label: "Yes"}, {Label: "No"}},
		},
		{
			name:   "no outcomes",
			market: pm.Market{},
			want:   []domain.Outcome{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, toOutcomes(tt.market))
		})
	}
}

func TestToLevels(t *testing.T) {
	levels := func(ps ...string) []pm.PriceLevel {
		out := make([]pm.PriceLevel, len(ps))
		for i, p := range ps {
			out[i] = pm.PriceLevel{Price: dec(p), Size: dec("1")}
		}

		return out
	}

	tests := []struct {
		name  string
		in    []pm.PriceLevel
		bids  bool
		wantP []string
	}{
		{name: "bids as sent (worst first) become best first", in: levels("0.1", "0.2", "0.3"), bids: true, wantP: []string{"0.3", "0.2", "0.1"}},
		{name: "asks as sent (worst first) become best first", in: levels("0.9", "0.8", "0.7"), wantP: []string{"0.7", "0.8", "0.9"}},
		{name: "already sorted bids are kept", in: levels("0.3", "0.1"), bids: true, wantP: []string{"0.3", "0.1"}},
		{name: "unordered asks", in: levels("0.8", "0.7", "0.9"), wantP: []string{"0.7", "0.8", "0.9"}},
		{name: "empty side", in: nil, bids: true, wantP: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toLevels(tt.in, tt.bids)

			gotP := make([]string, len(got))
			for i, l := range got {
				gotP[i] = l.Price.String()
			}

			assert.Equal(t, tt.wantP, gotP)
		})
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{name: "gamma iso", in: "2027-01-01T04:59:00Z", want: time.Date(2027, 1, 1, 4, 59, 0, 0, time.UTC)},
		{name: "gamma iso with fraction", in: "2025-07-03T20:37:00.228Z", want: time.Date(2025, 7, 3, 20, 37, 0, 228e6, time.UTC)},
		{name: "gamma closed time", in: "2026-10-08 14:59:26+00", want: time.Date(2026, 10, 8, 14, 59, 26, 0, time.UTC)},
		{name: "data end date", in: "2027-01-01", want: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{name: "clob millis", in: "1791450379907", want: time.UnixMilli(1791450379907).UTC()},
		{name: "unix seconds", in: "1791450379", want: time.Unix(1791450379, 0).UTC()},
		{name: "offset is normalised to utc", in: "2027-01-01T03:00:00+03:00", want: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{name: "empty", in: "", want: time.Time{}},
		{name: "garbage", in: "soon", want: time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseTime(tt.in))
		})
	}
}

func TestSides(t *testing.T) {
	tests := []struct {
		api    pm.Side
		domain domain.Side
	}{
		{api: pm.SideBuy, domain: domain.SideBuy},
		{api: pm.SideSell, domain: domain.SideSell},
		{api: "", domain: ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.api)+"/"+string(tt.domain), func(t *testing.T) {
			assert.Equal(t, tt.domain, toSide(tt.api))
			assert.Equal(t, tt.api, fromSide(tt.domain))
		})
	}
}

func TestToMarketAndEvent(t *testing.T) {
	market := pm.Market{
		ConditionID: "0xc", Slug: "s", Question: "Q?", Description: "d", Version: "v1",
		Outcomes: []string{"Yes", "No"}, ClobTokenIDs: []string{"t1", "t2"}, OutcomePrices: prices("0.4", "0.6"),
		Active: true, AcceptingOrders: true, NegRisk: true,
		OrderPriceMinTickSize: dec("0.001"), OrderMinSize: dec("5"),
		Volume24hr: dec("100.5"), LiquidityNum: dec("2000"), EndDate: "2027-01-01T00:00:00Z",
	}

	wantMarket := domain.Market{
		ID: "0xc", Slug: "s", Question: "Q?", Description: "d",
		Outcomes: []domain.Outcome{
			{TokenID: "t1", Label: "Yes", Price: dec("0.4")},
			{TokenID: "t2", Label: "No", Price: dec("0.6")},
		},
		Status: domain.MarketOpen, AcceptingOrders: true, NegRisk: true,
		TickSize: dec("0.001"), MinOrderSize: dec("5"), Volume24h: dec("100.5"), Liquidity: dec("2000"),
		EndsAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "market", got: toMarket(market), want: wantMarket},
		{
			name: "event",
			got: toEvent(pm.Event{
				ID: "7", Slug: "e", Title: "T", Description: "d", EnableNegRisk: true,
				StartDate: "2026-01-01T00:00:00Z", EndDate: "2027-01-01T00:00:00Z",
				Volume24hr: dec("10"), Liquidity: dec("20"),
				Markets: []pm.Market{market}, Tags: []pm.Tag{{ID: "1", Slug: "politics"}},
			}),
			want: domain.Event{
				ID: "7", Slug: "e", Title: "T", Description: "d", NegRisk: true,
				StartsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), EndsAt: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
				Volume24h: dec("10"), Liquidity: dec("20"),
				Markets: []domain.Market{wantMarket}, Tags: []string{"politics"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}
