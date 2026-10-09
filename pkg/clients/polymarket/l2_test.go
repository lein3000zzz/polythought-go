package polymarket

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

// Vector from py-clob-client's tests/signing/test_hmac.py.
const (
	vectorSecret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	vectorSig    = "ZwAdJKvoYRlEKDkNMwd5BuwNNtg93kNaR_oU2HrfVvc="
)

func TestSign(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		method  string
		path    string
		body    string
		same    bool // true: must equal the official vector
		wantErr error
	}{
		{name: "official vector", secret: vectorSecret, method: "test-sign", path: "/orders", body: `{"hash": "0x123"}`, same: true},
		{name: "unpadded secret is the same key", secret: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", method: "test-sign", path: "/orders", body: `{"hash": "0x123"}`, same: true},
		{name: "body bytes are signed exactly", secret: vectorSecret, method: "test-sign", path: "/orders", body: `{"hash":"0x123"}`},
		{name: "method is signed", secret: vectorSecret, method: "GET", path: "/orders", body: `{"hash": "0x123"}`},
		{name: "path is signed", secret: vectorSecret, method: "test-sign", path: "/api/v1/orders", body: `{"hash": "0x123"}`},
		{name: "secret that is not base64url", secret: "!!!notbase64!!!", method: "GET", path: "/", wantErr: ErrInvalidSecret},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := L2Credentials{Secret: tt.secret}.Sign(1000000, tt.method, tt.path, []byte(tt.body))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)

			if tt.same {
				assert.Equal(t, vectorSig, got)
			} else {
				assert.NotEqual(t, vectorSig, got)
			}
		})
	}
}

// received is what an L2 test server saw.
type received struct {
	method, path, query string
	header              http.Header
	body                []byte
}

func l2Server(t *testing.T, statuses []int, response string) (string, func() []received) {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []received
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		mu.Lock()
		seen = append(seen, received{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
		status := statuses[min(len(seen), len(statuses))-1]
		mu.Unlock()

		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
		}

		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)

	return srv.URL, func() []received {
		mu.Lock()
		defer mu.Unlock()

		return append([]received(nil), seen...)
	}
}

var testCreds = L2Credentials{Address: "0xSigner", APIKey: "key-1", Secret: vectorSecret, Passphrase: "pass"}

// ticking returns a clock that moves one second per call.
func ticking() func() time.Time {
	var (
		mu  sync.Mutex
		now = time.Unix(1_800_000_000, 0)
	)

	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		now = now.Add(time.Second)

		return now
	}
}

func newL2Client(t *testing.T, base string, util *observability.Utility) *DefaultClobClient {
	t.Helper()

	c, err := NewClobClient(util, HTTPConfig{BaseURL: base})
	require.NoError(t, err)

	c.core.now = ticking()

	return c
}

