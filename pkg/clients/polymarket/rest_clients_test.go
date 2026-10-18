package polymarket

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

type restClients struct {
	gamma *DefaultGammaClient
	clob  *DefaultClobClient
	data  *DefaultDataClient
}

// captured is the last request a canned server received.
type captured struct {
	hits   atomic.Int32
	method string
	uri    string
	body   string
}

// newRESTClients points all three clients at one server that answers with status and body.
func newRESTClients(t *testing.T, util *observability.Utility, status int, body []byte) (restClients, *captured) {
	t.Helper()

	got := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.hits.Add(1)
		raw, _ := io.ReadAll(r.Body)
		got.method, got.uri, got.body = r.Method, r.RequestURI, string(raw)

		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	cfg := HTTPConfig{BaseURL: srv.URL, MaxRetries: -1}

	gamma, err := NewGammaClient(util, cfg)
	require.NoError(t, err)

	clob, err := NewClobClient(util, cfg)
	require.NoError(t, err)

	data, err := NewDataClient(util, cfg)
	require.NoError(t, err)

	return restClients{gamma: gamma, clob: clob, data: data}, got
}

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func TestRESTRequests(t *testing.T) {
	closed, takerOnly := false, false

	tests := []struct {
		name       string
		response   []byte
		call       func(ctx context.Context, c restClients) (any, error)
		wantMethod string
		wantURI    string
		wantBody   string
		check      func(t *testing.T, result any)
	}{
		// Gamma: list filters repeat the key.
		{
			name:     "gamma list markets",
			response: fixture(t, "gamma_markets_keyset.json"),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.ListMarkets(ctx, MarketsQuery{
					Limit: 5, AfterCursor: "abc", Closed: &closed,
					TagIDs: []string{"1", "2"}, ConditionIDs: []string{"0xc"}, Slugs: []string{"a", "b"}, TokenIDs: []string{"t1"},
				})
			},
			wantURI: "/markets/keyset?after_cursor=abc&clob_token_ids=t1&closed=false&condition_ids=0xc&limit=5&slug=a&slug=b&tag_id=1&tag_id=2",
			check: func(t *testing.T, result any) {
				page := result.(Page[Market])
				assert.NotEmpty(t, page.Items)
				assert.NotEmpty(t, page.NextCursor)
			},
		},
		{
			name:     "gamma zero query sends no params",
			response: []byte(`{"markets":[]}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.ListMarkets(ctx, MarketsQuery{})
			},
			wantURI: "/markets/keyset",
			check: func(t *testing.T, result any) {
				assert.Empty(t, result.(Page[Market]).NextCursor)
			},
		},
		{
			name:     "gamma list events",
			response: fixture(t, "gamma_events_keyset.json"),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.ListEvents(ctx, EventsQuery{
					Limit: 2, Closed: &closed, Live: &closed, Featured: &closed, TitleSearch: "btc",
					SeriesIDs: []string{"s1"}, ExcludeTagIDs: []string{"t1", "t2"},
				})
			},
			wantURI: "/events/keyset?closed=false&exclude_tag_id=t1&exclude_tag_id=t2&featured=false&limit=2&live=false&series_id=s1&title_search=btc",
			check: func(t *testing.T, result any) {
				assert.Len(t, result.(Page[Event]).Items, 1)
			},
		},
		{
			name:     "gamma get market",
			response: []byte(`{"id":"123","conditionId":"0xc"}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.GetMarket(ctx, "123")
			},
			wantURI: "/markets/123",
			check: func(t *testing.T, result any) {
				assert.Equal(t, "0xc", result.(Market).ConditionID)
			},
		},
		{
			name:     "gamma get market by slug escapes the path",
			response: []byte(`{"id":"1"}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.GetMarketBySlug(ctx, "a/b c")
			},
			wantURI: "/markets/slug/a%2Fb%20c",
		},
		{
			name:     "gamma get event",
			response: []byte(`{"id":"7","markets":[{"id":"1"},{"id":"2"}]}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.GetEvent(ctx, "7")
			},
			wantURI: "/events/7",
			check: func(t *testing.T, result any) {
				assert.Len(t, result.(Event).Markets, 2)
			},
		},
		{
			name:     "gamma get event by slug",
			response: []byte(`{"id":"7"}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.GetEventBySlug(ctx, "election")
			},
			wantURI: "/events/slug/election",
		},
		{
			name:     "gamma list tags",
			response: []byte(`[{"id":"1","label":"Politics","slug":"politics"}]`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.ListTags(ctx, TagsQuery{Limit: 2, Offset: 4})
			},
			wantURI: "/tags?limit=2&offset=4",
			check: func(t *testing.T, result any) {
				assert.Equal(t, []Tag{{ID: "1", Label: "Politics", Slug: "politics"}}, result)
			},
		},
		{
			name:     "gamma search",
			response: []byte(`{"events":[{"id":"1"}],"pagination":{"hasMore":true,"totalResults":9}}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.gamma.Search(ctx, SearchQuery{Query: "btc", LimitPerType: 3, Page: 2, EventsStatus: "active"})
			},
			wantURI: "/public-search?events_status=active&limit_per_type=3&page=2&q=btc",
			check: func(t *testing.T, result any) {
				assert.Equal(t, SearchPagination{HasMore: true, TotalResults: 9}, result.(SearchResult).Pagination)
			},
		},

		// CLOB: batches are POST bodies.
		{
			name:     "clob order books",
			response: fixture(t, "clob_books.json"),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.GetOrderBooks(ctx, "1", "2")
			},
			wantMethod: http.MethodPost,
			wantURI:    "/books",
			wantBody:   `[{"token_id":"1"},{"token_id":"2"}]`,
			check: func(t *testing.T, result any) {
				assert.Len(t, result, 2)
			},
		},
		{
			name:     "clob prices",
			response: []byte(`{"1":{"BUY":"0.026","SELL":"0.027"}}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.GetPrices(ctx, PriceRequest{TokenID: "1", Side: SideBuy}, PriceRequest{TokenID: "1", Side: SideSell})
			},
			wantMethod: http.MethodPost,
			wantURI:    "/prices",
			wantBody:   `[{"token_id":"1","side":"BUY"},{"token_id":"1","side":"SELL"}]`,
			check: func(t *testing.T, result any) {
				prices := result.(map[string]SidePrices)["1"]
				assert.True(t, prices[SideBuy].Equal(dec("0.026")))
				assert.True(t, prices[SideSell].Equal(dec("0.027")))
			},
		},
		{
			name:     "clob midpoints",
			response: []byte(`{"1":"0.5","2":"0.25"}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.GetMidpoints(ctx, "1", "2")
			},
			wantMethod: http.MethodPost,
			wantURI:    "/midpoints",
			wantBody:   `[{"token_id":"1"},{"token_id":"2"}]`,
			check: func(t *testing.T, result any) {
				assert.True(t, result.(map[string]decimal.Decimal)["2"].Equal(dec("0.25")))
			},
		},
		{
			name:     "clob spreads",
			response: []byte(`{"1":"0.001"}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.GetSpreads(ctx, "1")
			},
			wantMethod: http.MethodPost,
			wantURI:    "/spreads",
			wantBody:   `[{"token_id":"1"}]`,
			check: func(t *testing.T, result any) {
				assert.True(t, result.(map[string]decimal.Decimal)["1"].Equal(dec("0.001")))
			},
		},
		{
			name:     "clob last trade prices",
			response: []byte(`[{"token_id":"1","price":"0.5","side":"SELL"}]`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.GetLastTradePrices(ctx, "1")
			},
			wantMethod: http.MethodPost,
			wantURI:    "/last-trades-prices",
			wantBody:   `[{"token_id":"1"}]`,
			check: func(t *testing.T, result any) {
				trades := result.([]LastTrade)
				require.Len(t, trades, 1)
				assert.Equal(t, SideSell, trades[0].Side)
			},
		},
		{
			name:     "clob market info",
			response: fixture(t, "clob_market_info.json"),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.GetMarketInfo(ctx, "0xabc")
			},
			wantURI: "/clob-markets/0xabc",
			check: func(t *testing.T, result any) {
				assert.Len(t, result.(MarketInfo).Tokens, 2)
			},
		},
		{
			name:     "clob server time",
			response: []byte(`1791451378`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.clob.ServerTime(ctx)
			},
			wantURI: "/time",
			check: func(t *testing.T, result any) {
				assert.Equal(t, int64(1791451378), result)
			},
		},

		// Data: multiple values are joined with commas.
		{
			name:     "data positions",
			response: fixture(t, "data_positions.json"),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.Positions(ctx, PositionsQuery{
					User: "0x1", Conditions: []string{"0xa", "0xb"}, Status: "OPEN", EventID: "9",
					SortBy: "CURRENT_VALUE", SortDirection: "DESC", Limit: 10, Cursor: "c",
				})
			},
			wantURI: "/v2/positions?condition=0xa%2C0xb&cursor=c&event_id=9&limit=10&sort_by=CURRENT_VALUE&sort_direction=DESC&status=OPEN&user=0x1",
			check: func(t *testing.T, result any) {
				assert.NotEmpty(t, result.(Page[Position]).Items)
			},
		},
		{
			name:     "data trades",
			response: fixture(t, "data_trades.json"),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.Trades(ctx, TradesQuery{
					User: "0x1", Conditions: []string{"0xa"}, EventID: "9", Side: SideBuy,
					TakerOnly: &takerOnly, Start: 100, End: 200, Limit: 2, Cursor: "c",
				})
			},
			wantURI: "/v2/trades?condition=0xa&cursor=c&end=200&event_id=9&limit=2&side=BUY&start=100&taker_only=false&user=0x1",
			check: func(t *testing.T, result any) {
				page := result.(Page[Trade])
				assert.NotEmpty(t, page.Items)
				assert.NotEmpty(t, page.NextCursor)
			},
		},
		{
			name:     "data activity",
			response: []byte(`{"data":[{"type":"TRADE","usdc_size":5.87,"price":0.97}],"pagination":{"next_cursor":"n"}}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.Activity(ctx, ActivityQuery{
					User: "0x1", Types: []string{"TRADE", "REDEEM"}, SortDirection: "ASC", Limit: 1,
				})
			},
			wantURI: "/v2/activity?limit=1&sort_direction=ASC&type=TRADE%2CREDEEM&user=0x1",
			check: func(t *testing.T, result any) {
				page := result.(Page[Activity])
				require.Len(t, page.Items, 1)
				assert.True(t, page.Items[0].USDCSize.Equal(dec("5.87")))
				assert.True(t, page.Items[0].Price.Equal(dec("0.97")), "embedded trade fields decode")
				assert.Equal(t, "n", page.NextCursor)
			},
		},
		{
			name:     "data null cursor is the last page",
			response: []byte(`{"data":[],"pagination":{"has_more":false,"next_cursor":null}}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.Positions(ctx, PositionsQuery{User: "0x1"})
			},
			wantURI: "/v2/positions?user=0x1",
			check: func(t *testing.T, result any) {
				assert.Empty(t, result.(Page[Position]).NextCursor)
			},
		},
		{
			name:     "data holders",
			response: []byte(`{"data":[{"token_id":"1","holders":[{"proxy_wallet":"0x1","amount":12.5}]}]}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.Holders(ctx, HoldersQuery{Conditions: []string{"0xa"}, Limit: 3, MinBalance: dec("10.5")})
			},
			wantURI: "/v2/holders?condition=0xa&limit=3&min_balance=10.5",
			check: func(t *testing.T, result any) {
				holders := result.([]TokenHolders)
				require.Len(t, holders, 1)
				assert.True(t, holders[0].Holders[0].Amount.Equal(dec("12.5")))
			},
		},
		{
			name:     "data value of a wallet's positions",
			response: []byte(`{"data":{"proxy_wallet":"0xw","value":1234.5678}}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.Value(ctx, "0xw")
			},
			wantURI: "/v2/value?user=0xw",
			check: func(t *testing.T, result any) {
				assert.True(t, result.(decimal.Decimal).Equal(dec("1234.5678")), "exact")
			},
		},
		{
			name:     "data open interest for conditions",
			response: []byte(`{"data":[{"condition_id":"0xa","value":1},{"condition_id":"0xb","value":2}]}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.OpenInterest(ctx, "0xa", "0xb")
			},
			wantURI: "/v2/oi?condition=0xa%2C0xb",
			check: func(t *testing.T, result any) {
				assert.Len(t, result, 2)
			},
		},
		{
			name:     "data global open interest",
			response: []byte(`{"data":[{"condition_id":"GLOBAL","value":12.5}]}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.OpenInterest(ctx)
			},
			wantURI: "/v2/oi",
			check: func(t *testing.T, result any) {
				oi := result.([]OpenInterest)
				require.Len(t, oi, 1)
				assert.Equal(t, "GLOBAL", oi[0].ConditionID)
			},
		},
		{
			name:     "data price history",
			response: []byte(`{"data":[{"timestamp":1,"price":0.0265,"resolution_seconds":3600}],"pagination":{"next_cursor":null}}`),
			call: func(ctx context.Context, c restClients) (any, error) {
				return c.data.PriceHistory(ctx, PriceHistoryQuery{TokenID: "1", Interval: "1d", BucketSeconds: 3600, Limit: 5})
			},
			wantURI: "/v2/prices-history?bucket_seconds=3600&interval=1d&limit=5&token_id=1",
			check: func(t *testing.T, result any) {
				page := result.(Page[PricePoint])
				require.Len(t, page.Items, 1)
				assert.Equal(t, 3600, page.Items[0].ResolutionSeconds)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clients, got := newRESTClients(t, observability.NoopUtility(), http.StatusOK, tt.response)

			result, err := tt.call(context.Background(), clients)
			require.NoError(t, err)

			wantMethod := tt.wantMethod
			if wantMethod == "" {
				wantMethod = http.MethodGet
			}

			assert.Equal(t, wantMethod, got.method)
			assert.Equal(t, tt.wantURI, got.uri)
			assert.Equal(t, tt.wantBody, got.body)

			if tt.check != nil {
				tt.check(t, result)
			}
		})
	}
}

func TestRESTOperationMetrics(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		op         string
		call       func(ctx context.Context, c restClients) error
		wantStatus string
		wantErrIs  error
	}{
		{
			name:   "success",
			status: http.StatusOK,
			op:     opListTags,
			call: func(ctx context.Context, c restClients) error {
				_, err := c.gamma.ListTags(ctx, TagsQuery{})
				return err
			},
			wantStatus: metrics.StatusSuccess,
		},
		{
			name:   "not found counts as success",
			status: http.StatusNotFound,
			op:     opGetMarketInfo,
			call: func(ctx context.Context, c restClients) error {
				_, err := c.clob.GetMarketInfo(ctx, "0xdead")
				return err
			},
			wantStatus: metrics.StatusSuccess,
			wantErrIs:  ErrNotFound,
		},
		{
			name:   "bad request counts as error",
			status: http.StatusBadRequest,
			op:     opPositions,
			call: func(ctx context.Context, c restClients) error {
				_, err := c.data.Positions(ctx, PositionsQuery{})
				return err
			},
			wantStatus: metrics.StatusError,
		},
		{
			name:   "server error counts as error",
			status: http.StatusInternalServerError,
			op:     opGetOrderBooks,
			call: func(ctx context.Context, c restClients) error {
				_, err := c.clob.GetOrderBooks(ctx, "1")
				return err
			},
			wantStatus: metrics.StatusError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total := metricsmocks.NewMockCounter(t)
			total.EXPECT().Inc(metrics.Labels{"operation": tt.op, "status": tt.wantStatus}).Return(nil).Once()

			util := newMockedUtil(t, map[string]*metricsmocks.MockCounter{metricOperationsTotal: total})
			clients, _ := newRESTClients(t, util, tt.status, []byte(`[]`))

			err := tt.call(context.Background(), clients)

			switch {
			case tt.wantStatus == metrics.StatusSuccess && tt.wantErrIs == nil:
				require.NoError(t, err)
			case tt.wantErrIs != nil:
				require.ErrorIs(t, err, tt.wantErrIs)
			default:
				require.Error(t, err)
			}
		})
	}
}

func TestClobEmptyBatchesSkipTheRequest(t *testing.T) {
	tests := []struct {
		name string
		call func(ctx context.Context, c *DefaultClobClient) (any, error)
		want any
	}{
		{
			name: "order books",
			call: func(ctx context.Context, c *DefaultClobClient) (any, error) { return c.GetOrderBooks(ctx) },
			want: []OrderBook(nil),
		},
		{
			name: "prices",
			call: func(ctx context.Context, c *DefaultClobClient) (any, error) { return c.GetPrices(ctx) },
			want: map[string]SidePrices{},
		},
		{
			name: "midpoints",
			call: func(ctx context.Context, c *DefaultClobClient) (any, error) { return c.GetMidpoints(ctx) },
			want: map[string]decimal.Decimal{},
		},
		{
			name: "spreads",
			call: func(ctx context.Context, c *DefaultClobClient) (any, error) { return c.GetSpreads(ctx) },
			want: map[string]decimal.Decimal{},
		},
		{
			name: "last trade prices",
			call: func(ctx context.Context, c *DefaultClobClient) (any, error) { return c.GetLastTradePrices(ctx) },
			want: []LastTrade(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clients, got := newRESTClients(t, observability.NoopUtility(), http.StatusOK, []byte(`[]`))

			result, err := tt.call(context.Background(), clients.clob)
			require.NoError(t, err)

			assert.Equal(t, tt.want, result)
			assert.Zero(t, got.hits.Load(), "no request for an empty batch")
		})
	}
}
