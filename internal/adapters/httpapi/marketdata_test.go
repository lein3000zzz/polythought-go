package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestMarketData(t *testing.T) {
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	d := decimal.RequireFromString

	server := func(t *testing.T, md *httpapimocks.MockMarketDataService) *httpapi.Server {
		t.Helper()

		authSvc := httpapimocks.NewMockAuthService(t)
		scoped(authSvc, alice, domain.ScopeRead) // read is enough

		srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, MarketData: md})
		require.NoError(t, err)

		return srv
	}

	t.Run("search, with each market's event", func(t *testing.T) {
		md := httpapimocks.NewMockMarketDataService(t)
		md.EXPECT().Search(mock.Anything, alice.ID, "rain", true, 200).Return([]markets.Found{{
			Event: domain.Event{Title: "Rain in NYC", Slug: "rain-in-nyc"},
			Market: domain.Market{
				ID: "0xabc", Slug: "rain", Question: "Rain?", Status: domain.MarketOpen, AcceptingOrders: true,
				TickSize: d("0.01"), MinOrderSize: d("5"), Volume24h: d("100"), Liquidity: d("50"), EndsAt: at,
				Outcomes: []domain.Outcome{{TokenID: "t1", Label: "Yes", Price: d("0.53")}},
			},
		}}, nil).Once()

		w := request{method: "GET", path: "/api/v1/market-data/search?query=rain&closed=true&limit=200", headers: bearer}.do(t, server(t, md))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"markets": [{
			"id": "0xabc", "slug": "rain", "question": "Rain?", "outcomes": [{"tokenId": "t1", "label": "Yes", "price": "0.53"}],
			"status": "open", "acceptingOrders": true, "negRisk": false, "tickSize": "0.01", "minOrderSize": "5",
			"volume24h": "100", "liquidity": "50", "endsAt": "2026-11-24T12:00:00Z", "event": "Rain in NYC", "eventSlug": "rain-in-nyc"
		}]}`, w.Body.String())
	})

	t.Run("books in the order asked, null for none, to the depth asked", func(t *testing.T) {
		md := httpapimocks.NewMockMarketDataService(t)
		md.EXPECT().Books(mock.Anything, alice.ID, []domain.TokenID{"t1", "t2"}).Return([]domain.OrderBook{
			{TokenID: "t1", Bids: []domain.Level{{Price: d("0.52"), Size: d("10")}, {Price: d("0.51"), Size: d("5")}}, Asks: []domain.Level{{Price: d("0.54"), Size: d("7")}}, At: at},
			{},
		}, nil).Once()

		w := request{method: "GET", path: "/api/v1/market-data/books?tokens=t1,%20t2&depth=1", headers: bearer}.do(t, server(t, md))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"books": [{
			"tokenId": "t1", "bestBid": "0.52", "bestAsk": "0.54", "mid": "0.53", "spread": "0.02",
			"bids": [{"price": "0.52", "size": "10"}], "asks": [{"price": "0.54", "size": "7"}], "at": "2026-11-24T12:00:00Z"
		}, null]}`, w.Body.String())
	})

	t.Run("history, with how much it moved", func(t *testing.T) {
		md := httpapimocks.NewMockMarketDataService(t)
		md.EXPECT().History(mock.Anything, alice.ID, domain.TokenID("t1"), "1d", 0).Return([]domain.PricePoint{
			{At: at.Add(-time.Hour), Price: d("0.50")}, {At: at, Price: d("0.55")},
		}, nil).Once()

		w := request{method: "GET", path: "/api/v1/market-data/history/t1", headers: bearer}.do(t, server(t, md))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"tokenId": "t1", "window": "1d", "points": [
			{"at": "2026-11-24T11:00:00Z", "price": "0.5"}, {"at": "2026-11-24T12:00:00Z", "price": "0.55"}
		], "change": "0.05", "changePct": "10"}`, w.Body.String())
	})

	t.Run("a market, its trades, an address's positions", func(t *testing.T) {
		md := httpapimocks.NewMockMarketDataService(t)
		md.EXPECT().Market(mock.Anything, alice.ID, "rain-in-nyc").Return(domain.Market{ID: "0xabc", Slug: "rain-in-nyc"}, nil).Once()
		md.EXPECT().Trades(mock.Anything, alice.ID, domain.MarketID("0xabc"), 5).Return([]domain.Trade{{
			MarketID: "0xabc", TokenID: "t1", Outcome: "Yes", Side: domain.SideBuy, Price: d("0.53"), Size: d("10"), Wallet: "0xw", At: at,
		}}, nil).Once()
		md.EXPECT().Positions(mock.Anything, alice.ID, "0xw", 0).Return([]domain.Position{{
			Wallet: "0xw", MarketID: "0xabc", TokenID: "t1", Title: "Rain?", Outcome: "Yes", Size: d("10"), AvgPrice: d("0.5"),
			CurrentPrice: d("0.53"), Value: d("5.3"), RealizedPnL: d("0"), UnrealizedPnL: d("0.3"),
		}}, nil).Once()

		srv := server(t, md)

		w := request{method: "GET", path: "/api/v1/market-data/markets/rain-in-nyc", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), `"id":"0xabc"`)

		w = request{method: "GET", path: "/api/v1/market-data/markets/0xabc/trades?limit=5", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"trades": [{"marketId": "0xabc", "tokenId": "t1", "outcome": "Yes", "side": "buy", "price": "0.53",
			"size": "10", "wallet": "0xw", "at": "2026-11-24T12:00:00Z"}]}`, w.Body.String())

		w = request{method: "GET", path: "/api/v1/market-data/traders/0xw/positions", headers: bearer}.do(t, srv)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"positions": [{"wallet": "0xw", "marketId": "0xabc", "tokenId": "t1", "title": "Rain?", "outcome": "Yes",
			"size": "10", "avgPrice": "0.5", "currentPrice": "0.53", "value": "5.3", "realizedPnl": "0", "unrealizedPnl": "0.3",
			"redeemable": false}]}`, w.Body.String())
	})

	t.Run("what it refuses", func(t *testing.T) {
		md := httpapimocks.NewMockMarketDataService(t)
		md.EXPECT().Market(mock.Anything, alice.ID, "nope").Return(domain.Market{}, domain.ErrNotFound).Once()
		md.EXPECT().Search(mock.Anything, alice.ID, "", false, 0).Return(nil, fmt.Errorf("%w: query: say what to look for", domain.ErrInvalidInput)).Once()

		srv := server(t, md)

		for _, tc := range []struct {
			path string
			want int
		}{
			{path: "/api/v1/market-data/markets/nope", want: http.StatusNotFound},
			{path: "/api/v1/market-data/search", want: http.StatusBadRequest},
			{path: "/api/v1/market-data/search?query=x&limit=lots", want: http.StatusBadRequest},
			{path: "/api/v1/market-data/books?tokens=t1&depth=101", want: http.StatusBadRequest},
		} {
			w := request{method: "GET", path: tc.path, headers: bearer}.do(t, srv)
			assert.Equal(t, tc.want, w.Code, tc.path+": "+w.Body.String())
		}
	})
}
