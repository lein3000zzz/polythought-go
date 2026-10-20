package polymarket

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The CLOB faked from its docs and the official client's schemas (D196): every answer
// below is one the docs show, or the client reads. Nothing calls the real CLOB.

// step is what the fake CLOB answers to the next request, which must be method on a path
// starting with path.
type step struct {
	method, path string
	status       int
	body         string
	header       map[string]string
	hangUp       bool // close the connection without answering
}

// clob is a fake CLOB answering steps in turn; the test fails on a request out of turn,
// or a step left unasked.
func clob(t *testing.T, steps ...step) (*DefaultClobClient, func() []received) {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []received
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		seen = append(seen, received{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		n := len(seen)
		mu.Unlock()

		if n > len(steps) {
			t.Errorf("request %d (%s %s) out of turn", n, r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)

			return
		}

		s := steps[n-1]
		if r.Method != s.method || !strings.HasPrefix(r.URL.Path, s.path) {
			t.Errorf("request %d: %s %s, want %s %s", n, r.Method, r.URL.Path, s.method, s.path)
		}

		if s.hangUp {
			conn, _, err := w.(http.Hijacker).Hijack()
			require.NoError(t, err)
			_ = conn.Close()

			return
		}

		for k, v := range s.header {
			w.Header().Set(k, v)
		}

		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(srv.Close)

	c := newL2Client(t, srv.URL, observability.NoopUtility())
	c.lookupWait = 0

	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		assert.Len(t, seen, len(steps), "requests the fake CLOB was ready for")
	})

	return c, func() []received {
		mu.Lock()
		defer mu.Unlock()

		return append([]received(nil), seen...)
	}
}

// testPost is a FAK buy the official client signed, and its ID.
func testPost(t *testing.T) (OrderPost, string) {
	t.Helper()

	return vectorPost(t, loadVectors(t), "an EOA, a buy, on ctf", OrderFAK, 0, false)
}

func vectorPost(t *testing.T, v vectors, name string, typ OrderType, expiration int64, postOnly bool) (OrderPost, string) {
	t.Helper()

	sv := v.signature(t, name)
	o, ex := sv.order(t)
	p := OrderPost{Order: SignedOrder{Order: o, Signature: sv.Signature, Expiration: expiration}, Exchange: ex, Type: typ, PostOnly: postOnly}

	id, err := p.ID()
	require.NoError(t, err)

	return p, id
}

func answer(id, status string) string {
	return fmt.Sprintf(`{"success":true,"errorMsg":"","orderID":%q,"status":%q,"makingAmount":"5.5","takingAmount":"10","tradeIDs":["trade-1"],"transactionsHashes":[]}`, id, status)
}

func openOrder(id, status string) string {
	return fmt.Sprintf(`{"id":%q,"status":%q,"market":"0xm","asset_id":"1","side":"BUY","original_size":"10","size_matched":"4","price":"0.55","order_type":"FAK","associate_trades":["trade-1","trade-2"],"created_at":1760875200}`, id, status)
}

// The bodies are the official client's own, sent to the paths it sends them to.
func TestOrderBodiesMatchTheOfficialClient(t *testing.T) {
	v := loadVectors(t)
	require.NotEmpty(t, v.Posts)

	creds := testCreds
	creds.APIKey = v.Posts[0].Owner

	for _, tc := range v.Posts {
		t.Run(tc.Name, func(t *testing.T) {
			var (
				posts   []OrderPost
				answers []string
			)

			for _, o := range tc.Orders {
				p, id := vectorPost(t, v, o.Vector, o.OrderType, o.Expiration, o.PostOnly)
				posts, answers = append(posts, p), append(answers, answer(id, "live"))
			}

			body := answers[0]
			if len(posts) > 1 {
				body = "[" + strings.Join(answers, ",") + "]"
			}

			c, seen := clob(t, step{method: tc.Method, path: tc.Path, status: 200, body: body})

			var err error
			if len(posts) == 1 {
				_, err = c.PostOrder(context.Background(), creds, posts[0])
			} else {
				var results []PostResult
				results, err = c.PostOrders(context.Background(), creds, posts)

				for _, r := range results {
					require.NoError(t, r.Err)
				}
			}

			require.NoError(t, err)

			got := seen()[0]
			assert.Equal(t, tc.Path, got.path)
			assert.JSONEq(t, string(tc.Body), string(got.body))
			assertSigned(t, got, creds)
		})
	}
}

