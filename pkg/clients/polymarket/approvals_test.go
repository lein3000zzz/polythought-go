package polymarket

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approvals.json is the Data API's answer for an address that granted nothing, as it
// answered on 2026-10-19.
const snapshotWallet = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

func TestApprovalsSnapshot(t *testing.T) {
	c, got := newRESTClients(t, observability.NoopUtility(), http.StatusOK, fixture(t, "approvals.json"))

	snap, err := c.data.Approvals(t.Context(), snapshotWallet)
	require.NoError(t, err)
	assert.Equal(t, "/v2/approvals?user="+snapshotWallet, got.uri)
	assert.Len(t, snap.Contracts, 23, "the whole catalog, perps and rewards too")
	assert.False(t, snap.CheckedAt.IsZero())

	required := ProductionContracts.TradingApprovals()
	wallet := common.HexToAddress(snapshotWallet)

	missing, err := snap.Missing(wallet, required)
	require.NoError(t, err)
	assert.Equal(t, required, missing, "nothing granted")

	// Granted rows count; an ERC-20 for less than all of it does not.
	granted := snap
	granted.Contracts = append([]ApprovalRow(nil), snap.Contracts...)
	for i, row := range granted.Contracts {
		granted.Contracts[i].Approved = true
		if row.Standard == ERC20 {
			granted.Contracts[i].Amount = "max"
		}
	}

	missing, err = granted.Missing(wallet, required)
	require.NoError(t, err)
	assert.Empty(t, missing)

	for i, row := range granted.Contracts {
		if common.HexToAddress(row.Spender) == required[0].Spender && row.Standard == ERC20 {
			granted.Contracts[i].Amount = "1000000"
		}
	}

	missing, err = granted.Missing(wallet, required)
	require.NoError(t, err)
	assert.Equal(t, []Approval{required[0]}, missing)

	_, err = snap.Missing(common.HexToAddress("0x1111111111111111111111111111111111111111"), required)
	require.Error(t, err, "another wallet's")

	short := snap
	short.Contracts = snap.Contracts[:3]
	_, err = short.Missing(wallet, required)
	require.Error(t, err, "a required row missing from the catalog")
}

// rpcServer answers JSON-RPC requests with answer, recording them.
func rpcServer(t *testing.T, answer func(method string, params []json.RawMessage, id int) string) (string, *[]string) {
	t.Helper()

	var bodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))

		type req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}

		var batch []req
		if json.Unmarshal(raw, &batch) != nil {
			var one req
			require.NoError(t, json.Unmarshal(raw, &one))
			_, _ = w.Write([]byte(answer(one.Method, one.Params, one.ID)))

			return
		}

		// Answered in reverse, as a batch may be.
		out := make([]string, 0, len(batch))
		for i := len(batch) - 1; i >= 0; i-- {
			out = append(out, answer(batch[i].Method, batch[i].Params, batch[i].ID))
		}

		_, _ = w.Write([]byte("[" + strings.Join(out, ",") + "]"))
	}))
	t.Cleanup(srv.Close)

	return srv.URL + "/key-in-path", &bodies
}

func newRPC(t *testing.T, url string) *DefaultRPCClient {
	t.Helper()

	c, err := NewRPCClient(observability.NoopUtility(), HTTPConfig{}, func() string { return url })
	require.NoError(t, err)

	return c
}

