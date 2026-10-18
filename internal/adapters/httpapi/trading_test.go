package httpapi_test

import (
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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	tradingsvc "github.com/lein3000zzz/polythought-go/internal/core/services/trading"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestTrading(t *testing.T) {
	authSvc := httpapimocks.NewMockAuthService(t)
	withSession(authSvc, alice)

	trading := httpapimocks.NewMockTradingService(t)
	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: authSvc, Trading: trading})
	require.NoError(t, err)

	byAlice := mock.MatchedBy(func(p auth.Principal) bool { return p.User.ID == alice.ID })
	num := decimal.RequireFromString
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	get := func(path string) request { return request{method: "GET", path: path, headers: cookie} }

	t.Run("a paper account not open yet says what it would start with", func(t *testing.T) {
		trading.EXPECT().PaperAccount(mock.Anything, byAlice, domain.WorkflowID("wf-1")).
			Return(domain.PaperAccount{}, false, num("1000"), nil).Once()

		w := get("/api/v1/workflows/wf-1/paper-account").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"open":false,"cash":"1000","startedWith":"1000"}`, w.Body.String())
	})

	t.Run("an open one, exactly", func(t *testing.T) {
		trading.EXPECT().PaperAccount(mock.Anything, byAlice, domain.WorkflowID("wf-1")).
			Return(domain.PaperAccount{Cash: num("989.65661"), StartedWith: num("1000"), StartedAt: at}, true, num("500"), nil).Once()

		w := get("/api/v1/workflows/wf-1/paper-account").do(t, srv)
		assert.JSONEq(t, `{"open":true,"cash":"989.65661","startedWith":"1000","startedAt":"2026-10-17T12:00:00Z"}`, w.Body.String())
	})

	t.Run("orders with their fills, refused ones with why", func(t *testing.T) {
		trading.EXPECT().Orders(mock.Anything, byAlice, domain.WorkflowID("wf-1"), ports.PageFilter{Limit: 2}).
			Return(ports.Page[domain.PlacedOrder]{Items: []domain.PlacedOrder{
				{
					OrderRequest: domain.OrderRequest{
						NodeID: "copy", Mode: domain.ModePaper, Trader: "0xA1", TraderLabel: "Alice", TokenID: "t-yes", MarketID: "0xm",
						Side: domain.SideBuy, Type: domain.OrderFAK, Price: num("0.52"), Dollars: num("10"),
					},
					ID: "o-2", Status: domain.OrderFilled, CreatedAt: at,
					Fills: []domain.OrderFill{{Price: num("0.5"), Shares: num("20"), Dollars: num("10"), Fee: num("0.2")}},
				},
				{
					OrderRequest: domain.OrderRequest{
						NodeID: "copy", Mode: domain.ModePaper, TokenID: "t-yes", Side: domain.SideSell, Type: domain.OrderFOK,
						Price: num("0.4"), Shares: num("5"),
					},
					ID: "o-1", Status: domain.OrderRefused, Reason: "not enough shares: 5 to sell, 0 held", CreatedAt: at,
				},
			}, NextCursor: "o-1"}, nil).Once()

		w := get("/api/v1/workflows/wf-1/orders?limit=2").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"orders":[
			{"id":"o-2","nodeId":"copy","mode":"paper","trader":"0xA1","traderLabel":"Alice","tokenId":"t-yes","marketId":"0xm",
			 "side":"buy","type":"FAK","postOnly":false,"price":"0.52","dollars":"10","status":"filled",
			 "fills":[{"price":"0.5","shares":"20","dollars":"10","fee":"0.2"}],"at":"2026-10-17T12:00:00Z"},
			{"id":"o-1","nodeId":"copy","mode":"paper","tokenId":"t-yes","side":"sell","type":"FOK","postOnly":false,
			 "price":"0.4","shares":"5","status":"refused","reason":"not enough shares: 5 to sell, 0 held","fills":[],
			 "at":"2026-10-17T12:00:00Z"}
		],"nextCursor":"o-1"}`, w.Body.String())
	})

	t.Run("holdings, per trader", func(t *testing.T) {
		trading.EXPECT().Holdings(mock.Anything, byAlice, domain.WorkflowID("wf-1")).Return([]domain.Holding{{
			Mode: domain.ModePaper, Trader: "0xA1", TraderLabel: "Alice", TokenID: "t-yes", MarketID: "0xm",
			Shares: num("15"), Cost: num("7.65"), Realized: num("1.35"), UpdatedAt: at,
		}}, nil).Once()

		w := get("/api/v1/workflows/wf-1/holdings").do(t, srv)
		assert.JSONEq(t, `{"holdings":[{"mode":"paper","trader":"0xA1","traderLabel":"Alice","tokenId":"t-yes","marketId":"0xm",
			"shares":"15","cost":"7.65","realized":"1.35","updatedAt":"2026-10-17T12:00:00Z"}]}`, w.Body.String())
	})

	t.Run("the summary: the account, traders and holdings marked to market", func(t *testing.T) {
		mark := num("0.49")
		trading.EXPECT().Summary(mock.Anything, byAlice, domain.WorkflowID("wf-1")).Return(tradingsvc.Summary{
			Account: tradingsvc.AccountSummary{
				Open: true, Cash: num("89.40016"), StartedWith: num("100"), Worth: num("99.20016"), Profit: num("-0.79984"),
				StartedAt: at, ResetAt: at,
			},
			Traders: []tradingsvc.TraderSummary{{
				Trader: "0xA1", Label: "Alice", Copies: 2, Volume: num("14.8"), Realized: num("-0.39984"),
				Unrealized: num("-0.2"), Wins: 1, Closed: 2,
			}},
			Holdings: []tradingsvc.HoldingSummary{{
				Holding: domain.Holding{
					Mode: domain.ModePaper, Trader: "0xA1", TraderLabel: "Alice", TokenID: "t-yes", MarketID: "0xm",
					Shares: num("10"), Cost: num("5.1"), Realized: num("-0.39984"), UpdatedAt: at,
				},
				Question: "Will it?", Outcome: "Yes", Mark: &mark, Value: num("4.9"),
			}},
		}, nil).Once()

		w := get("/api/v1/workflows/wf-1/trading").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{
			"account": {"open": true, "cash": "89.40016", "startedWith": "100", "worth": "99.20016", "profit": "-0.79984",
				"startedAt": "2026-10-17T12:00:00Z", "resetAt": "2026-10-17T12:00:00Z"},
			"traders": [{"trader": "0xA1", "label": "Alice", "copies": 2, "volume": "14.8", "realized": "-0.39984",
				"unrealized": "-0.2", "wins": 1, "closed": 2}],
			"holdings": [{"mode": "paper", "trader": "0xA1", "traderLabel": "Alice", "tokenId": "t-yes", "marketId": "0xm",
				"shares": "10", "cost": "5.1", "realized": "-0.39984", "updatedAt": "2026-10-17T12:00:00Z",
				"question": "Will it?", "outcome": "Yes", "mark": "0.49", "value": "4.9", "unrealized": "-0.2"}]
		}`, w.Body.String())
	})

	t.Run("reset", func(t *testing.T) {
		trading.EXPECT().Reset(mock.Anything, byAlice, domain.WorkflowID("wf-1")).
			Return(domain.PaperAccount{Cash: num("1000"), StartedWith: num("1000"), StartedAt: at, ResetAt: at}, nil).Once()

		w := request{method: "POST", path: "/api/v1/workflows/wf-1/paper-account/reset", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"open":true,"cash":"1000","startedWith":"1000","startedAt":"2026-10-17T12:00:00Z","resetAt":"2026-10-17T12:00:00Z"}`, w.Body.String())
	})

	t.Run("someone else's workflow is not there", func(t *testing.T) {
		trading.EXPECT().Holdings(mock.Anything, byAlice, domain.WorkflowID("wf-bob")).Return(nil, domain.ErrNotFound).Once()

		assert.Equal(t, http.StatusNotFound, get("/api/v1/workflows/wf-bob/holdings").do(t, srv).Code)
	})

	t.Run("a bad page size", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, get("/api/v1/workflows/wf-1/orders?limit=0").do(t, srv).Code)
	})

	t.Run("signed in only", func(t *testing.T) {
		w := request{method: "GET", path: "/api/v1/workflows/wf-1/orders"}.do(t, srv)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}