// assertSigned checks the request carries L2 headers the server can check against what
// it received.
func assertSigned(t *testing.T, r received, creds L2Credentials) {
	t.Helper()

	assert.Equal(t, creds.Address, r.header.Get(headerAddress))
	assert.Equal(t, creds.APIKey, r.header.Get(headerAPIKey))

	var ts int64
	_, err := fmt.Sscan(r.header.Get(headerTimestamp), &ts)
	require.NoError(t, err)

	want, err := creds.Sign(ts, r.method, r.path, r.body)
	require.NoError(t, err)
	assert.Equal(t, want, r.header.Get(headerSignature))
}

func TestPostOrder(t *testing.T) {
	p, id := testPost(t)

	refused := func(code RefusalCode, status int) func(t *testing.T, o PostedOrder, err error) {
		return func(t *testing.T, o PostedOrder, err error) {
			var r *OrderRefusedError
			require.ErrorAs(t, err, &r)
			assert.Equal(t, code, r.Code)
			assert.Equal(t, status, r.StatusCode)
			assert.Equal(t, id, r.ID)
		}
	}

	posted := func(status PostStatus, found bool) func(t *testing.T, o PostedOrder, err error) {
		return func(t *testing.T, o PostedOrder, err error) {
			require.NoError(t, err)
			assert.Equal(t, id, o.ID)
			assert.Equal(t, status, o.Status)
			assert.Equal(t, found, o.Found != nil)
		}
	}

	post := step{method: http.MethodPost, path: "/order"}
	at := func(s step, status int, body string) step { s.status, s.body = status, body; return s }
	lookup := step{method: http.MethodGet, path: "/data/order/" + id}

	tests := []struct {
		name  string
		steps []step
		check func(t *testing.T, o PostedOrder, err error)
	}{
		{
			name: "matched at once", steps: []step{at(post, 200, answer(id, "matched"))},
			check: func(t *testing.T, o PostedOrder, err error) {
				posted(PostMatched, false)(t, o, err)
				assert.Equal(t, "5.5", o.MakingAmount.Decimal.String())
				assert.Equal(t, []string{"trade-1"}, o.TradeIDs)
			},
		},
		{name: "resting", steps: []step{at(post, 200, answer(id, "live"))}, check: posted(PostLive, false)},
		{name: "in the market's delay", steps: []step{at(post, 200, answer(id, "delayed"))}, check: posted(PostDelayed, false)},
		{name: "resting after its delay, which the docs count placed", steps: []step{at(post, 200, answer(id, "unmatched"))}, check: posted(PostUnmatched, false)},
		{
			name: "a FAK with nothing to match", check: refused(RefusedFAKNotFilled, 200),
			steps: []step{at(post, 200, `{"success":false,"errorMsg":"no orders found to match with FAK order. FAK orders are partially filled or killed if no match is found.","orderID":"","status":"","makingAmount":"","takingAmount":""}`)},
		},
		{
			name: "post-only mode, which says success", check: refused(RefusedPostOnlyMode, 200),
			steps: []step{at(post, 200, `{"success":true,"errorMsg":"post-only mode: only post-only orders and cancels are allowed","orderID":"","status":"","makingAmount":"","takingAmount":""}`)},
		},
		{
			name: "success with no ID", check: refused(RefusedOther, 200),
			steps: []step{at(post, 200, `{"success":true,"errorMsg":"","orderID":"","status":"matched","makingAmount":"","takingAmount":""}`)},
		},
		{name: "not enough balance", steps: []step{at(post, 400, `{"error":"not enough balance / allowance"}`)}, check: refused(RefusedBalance, 400)},
		{
			name: "under the market's least order", check: refused(RefusedMinSize, 400),
			steps: []step{at(post, 400, `{"error":"order `+id+` is invalid. Size (1) lower than the minimum: 5"}`)},
		},
		{
			name: "off the tick", check: refused(RefusedTick, 400),
			steps: []step{at(post, 400, `{"error":"order `+id+` is invalid. Price (0.555) breaks minimum tick size rule: 0.01"}`)},
		},
		{name: "close-only", steps: []step{at(post, 400, `{"error":"'0x1234' address in closed only mode"}`)}, check: refused(RefusedCloseOnly, 400)},
		{name: "a region close-only", steps: []step{at(post, 403, `{"code":"restricted_region_close_only","message":"Trading is restricted in your region"}`)}, check: refused(RefusedCloseOnly, 403)},
		{name: "the API key's signer", steps: []step{at(post, 400, `{"error":"the order signer address has to be the address of the API KEY"}`)}, check: refused(RefusedCredentials, 400)},
		{
			name: "cancel-only", check: refused(RefusedCancelOnly, 503),
			steps: []step{at(post, 503, `{"error":"Trading is currently cancel-only. New orders are not accepted, but cancels are allowed."}`)},
		},
		{
			name: "post-only mode, saying when", steps: []step{{
				method: http.MethodPost, path: "/order", status: 503, header: map[string]string{"Retry-After": "79"},
				body: `{"error":"post-only mode: only post-only orders and cancels are allowed","code":"post_only_mode","retry_after_seconds":79}`,
			}},
			check: func(t *testing.T, o PostedOrder, err error) {
				refused(RefusedPostOnlyMode, 503)(t, o, err)

				var r *OrderRefusedError
				require.ErrorAs(t, err, &r)
				assert.Equal(t, 79*time.Second, r.RetryAfter)
			},
		},
		{
			name: "rate limited, then placed", check: posted(PostMatched, false),
			steps: []step{
				{method: http.MethodPost, path: "/order", status: 429, header: map[string]string{"Retry-After": "0"}},
				at(post, 200, answer(id, "matched")),
			},
		},
		{
			name: "the engine restarting, and still after the retries", check: refused(RefusedRestarting, 425),
			steps: []step{
				{method: http.MethodPost, path: "/order", status: 425, header: map[string]string{"Retry-After": "0"}},
				{method: http.MethodPost, path: "/order", status: 425, header: map[string]string{"Retry-After": "0"}},
				{method: http.MethodPost, path: "/order", status: 425, header: map[string]string{"Retry-After": "0"}},
				{method: http.MethodPost, path: "/order", status: 425, header: map[string]string{"Retry-After": "0"}},
			},
		},
		{
			name: "placed before: the duplicate refused, the one there found", check: posted(PostLive, true),
			steps: []step{
				at(post, 400, `{"error":"order `+id+` is invalid. Duplicated."}`),
				at(lookup, 200, openOrder(id, "LIVE")),
			},
		},
		{
			name: "no answer from the gateway, the order found", steps: []step{at(post, 502, "Bad Gateway"), at(lookup, 200, openOrder(id, "MATCHED"))},
			check: func(t *testing.T, o PostedOrder, err error) {
				posted(PostMatched, true)(t, o, err)
				assert.Equal(t, []string{"trade-1", "trade-2"}, o.TradeIDs)
				assert.Equal(t, "4", o.Found.SizeMatched.String())
			},
		},
		{
			name: "the connection dropped, the order found done", check: posted(PostDone, true),
			steps: []step{{method: http.MethodPost, path: "/order", hangUp: true}, at(lookup, 200, openOrder(id, "CANCELED"))},
		},
		{
			name: "timed out and not there: sent again, once", check: posted(PostMatched, false),
			steps: []step{
				at(post, 500, `{"error":"order timed out"}`),
				at(lookup, 404, `{"error":"Order not found"}`),
				at(post, 200, answer(id, "matched")),
			},
		},
		{
			name: "an answer that cannot be read, the order found", check: posted(PostLive, true),
			steps: []step{at(post, 200, `<html>`), at(lookup, 200, openOrder(id, "LIVE"))},
		},
		{
			name: "the gateway not knowing", check: posted(PostLive, true),
			steps: []step{at(post, 502, `{"code":"submission_outcome_unknown","message":"unknown"}`), at(lookup, 200, openOrder(id, "LIVE"))},
		},
		{
			name: "sent again, and the first had landed after all", check: posted(PostLive, true),
			steps: []step{
				at(post, 504, ""),
				at(lookup, 200, "null"),
				at(post, 400, `{"error":"order `+id+` is invalid. Duplicated."}`),
				at(lookup, 200, openOrder(id, "LIVE")),
			},
		},
		{
			name: "twice without an answer and not found: in doubt",
			steps: []step{
				at(post, 504, ""),
				at(lookup, 404, `{"error":"Order not found"}`),
				at(post, 504, ""),
				at(lookup, 404, `{"error":"Order not found"}`),
			},
			check: func(t *testing.T, _ PostedOrder, err error) {
				var u *OrderUncertainError
				require.ErrorAs(t, err, &u)
				assert.Equal(t, id, u.ID)
				assert.ErrorContains(t, err, "sent twice without an answer")
			},
		},
		{
			name: "no answer, and the lookup failing: in doubt",
			steps: []step{
				at(post, 502, ""),
				at(lookup, 500, `{"error":"Internal server error"}`),
			},
			check: func(t *testing.T, _ PostedOrder, err error) {
				var u *OrderUncertainError
				require.ErrorAs(t, err, &u)
				assert.ErrorContains(t, err, "http 500")
			},
		},
		{
			name: "credentials rejected", steps: []step{at(post, 401, `{"error":"Invalid API key"}`)},
			check: func(t *testing.T, _ PostedOrder, err error) { require.ErrorIs(t, err, ErrUnauthorized) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, seen := clob(t, tt.steps...)

			o, err := c.PostOrder(context.Background(), testCreds, p)
			tt.check(t, o, err)

			// Every send is the same signed order, signed for its own moment.
			var bodies []string
			for _, r := range seen() {
				assertSigned(t, r, testCreds)

				if r.method == http.MethodPost {
					bodies = append(bodies, string(r.body))
				}
			}

			for _, b := range bodies {
				assert.Equal(t, bodies[0], b)
			}
		})
	}
}