func TestL2Requests(t *testing.T) {
	tests := []struct {
		name      string
		statuses  []int
		response  string
		call      func(c *DefaultClobClient) (any, error)
		wantPath  string
		wantQuery string
		check     func(t *testing.T, result any, err error, seen []received)
	}{
		{
			name: "api keys", statuses: []int{200}, response: `{"apiKeys":["key-1","key-2"]}`,
			call:     func(c *DefaultClobClient) (any, error) { return c.APIKeys(context.Background(), testCreds) },
			wantPath: "/auth/api-keys",
			check: func(t *testing.T, result any, err error, _ []received) {
				require.NoError(t, err)
				assert.Equal(t, []string{"key-1", "key-2"}, result)
			},
		},
		{
			name: "open orders start at the first cursor and end at LTE=", statuses: []int{200},
			response: `{"data":[{"id":"0xo","status":"LIVE","side":"BUY","price":"0.5","original_size":"10","size_matched":"2","created_at":1700000000}],"next_cursor":"LTE="}`,
			call: func(c *DefaultClobClient) (any, error) {
				return c.OpenOrders(context.Background(), testCreds, OpenOrdersQuery{Market: "0xm"})
			},
			wantPath: "/data/orders", wantQuery: "market=0xm&next_cursor=MA%3D%3D",
			check: func(t *testing.T, result any, err error, _ []received) {
				require.NoError(t, err)

				page := result.(Page[OpenOrder])
				require.Len(t, page.Items, 1)
				assert.Empty(t, page.NextCursor)
				assert.Equal(t, "8", page.Items[0].OriginalSize.Sub(page.Items[0].SizeMatched).String())
			},
		},
		{
			name: "balance", statuses: []int{200}, response: `{"balance":"1500000","allowances":{"0xexchange":"115792089237316195423570985008687907853269984665640564039457584007913129639935"}}`,
			call: func(c *DefaultClobClient) (any, error) {
				return c.BalanceAllowance(context.Background(), testCreds, BalanceQuery{AssetType: AssetCollateral, SignatureType: 1})
			},
			wantPath: "/balance-allowance", wantQuery: "asset_type=COLLATERAL&signature_type=1",
			check: func(t *testing.T, result any, err error, _ []received) {
				require.NoError(t, err)
				assert.Equal(t, "1.5", result.(BalanceAllowance).Balance.Shift(-6).String())
			},
		},
		{
			name: "rejected credentials", statuses: []int{401}, response: `{"error":"Unauthorized/Invalid api key"}`,
			call:     func(c *DefaultClobClient) (any, error) { return c.APIKeys(context.Background(), testCreds) },
			wantPath: "/auth/api-keys",
			check: func(t *testing.T, _ any, err error, _ []received) {
				require.ErrorIs(t, err, ErrUnauthorized)
				assert.Contains(t, err.Error(), "Invalid api key")
			},
		},
		{
			name: "retries are signed afresh", statuses: []int{429, 200}, response: `{"apiKeys":[]}`,
			call:     func(c *DefaultClobClient) (any, error) { return c.APIKeys(context.Background(), testCreds) },
			wantPath: "/auth/api-keys",
			check: func(t *testing.T, _ any, err error, seen []received) {
				require.NoError(t, err)
				require.Len(t, seen, 2)
				assert.NotEqual(t, seen[0].header.Get(headerTimestamp), seen[1].header.Get(headerTimestamp))
				assert.NotEqual(t, seen[0].header.Get(headerSignature), seen[1].header.Get(headerSignature))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, seen := l2Server(t, tt.statuses, tt.response)

			result, err := tt.call(newL2Client(t, base, observability.NoopUtility()))

			requests := seen()
			require.NotEmpty(t, requests)

			for _, r := range requests {
				assert.Equal(t, http.MethodGet, r.method)
				assert.Equal(t, tt.wantPath, r.path)
				assert.Equal(t, tt.wantQuery, r.query)
				assert.Equal(t, "0xSigner", r.header.Get(headerAddress))
				assert.Equal(t, "key-1", r.header.Get(headerAPIKey))
				assert.Equal(t, "pass", r.header.Get(headerPassphrase))

				// The server can recompute the signature from what it received: the path
				// without the query, the method and the exact body.
				var ts int64
				_, scanErr := fmt.Sscan(r.header.Get(headerTimestamp), &ts)
				require.NoError(t, scanErr)

				want, signErr := testCreds.Sign(ts, r.method, r.path, r.body)
				require.NoError(t, signErr)
				assert.Equal(t, want, r.header.Get(headerSignature))
			}

			tt.check(t, result, err, requests)
		})
	}
}

func TestRejectedCredentialsAreNotServiceFailures(t *testing.T) {
	base, _ := l2Server(t, []int{401}, `{}`)

	total := metricsmocks.NewMockCounter(t)
	total.EXPECT().Inc(metrics.Labels{"operation": opAPIKeys, "status": metrics.StatusSuccess}).Return(nil).Once()

	_, err := newL2Client(t, base, newMockedUtil(t, map[string]*metricsmocks.MockCounter{metricOperationsTotal: total})).
		APIKeys(context.Background(), testCreds)
	require.ErrorIs(t, err, ErrUnauthorized)
}