func TestRPCCall(t *testing.T) {
	url, bodies := rpcServer(t, func(_ string, _ []json.RawMessage, id int) string {
		return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"result":"0x` + strings.Repeat("0", 63) + itoa(id) + `"}`
	})

	wallet := common.HexToAddress(snapshotWallet)
	approvals := ProductionContracts.TradingApprovals()[:3]
	calls := []Call{approvals[0].Check(wallet), approvals[1].Check(wallet), approvals[2].Check(wallet)}

	got, err := newRPC(t, url).Call(t.Context(), calls)
	require.NoError(t, err)
	require.Len(t, got, 3)

	for i, r := range got {
		assert.Equal(t, byte(i+1), r[31], "in the order asked, whatever the order answered")
	}

	require.Len(t, *bodies, 1, "one batch")
	assert.Contains(t, (*bodies)[0], `"method":"eth_call"`)
	assert.Contains(t, (*bodies)[0], `"latest"`)
	assert.Contains(t, (*bodies)[0], hexutil.Encode(calls[0].Data))
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestRPCEstimateGas(t *testing.T) {
	url, bodies := rpcServer(t, func(method string, _ []json.RawMessage, id int) string {
		return `{"jsonrpc":"2.0","id":` + itoa(id) + `,"result":"0x1e240"}`
	})

	gas, err := newRPC(t, url).EstimateGas(t.Context(), common.HexToAddress(snapshotWallet), Call{To: ProductionContracts.Collateral, Data: []byte{1}})
	require.NoError(t, err)
	assert.Equal(t, uint64(123456), gas)
	assert.Contains(t, (*bodies)[0], `"from":"`+snapshotWallet+`"`)
}

func TestRPCFailures(t *testing.T) {
	call := []Call{{To: ProductionContracts.Collateral, Data: []byte{1}}}

	tests := []struct {
		name   string
		answer string
		status int
	}{
		{"an error", `[{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"execution reverted"}}]`, http.StatusOK},
		{"too few answers", `[]`, http.StatusOK},
		{"an answer to nothing", `[{"jsonrpc":"2.0","id":9,"result":"0x"}]`, http.StatusOK},
		{"not JSON-RPC", `<html>`, http.StatusOK},
		{"a refusal", `{}`, http.StatusTooManyRequests},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.answer))
			}))
			t.Cleanup(srv.Close)

			url := srv.URL + "/secret-key-123"
			_, err := newRPC(t, url).Call(t.Context(), call)
			require.ErrorIs(t, err, ErrRPC)
			assert.NotContains(t, err.Error(), "secret-key-123", "the URL may hold a key")
		})
	}

	_, err := newRPC(t, "http://127.0.0.1:1/secret-key-123").Call(context.Background(), call)
	require.ErrorIs(t, err, ErrRPC)
	assert.NotContains(t, err.Error(), "secret-key-123")
}

func TestRelayerSubmit(t *testing.T) {
	req := SubmitRequest{From: "0xOwner", Nonce: "7", Signature: "0xsig", To: "0xFactory", Type: RelayerSafe}

	t.Run("submitted once, signed with the Builder key", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusOK}, `{"state":"STATE_NEW","transactionID":"tx-1","transactionHash":null}`)

		got, err := newRelayerClient(t, base).Submit(t.Context(), testBuilder, req)
		require.NoError(t, err)
		assert.Equal(t, RelayerSubmission{State: "STATE_NEW", TransactionID: "tx-1"}, got)

		r := seen()[0]
		assert.Equal(t, "POST /submit", r.method+" "+r.path)
		assert.JSONEq(t, `{"from":"0xOwner","nonce":"7","signature":"0xsig","to":"0xFactory","type":"SAFE"}`, string(r.body))
		assert.Equal(t, "builder-key", r.header.Get("POLY_BUILDER_API_KEY"))
	})

	t.Run("not retried", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusBadGateway, http.StatusOK}, `{}`)

		_, err := newRelayerClient(t, base).Submit(t.Context(), testBuilder, req)
		require.Error(t, err)
		assert.Len(t, seen(), 1)
	})

	t.Run("failed at once", func(t *testing.T) {
		base, _ := l2Server(t, []int{http.StatusOK}, `{"state":"STATE_INVALID","transactionID":"tx-1"}`)

		_, err := newRelayerClient(t, base).Submit(t.Context(), testBuilder, req)
		require.ErrorIs(t, err, ErrRelayerRefused)
	})
}

func TestRelayerDeployed(t *testing.T) {
	base, seen := l2Server(t, []int{http.StatusOK}, `{"deployed":true}`)
	c := newRelayerClient(t, base)

	deployed, err := c.Deployed(t.Context(), testBuilder, "0xWallet", true)
	require.NoError(t, err)
	assert.True(t, deployed)
	assert.Equal(t, "address=0xWallet&type=WALLET", seen()[0].query)

	_, err = c.Deployed(t.Context(), testBuilder, "0xSafe", false)
	require.NoError(t, err)
	assert.Equal(t, "address=0xSafe", seen()[1].query)
}