func TestPostOrderChecksTheOrderFirst(t *testing.T) {
	p, _ := testPost(t)

	gtd := p
	gtd.Type = OrderGTD

	expiring := p
	expiring.Type, expiring.Order.Expiration = OrderGTC, 1760875500

	postOnly := p
	postOnly.PostOnly = true

	unknown := p
	unknown.Type = "IOC"

	for name, bad := range map[string]OrderPost{
		"a GTD without an expiration": gtd, "a GTC with one": expiring, "a post-only FAK": postOnly, "an unknown type": unknown,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := clob(t)

			_, err := c.PostOrder(context.Background(), testCreds, bad)
			require.Error(t, err)

			var r *OrderRefusedError
			assert.NotErrorAs(t, err, &r)
		})
	}
}

func TestPostOrders(t *testing.T) {
	v := loadVectors(t)
	a, idA := vectorPost(t, v, "a Deposit Wallet, a buy, on negRisk", OrderFAK, 0, false)
	b, idB := vectorPost(t, v, "an EOA with a builder code and metadata", OrderGTC, 0, false)
	c3, idC := vectorPost(t, v, "a Safe, a sell, on v3", OrderGTC, 0, true)

	refusedWith := func(t *testing.T, r PostResult, code RefusalCode, id string) {
		t.Helper()

		var refused *OrderRefusedError
		require.ErrorAs(t, r.Err, &refused)
		assert.Equal(t, code, refused.Code)
		assert.Equal(t, id, refused.ID)
	}

	t.Run("answers out of order, refused ones without an ID", func(t *testing.T) {
		c, _ := clob(t, step{method: http.MethodPost, path: "/orders", status: 200, body: "[" + answer(idC, "live") + "," +
			`{"success":false,"orderID":"","status":"delayed","errorMsg":"Rate limit exceeded for tokenId: 1"}` + "," +
			answer(idA, "matched") + "]"})

		results, err := c.PostOrders(context.Background(), testCreds, []OrderPost{a, b, c3})
		require.NoError(t, err)
		require.Len(t, results, 3)

		assert.Equal(t, PostMatched, results[0].Order.Status)
		refusedWith(t, results[1], RefusedRateLimited, idB)
		assert.Equal(t, PostLive, results[2].Order.Status)
	})

	t.Run("one placed before, found", func(t *testing.T) {
		c, _ := clob(t,
			step{method: http.MethodPost, path: "/orders", status: 200, body: "[" + answer(idA, "matched") + "," +
				`{"success":false,"orderID":"","status":"","errorMsg":"order ` + idB + ` is invalid. Duplicated."}]`},
			step{method: http.MethodGet, path: "/data/order/" + idB, status: 200, body: openOrder(idB, "LIVE")},
		)

		results, err := c.PostOrders(context.Background(), testCreds, []OrderPost{a, b})
		require.NoError(t, err)
		require.NoError(t, results[0].Err)
		require.NoError(t, results[1].Err)
		assert.NotNil(t, results[1].Order.Found)
	})

	t.Run("all refused at once", func(t *testing.T) {
		c, _ := clob(t, step{method: http.MethodPost, path: "/orders", status: 400, body: `{"error":"Invalid order payload"}`})

		results, err := c.PostOrders(context.Background(), testCreds, []OrderPost{a, b})
		require.NoError(t, err)
		refusedWith(t, results[0], RefusedInvalid, idA)
		refusedWith(t, results[1], RefusedInvalid, idB)
	})

	t.Run("no answer: the one found stays, the other sent again alone", func(t *testing.T) {
		c, seen := clob(t,
			step{method: http.MethodPost, path: "/orders", status: 502},
			step{method: http.MethodGet, path: "/data/order/" + idA, status: 200, body: openOrder(idA, "MATCHED")},
			step{method: http.MethodGet, path: "/data/order/" + idB, status: 404, body: `{"error":"Order not found"}`},
			step{method: http.MethodPost, path: "/orders", status: 200, body: "[" + answer(idB, "live") + "]"},
		)

		results, err := c.PostOrders(context.Background(), testCreds, []OrderPost{a, b})
		require.NoError(t, err)
		assert.Equal(t, PostMatched, results[0].Order.Status)
		assert.NotNil(t, results[0].Order.Found)
		assert.Equal(t, PostLive, results[1].Order.Status)

		var again []map[string]any
		require.NoError(t, json.Unmarshal(seen()[3].body, &again))
		require.Len(t, again, 1)
		assert.Equal(t, "GTC", again[0]["orderType"])
	})

	t.Run("too few or too many", func(t *testing.T) {
		c, _ := clob(t)

		_, err := c.PostOrders(context.Background(), testCreds, nil)
		require.Error(t, err)

		_, err = c.PostOrders(context.Background(), testCreds, make([]OrderPost, MaxBatchOrders+1))
		require.Error(t, err)
	})
}

