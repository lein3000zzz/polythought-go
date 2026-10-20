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
		trading.EXPECT().Summary(mock.Anything, byAlice, domain.WorkflowID("wf-1"), domain.TradingMode("")).Return(tradingsvc.Summary{
			Account: tradingsvc.AccountSummary{
				Mode: domain.ModePaper, Open: true, Cash: num("89.40016"), StartedWith: num("100"), Worth: num("99.20016"), Profit: num("-0.79984"),
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
			"account": {"mode": "paper", "open": true, "cash": "89.40016", "startedWith": "100", "worth": "99.20016",
				"profit": "-0.79984", "startedAt": "2026-10-17T12:00:00Z", "resetAt": "2026-10-17T12:00:00Z", "cashKnown": false,
				"held": "0"},
			"traders": [{"trader": "0xA1", "label": "Alice", "copies": 2, "volume": "14.8", "realized": "-0.39984",
				"unrealized": "-0.2", "wins": 1, "closed": 2}],
			"holdings": [{"mode": "paper", "trader": "0xA1", "traderLabel": "Alice", "tokenId": "t-yes", "marketId": "0xm",
				"shares": "10", "cost": "5.1", "realized": "-0.39984", "updatedAt": "2026-10-17T12:00:00Z",
				"question": "Will it?", "outcome": "Yes", "mark": "0.49", "value": "4.9", "unrealized": "-0.2"}]
		}`, w.Body.String())
	})

	t.Run("the live summary: the wallet and its cash", func(t *testing.T) {
		trading.EXPECT().Summary(mock.Anything, byAlice, domain.WorkflowID("wf-1"), domain.ModeLive).Return(tradingsvc.Summary{
			Account: tradingsvc.AccountSummary{
				Mode: domain.ModeLive, Open: true, Cash: num("250"), Worth: num("254.9"), Profit: num("2.4"), CashKnown: true,
				Wallet: &domain.Wallet{ID: "w-1", Address: "0x1111111111111111111111111111111111111111", Label: "Main", HasKey: true, CreatedAt: at},
			},
		}, nil).Once()

		w := get("/api/v1/workflows/wf-1/trading?mode=live").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{
			"account": {"mode": "live", "open": true, "cash": "250", "startedWith": "0", "worth": "254.9", "profit": "2.4",
				"cashKnown": true, "held": "0", "wallet": {"id": "w-1", "address": "0x1111111111111111111111111111111111111111", "label": "Main",
				"hasCredentials": false, "hasKey": true, "hasSessionKey": false, "createdAt": "2026-10-17T12:00:00Z"}},
			"traders": [], "holdings": []
		}`, w.Body.String())
	})

	t.Run("a live order: pending, its fills' trades", func(t *testing.T) {
		trading.EXPECT().Orders(mock.Anything, byAlice, domain.WorkflowID("wf-1"), ports.PageFilter{Limit: 1}).
			Return(ports.Page[domain.PlacedOrder]{Items: []domain.PlacedOrder{{
				OrderRequest: domain.OrderRequest{
					NodeID: "copy", Mode: domain.ModeLive, TokenID: "t-yes", Side: domain.SideBuy, Type: domain.OrderFAK,
					Price: num("0.5"), Shares: num("10"),
				},
				ID: "o-3", Status: domain.OrderPartial, CreatedAt: at,
				Fills: []domain.OrderFill{{Price: num("0.5"), Shares: num("4"), Dollars: num("2"), Fee: num("0.04"), TradeID: "trade-1", Status: domain.FillMined}},
			}}}, nil).Once()

		w := get("/api/v1/workflows/wf-1/orders?limit=1").do(t, srv)
		assert.JSONEq(t, `{"orders":[{"id":"o-3","nodeId":"copy","mode":"live","tokenId":"t-yes","side":"buy","type":"FAK",
			"postOnly":false,"price":"0.5","shares":"10","status":"partial",
			"fills":[{"price":"0.5","shares":"4","dollars":"2","fee":"0.04","status":"mined"}],"at":"2026-10-17T12:00:00Z"}]}`, w.Body.String())
	})

	t.Run("how it trades, and switching to live, confirmed", func(t *testing.T) {
		wallet := &domain.Wallet{ID: "w-1", Address: "0x1111111111111111111111111111111111111111", Label: "Main", HasKey: true, CreatedAt: at}

		trading.EXPECT().Setup(mock.Anything, byAlice, domain.WorkflowID("wf-1")).
			Return(tradingsvc.Setup{Mode: domain.ModePaper, LiveAvailable: false, Why: "live trading is off on this server"}, nil).Once()

		w := get("/api/v1/workflows/wf-1/trading/setup").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"mode":"paper","holdsLive":false,"liveAvailable":false,"why":"live trading is off on this server"}`, w.Body.String())

		trading.EXPECT().SetSetup(mock.Anything, byAlice, domain.WorkflowID("wf-1"),
			tradingsvc.SetupChange{Mode: domain.ModeLive, WalletID: "w-1", Confirmed: true}).
			Return(tradingsvc.Setup{Mode: domain.ModeLive, Wallet: wallet, LiveAvailable: true}, nil).Once()

		w = request{method: "PUT", path: "/api/v1/workflows/wf-1/trading/setup", headers: cookie,
			body: `{"mode":"live","walletId":"w-1","confirm":true}`}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"mode":"live","holdsLive":false,"liveAvailable":true,"wallet":{"id":"w-1",
			"address":"0x1111111111111111111111111111111111111111","label":"Main","hasCredentials":false,"hasKey":true,
			"hasSessionKey":false,"createdAt":"2026-10-17T12:00:00Z"}}`, w.Body.String())
	})

	t.Run("switching refused, each way it can be", func(t *testing.T) {
		for err, want := range map[error]struct {
			status int
			code   string
		}{
			domain.ErrNotConfirmed:      {http.StatusBadRequest, "not_confirmed"},
			domain.ErrWalletCannotTrade: {http.StatusBadRequest, "wallet_cannot_trade"},
			domain.ErrLiveTradingOff:    {http.StatusForbidden, "live_trading_off"},
			domain.ErrHoldsLive:         {http.StatusConflict, "holds_live"},
		} {
			trading.EXPECT().SetSetup(mock.Anything, byAlice, domain.WorkflowID("wf-1"), mock.Anything).Return(tradingsvc.Setup{}, err).Once()

			w := request{method: "PUT", path: "/api/v1/workflows/wf-1/trading/setup", headers: cookie,
				body: `{"mode":"live","walletId":"w-1"}`}.do(t, srv)
			assert.Equal(t, want.status, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"code":"`+want.code+`"`)
		}
	})

	t.Run("cancelling a resting order", func(t *testing.T) {
		expires := at.Add(time.Hour)
		trading.EXPECT().Cancel(mock.Anything, byAlice, domain.WorkflowID("wf-1"), domain.OrderID("o-7")).Return(domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{
				NodeID: "buy", Mode: domain.ModePaper, TokenID: "t-yes", Side: domain.SideBuy, Type: domain.OrderGTD,
				Price: num("0.4"), Shares: num("10"), Expires: expires,
			},
			ID: "o-7", Status: domain.OrderCanceled, Reason: "cancelled by its owner, 0 of 10 shares filled", CreatedAt: at,
		}, nil).Once()

		w := request{method: "POST", path: "/api/v1/workflows/wf-1/orders/o-7/cancel", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"id":"o-7","nodeId":"buy","mode":"paper","tokenId":"t-yes","side":"buy","type":"GTD","postOnly":false,
			"price":"0.4","shares":"10","status":"canceled","reason":"cancelled by its owner, 0 of 10 shares filled","fills":[],
			"at":"2026-10-17T12:00:00Z","expiresAt":"2026-10-17T13:00:00Z"}`, w.Body.String())

		trading.EXPECT().Cancel(mock.Anything, byAlice, domain.WorkflowID("wf-1"), domain.OrderID("o-7")).
			Return(domain.PlacedOrder{}, domain.ErrNotOpen).Once()

		w = request{method: "POST", path: "/api/v1/workflows/wf-1/orders/o-7/cancel", headers: cookie}.do(t, srv)
		assert.Equal(t, http.StatusConflict, w.Code)
		assert.Contains(t, w.Body.String(), `"code":"not_open"`)
	})

	t.Run("a wallet's cash", func(t *testing.T) {
		trading.EXPECT().WalletCash(mock.Anything, byAlice, domain.WalletID("w-1")).Return(num("152.25"), nil).Once()

		w := get("/api/v1/wallets/w-1/cash").do(t, srv)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.JSONEq(t, `{"cash":"152.25"}`, w.Body.String())
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