func TestOrder(t *testing.T) {
	const id = "0x00000000000000000000000000000000000000000000000000000000000000a1"

	t.Run("any status", func(t *testing.T) {
		c, seen := clob(t, step{method: http.MethodGet, path: "/data/order/" + id, status: 200, body: openOrder(id, "CANCELED")})

		o, err := c.Order(context.Background(), testCreds, id)
		require.NoError(t, err)
		assert.Equal(t, "CANCELED", o.Status)
		assert.Equal(t, "/data/order/"+id, seen()[0].path)
	})

	for name, s := range map[string]step{
		"not found":         {method: http.MethodGet, path: "/data/order/", status: 404, body: `{"error":"Order not found"}`},
		"nothing in a 200":  {method: http.MethodGet, path: "/data/order/", status: 200, body: "null"},
		"an empty in a 200": {method: http.MethodGet, path: "/data/order/", status: 200, body: "{}"},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := clob(t, s)

			_, err := c.Order(context.Background(), testCreds, id)
			require.ErrorIs(t, err, ErrNotFound)
		})
	}

	t.Run("no ID that could leave its path", func(t *testing.T) {
		c, _ := clob(t)

		_, err := c.Order(context.Background(), testCreds, "../cancel-all")
		require.Error(t, err)
	})
}

func TestTrades(t *testing.T) {
	c, seen := clob(t, step{method: http.MethodGet, path: "/data/trades", status: 200, body: `{
		"limit": 100, "count": 1, "next_cursor": "LTE=",
		"data": [{
			"id": "trade-1", "taker_order_id": "0xtaker", "market": "0xm", "asset_id": "1", "side": "BUY",
			"size": "10", "price": "0.55", "fee_rate_bps": "", "status": "TRADE_STATUS_MINED",
			"match_time": "1760875200", "last_update": 1760875203000, "outcome": "Yes", "bucket_index": 0,
			"owner": "key-1", "maker_address": "0xwallet", "transaction_hash": "0xtx", "trader_side": "MAKER",
			"err_msg": null,
			"maker_orders": [{"order_id": "0xmine", "owner": "key-1", "maker_address": "0xwallet",
				"matched_amount": "4", "price": "0.55", "fee_rate_bps": "", "asset_id": "1", "outcome": "Yes", "side": "SELL"}]
		}]
	}`})

	page, err := c.Trades(context.Background(), testCreds, ClobTradesQuery{ID: "trade-1", MakerAddress: "0xwallet", After: 1760875000})
	require.NoError(t, err)

	assert.Equal(t, "after=1760875000&id=trade-1&maker_address=0xwallet&next_cursor=MA%3D%3D", seen()[0].query)
	assert.Empty(t, page.NextCursor)
	require.Len(t, page.Items, 1)

	tr := page.Items[0]
	assert.Equal(t, TradeMined, tr.Status)
	assert.False(t, tr.Status.Final())
	assert.False(t, tr.FeeRateBps.Valid)
	assert.Equal(t, time.Unix(1760875200, 0).UTC(), tr.MatchTime.Time)
	assert.Equal(t, time.Unix(1760875203, 0).UTC(), tr.LastUpdate.Time)
	assert.Equal(t, TraderMaker, tr.TraderSide)
	require.Len(t, tr.MakerOrders, 1)
	assert.Equal(t, "4", tr.MakerOrders[0].MatchedAmount.String())
}

func TestTradeStatus(t *testing.T) {
	for raw, want := range map[string]TradeStatus{
		"TRADE_STATUS_CONFIRMED": TradeConfirmed, "CONFIRMED": TradeConfirmed, "TRADE_STATUS_FAILED": TradeFailed,
		"MATCHED": TradeMatched, "TRADE_STATUS_RETRYING": TradeRetrying, "TRADE_STATUS_MATCHED_NOT_BROADCASTED": TradeMatchedNotBroadcasted,
	} {
		var s TradeStatus
		require.NoError(t, json.Unmarshal([]byte(`"`+raw+`"`), &s))
		assert.Equal(t, want, s, raw)
		assert.Equal(t, want == TradeConfirmed || want == TradeFailed, s.Final(), raw)
	}
}

func TestEpochTime(t *testing.T) {
	at := time.Unix(1760875200, 0).UTC()

	for raw, want := range map[string]time.Time{
		`1760875200`: at, `"1760875200"`: at, `1760875200000`: at, `"1760875200000"`: at,
		`"2025-10-19T12:00:00Z"`: at, `""`: {}, `null`: {}, `0`: {}, `"0"`: {},
	} {
		var e EpochTime
		require.NoError(t, json.Unmarshal([]byte(raw), &e), raw)
		assert.Equal(t, want, e.Time, raw)
	}

	var e EpochTime
	require.Error(t, json.Unmarshal([]byte(`"soon"`), &e))
}

func TestCancelBodiesMatchTheOfficialClient(t *testing.T) {
	v := loadVectors(t)
	require.Len(t, v.Cancels, 6)

	ids := []string{"0x" + strings.Repeat("a1", 32), "0x" + strings.Repeat("b2", 32)}
	market := "0x" + strings.Repeat("5c", 32)
	asset := "71321045679252212594626385532706912750332728571942532289631379312455583992563"

	calls := map[string]func(c *DefaultClobClient) (Cancellation, error){
		"one order": func(c *DefaultClobClient) (Cancellation, error) {
			return c.CancelOrder(context.Background(), testCreds, ids[0])
		},
		"orders": func(c *DefaultClobClient) (Cancellation, error) {
			return c.CancelOrders(context.Background(), testCreds, ids)
		},
		"all": func(c *DefaultClobClient) (Cancellation, error) { return c.CancelAll(context.Background(), testCreds) },
		"a market's": func(c *DefaultClobClient) (Cancellation, error) {
			return c.CancelMarketOrders(context.Background(), testCreds, CancelMarketQuery{Market: market})
		},
		"an outcome's": func(c *DefaultClobClient) (Cancellation, error) {
			return c.CancelMarketOrders(context.Background(), testCreds, CancelMarketQuery{AssetID: asset})
		},
		"an outcome's in its market": func(c *DefaultClobClient) (Cancellation, error) {
			return c.CancelMarketOrders(context.Background(), testCreds, CancelMarketQuery{Market: market, AssetID: asset})
		},
	}

	for _, tc := range v.Cancels {
		t.Run(tc.Name, func(t *testing.T) {
			call, ok := calls[tc.Name]
			require.True(t, ok)

			c, seen := clob(t, step{method: tc.Method, path: tc.Path, status: 200,
				body: `{"canceled":["` + ids[0] + `"],"not_canceled":{"` + ids[1] + `":"Order already matched"}}`})

			out, err := call(c)
			require.NoError(t, err)
			assert.Equal(t, []string{ids[0]}, out.Canceled)
			assert.Equal(t, map[string]string{ids[1]: "Order already matched"}, out.NotCanceled)

			got := seen()[0]
			if string(tc.Body) == "null" {
				assert.Empty(t, got.body)
			} else {
				assert.JSONEq(t, string(tc.Body), string(got.body))
			}

			assertSigned(t, got, testCreds)
		})
	}
}

func TestCancelsAreRetriedAndChecked(t *testing.T) {
	t.Run("retried on the gateway", func(t *testing.T) {
		c, _ := clob(t,
			step{method: http.MethodDelete, path: "/cancel-all", status: 502, header: map[string]string{"Retry-After": "0"}},
			step{method: http.MethodDelete, path: "/cancel-all", status: 200, body: `{"canceled":[],"not_canceled":{}}`},
		)

		_, err := c.CancelAll(context.Background(), testCreds)
		require.NoError(t, err)
	})

	t.Run("nothing to cancel, or too much", func(t *testing.T) {
		c, _ := clob(t)

		_, err := c.CancelOrder(context.Background(), testCreds, "")
		require.Error(t, err)

		_, err = c.CancelOrders(context.Background(), testCreds, nil)
		require.Error(t, err)

		_, err = c.CancelOrders(context.Background(), testCreds, make([]string, MaxBatchCancels+1))
		require.Error(t, err)

		_, err = c.CancelMarketOrders(context.Background(), testCreds, CancelMarketQuery{})
		require.Error(t, err)
	})
}

// The refusals the docs and the official client word, read as they mean.
func TestRefusalCodes(t *testing.T) {
	for text, want := range map[string]RefusalCode{
		"not enough balance / allowance":                                           RefusedBalance,
		"not enough balance / allowance: the allowance is not enough -> 0x1234":    RefusedBalance,
		"order 0xab is invalid. Price (0.555) breaks minimum tick size rule: 0.01": RefusedTick,
		"order 0xab is invalid. Size (1) lower than the minimum: 5":                RefusedMinSize,
		"order 0xab is invalid. Duplicated.":                                       RefusedDuplicate,
		"invalid post-only order: order crosses book":                              RefusedPostOnlyCross,
		"post-only mode: only post-only orders and cancels are allowed":            RefusedPostOnlyMode,
		"invalid expiration": RefusedExpiration,
		"the market is not yet ready to process new orders":                                                        RefusedMarketNotReady,
		"order couldn't be fully filled. FOK orders are fully filled or killed.":                                   RefusedFOKNotFilled,
		"no orders found to match with FAK order. FAK orders are partially filled or killed if no match is found.": RefusedFAKNotFilled,
		"'0x1234' address in closed only mode":                                                                     RefusedCloseOnly,
		"'0x1234' address banned":                                                                                  RefusedBanned,
		"Trading is currently disabled. Check polymarket.com for updates":                                          RefusedTradingDisabled,
		"Trading is currently cancel-only. New orders are not accepted, but cancels are allowed.":                  RefusedCancelOnly,
		"the order owner has to be the owner of the API KEY":                                                       RefusedCredentials,
		"the order signer address has to be the address of the API KEY":                                            RefusedCredentials,
		"Rate limit exceeded for tokenId: 1":                                                                       RefusedRateLimited,
		"Invalid order payload":                                                                                    RefusedInvalid,
		"invalid nonce":                                                                                            RefusedInvalid,
		"something new":                                                                                            RefusedOther,
	} {
		assert.Equal(t, want, refusalCode(http.StatusBadRequest, text, ""), text)
	}

	assert.Equal(t, RefusedRestarting, refusalCode(http.StatusTooEarly, "", ""))
	assert.Equal(t, RefusedRateLimited, refusalCode(http.StatusTooManyRequests, "", ""))
	assert.Equal(t, RefusedCloseOnly, refusalCode(http.StatusForbidden, "", "restricted_region_close_only"))
	assert.Equal(t, RefusedMinSize, refusalCode(http.StatusBadRequest, "", "order_too_small"))
}

// A refusal is an answer about the order, not the service failing; an order in doubt is.
func TestRefusalsAreNotServiceFailures(t *testing.T) {
	assert.True(t, placedOrRefused(nil))
	assert.True(t, placedOrRefused(&OrderRefusedError{Code: RefusedBalance}))
	assert.True(t, placedOrRefused(fmt.Errorf("wrapped: %w", ErrUnauthorized)))
	assert.False(t, placedOrRefused(&OrderUncertainError{ID: "0x1", Err: io.EOF}))
}

func TestHeartbeat(t *testing.T) {
	t.Run("the first arms it, each gives the next", func(t *testing.T) {
		c, seen := clob(t,
			step{method: http.MethodPost, path: "/v1/heartbeats", status: 200, body: `{"heartbeat_id":"hb-1"}`},
			step{method: http.MethodPost, path: "/v1/heartbeats", status: 200, body: `{"heartbeat_id":"hb-2"}`},
		)

		next, err := c.Heartbeat(context.Background(), testCreds, "")
		require.NoError(t, err)
		assert.Equal(t, "hb-1", next)

		next, err = c.Heartbeat(context.Background(), testCreds, next)
		require.NoError(t, err)
		assert.Equal(t, "hb-2", next)

		got := seen()
		assert.JSONEq(t, `{"heartbeat_id":""}`, string(got[0].body))
		assert.JSONEq(t, `{"heartbeat_id":"hb-1"}`, string(got[1].body))
		assertSigned(t, got[1], testCreds)
	})

	t.Run("a mismatch says the ID expected", func(t *testing.T) {
		c, _ := clob(t, step{method: http.MethodPost, path: "/v1/heartbeats", status: 400,
			body: `{"error_msg":"Invalid Heartbeat ID","heartbeat_id":"hb-7"}`})

		_, err := c.Heartbeat(context.Background(), testCreds, "hb-1")

		var mismatch *HeartbeatMismatchError
		require.ErrorAs(t, err, &mismatch)
		assert.Equal(t, "hb-7", mismatch.Expected)
	})

	t.Run("not retried", func(t *testing.T) {
		c, _ := clob(t, step{method: http.MethodPost, path: "/v1/heartbeats", status: 502})

		_, err := c.Heartbeat(context.Background(), testCreds, "hb-1")
		require.Error(t, err)
	})
}
