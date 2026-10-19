package polymarket

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sessionVectors struct {
	Keys           struct{ Owner, Session string }
	SessionBatches []struct {
		Name, Owner, Wallet, SessionSigner string
		ValidUntil                         int64
		Nonce, Deadline                    string
		Calls                              []DepositWalletCall
		Digest, Signature                  string
	}
	BuilderHmac []struct {
		Secret    string
		Timestamp int64
		Method    string
		Path      string
		Body      string
		Signature string
	}
}

func loadSessionVectors(t *testing.T) sessionVectors {
	t.Helper()

	var v sessionVectors
	require.NoError(t, json.Unmarshal(fixture(t, "order_vectors.json"), &v))
	require.Len(t, v.SessionBatches, 2)
	require.NotEmpty(t, v.BuilderHmac)

	return v
}

func TestSessionBatchesMatchTheOfficialClient(t *testing.T) {
	v := loadSessionVectors(t)

	key, err := crypto.HexToECDSA(v.Keys.Owner[2:])
	require.NoError(t, err)

	for i, tc := range v.SessionBatches {
		t.Run(tc.Name, func(t *testing.T) {
			deadline, err := strconv.ParseInt(tc.Deadline, 10, 64)
			require.NoError(t, err)

			var call DepositWalletCall
			if i == 0 {
				call = AuthorizeSessionSignerCall(tc.Wallet, tc.SessionSigner, time.Unix(tc.ValidUntil, 0))
			} else {
				call = RevokeSessionSignerCall(tc.Wallet, tc.SessionSigner)
			}
			assert.Equal(t, tc.Calls, []DepositWalletCall{call})

			b := Batch{Wallet: tc.Wallet, Nonce: tc.Nonce, Deadline: time.Unix(deadline, 0), Calls: []DepositWalletCall{call}}

			digest, err := b.Digest(Polygon)
			require.NoError(t, err)
			assert.Equal(t, tc.Digest, hexutil.Encode(digest))

			// Signed the same, so the owner's browser wallet signs what the server checks.
			sig, err := crypto.Sign(digest, key)
			require.NoError(t, err)
			sig[64] += 27
			assert.Equal(t, tc.Signature, hexutil.Encode(sig))

			signer, err := b.Signer(Polygon, tc.Signature)
			require.NoError(t, err)
			assert.Equal(t, tc.Owner, signer)
		})
	}
}

func TestBatchTypedDataJSON(t *testing.T) {
	tc := loadSessionVectors(t).SessionBatches[0]
	deadline, _ := strconv.ParseInt(tc.Deadline, 10, 64)
	b := Batch{Wallet: tc.Wallet, Nonce: tc.Nonce, Deadline: time.Unix(deadline, 0), Calls: tc.Calls}

	raw, err := b.TypedDataJSON(Polygon)
	require.NoError(t, err)

	assert.JSONEq(t, `{
		"types": {
			"EIP712Domain": [{"name":"name","type":"string"},{"name":"version","type":"string"},
				{"name":"chainId","type":"uint256"},{"name":"verifyingContract","type":"address"}],
			"Batch": [{"name":"wallet","type":"address"},{"name":"nonce","type":"uint256"},
				{"name":"deadline","type":"uint256"},{"name":"calls","type":"Call[]"}],
			"Call": [{"name":"target","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"}]
		},
		"primaryType": "Batch",
		"domain": {"name":"DepositWallet","version":"1","chainId":137,"verifyingContract":"`+tc.Wallet+`"},
		"message": {"wallet":"`+tc.Wallet+`","nonce":"7","deadline":"1760875800",
			"calls":[{"target":"`+tc.Calls[0].Target+`","value":"0","data":"`+tc.Calls[0].Data+`"}]}
	}`, string(raw))

	// What the browser signs is what the server checks: the JSON read back hashes the same.
	var td apitypes.TypedData
	require.NoError(t, json.Unmarshal(raw, &td))
	digest, _, err := apitypes.TypedDataAndHash(td)
	require.NoError(t, err)
	assert.Equal(t, tc.Digest, hexutil.Encode(digest))
}

func TestBatchSigner(t *testing.T) {
	tc := loadSessionVectors(t).SessionBatches[1]
	deadline, _ := strconv.ParseInt(tc.Deadline, 10, 64)
	b := Batch{Wallet: tc.Wallet, Nonce: tc.Nonce, Deadline: time.Unix(deadline, 0), Calls: tc.Calls}

	t.Run("v of 0 or 1 too", func(t *testing.T) {
		sig := hexutil.MustDecode(tc.Signature)
		sig[64] -= 27

		signer, err := b.Signer(Polygon, hexutil.Encode(sig))
		require.NoError(t, err)
		assert.Equal(t, tc.Owner, signer)
	})

	t.Run("another batch's signature is someone else's", func(t *testing.T) {
		other := b
		other.Nonce = "8"

		signer, err := other.Signer(Polygon, tc.Signature)
		require.NoError(t, err)
		assert.NotEqual(t, tc.Owner, signer)
	})

	t.Run("not a signature", func(t *testing.T) {
		for _, sig := range []string{"", "0x1234", "nope", tc.Signature + "00"} {
			_, err := b.Signer(Polygon, sig)
			require.Error(t, err, sig)
		}
	})

	t.Run("the signature is unchanged", func(t *testing.T) {
		sig := tc.Signature
		_, err := b.Signer(Polygon, sig)
		require.NoError(t, err)
		assert.Equal(t, tc.Signature, sig)
	})
}

func TestBuilderHeadersMatchTheOfficialClient(t *testing.T) {
	for _, tc := range loadSessionVectors(t).BuilderHmac {
		auth := BuilderCredentials{Key: "builder-key", Secret: tc.Secret, Passphrase: "builder-pass"}

		h, err := idempotent{auth: auth, key: "retry-1"}.headers(time.Unix(tc.Timestamp, 0), tc.Method, tc.Path, []byte(tc.Body))
		require.NoError(t, err)

		assert.Equal(t, tc.Signature, h.Get("POLY_BUILDER_SIGNATURE"))
		assert.Equal(t, "builder-key", h.Get("POLY_BUILDER_API_KEY"))
		assert.Equal(t, "builder-pass", h.Get("POLY_BUILDER_PASSPHRASE"))
		assert.Equal(t, strconv.FormatInt(tc.Timestamp, 10), h.Get("POLY_BUILDER_TIMESTAMP"))
		assert.Equal(t, "retry-1", h.Get("Idempotency-Key"))
	}
}

func TestNewSessionKey(t *testing.T) {
	key, address, err := NewSessionKey()
	require.NoError(t, err)

	signer, err := NewOrderSigner(key, true)
	require.NoError(t, err)
	assert.Equal(t, address, signer.Address())

	again, _, err := NewSessionKey()
	require.NoError(t, err)
	assert.NotEqual(t, key, again)
}

func newRelayerClient(t *testing.T, base string) *DefaultRelayerClient {
	t.Helper()

	c, err := NewRelayerClient(observability.NoopUtility(), HTTPConfig{BaseURL: base})
	require.NoError(t, err)

	c.core.now = ticking()

	return c
}

var testBuilder = BuilderCredentials{Key: "builder-key", Secret: vectorSecret, Passphrase: "builder-pass"}

func TestRelayerRequests(t *testing.T) {
	req := SessionSignerRequest{
		Wallet: "0xBc0FF067B7740eFf76C1ca93C875Ba6B890d6B50", SessionSigner: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8",
		Scopes: []string{SessionScopeCLOB}, ValidUntil: 1776409200, Nonce: "7", Deadline: 1760875800,
		Signature: "0xsig", IdempotencyKey: "retry-1",
	}

	t.Run("nonce", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusOK}, `{"address":"0xowner","nonce":"7"}`)

		got, err := newRelayerClient(t, base).Nonce(t.Context(), testBuilder, "0xOwner")
		require.NoError(t, err)
		assert.Equal(t, "7", got)

		r := seen()[0]
		assert.Equal(t, "GET /v1/account/transactions/params", r.method+" "+r.path)
		assert.Equal(t, "address=0xOwner&type=WALLET", r.query)
		assert.Equal(t, "builder-key", r.header.Get("POLY_BUILDER_API_KEY"))
	})

	t.Run("authorise", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusOK}, `{"status":"SUBMITTED","transactionHash":null,"transactionId":"tx-1"}`)

		got, err := newRelayerClient(t, base).AuthorizeSessionSigner(t.Context(), testBuilder, req)
		require.NoError(t, err)
		assert.Equal(t, RelayerOperation{Status: "SUBMITTED", TransactionID: "tx-1"}, got)

		r := seen()[0]
		assert.Equal(t, "POST /v1/session-signers/authorizations", r.method+" "+r.path)
		assert.JSONEq(t, `{
			"walletAddress": "0xbc0ff067b7740eff76c1ca93c875ba6b890d6b50",
			"sessionSignerAddress": "0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
			"scopes": ["CLOB"], "validUntil": "1776409200",
			"nonce": "7", "deadline": "1760875800", "signature": "0xsig"
		}`, string(r.body))
		assert.Equal(t, "retry-1", r.header.Get("Idempotency-Key"))

		// Signed over the exact body sent.
		ts, err := strconv.ParseInt(r.header.Get("POLY_BUILDER_TIMESTAMP"), 10, 64)
		require.NoError(t, err)
		want, err := L2Credentials{Secret: vectorSecret}.Sign(ts, http.MethodPost, r.path, r.body)
		require.NoError(t, err)
		assert.Equal(t, want, r.header.Get("POLY_BUILDER_SIGNATURE"))
	})

	t.Run("revoke", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusOK}, `{"status":"PENDING","transactionId":"tx-2"}`)

		got, err := newRelayerClient(t, base).RevokeSessionSigner(t.Context(), testBuilder, req)
		require.NoError(t, err)
		assert.Equal(t, "tx-2", got.TransactionID)

		r := seen()[0]
		assert.Equal(t, "POST /v1/session-signers/revocations", r.method+" "+r.path)
		assert.JSONEq(t, `{
			"walletAddress": "0xbc0ff067b7740eff76c1ca93c875ba6b890d6b50",
			"sessionSignerAddress": "0x70997970c51812dc3a010c7d01b50e0d17dc79c8",
			"nonce": "7", "deadline": "1760875800", "signature": "0xsig"
		}`, string(r.body))
	})

	t.Run("a retry is the same request", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusServiceUnavailable, http.StatusOK}, `{"status":"SUBMITTED","transactionId":"tx-1"}`)

		_, err := newRelayerClient(t, base).AuthorizeSessionSigner(t.Context(), testBuilder, req)
		require.NoError(t, err)

		reqs := seen()
		require.Len(t, reqs, 2)
		assert.Equal(t, reqs[0].body, reqs[1].body)
		assert.Equal(t, "retry-1", reqs[1].header.Get("Idempotency-Key"))
	})

	for _, status := range []string{"FAILED", "SUPERSEDED", "REPAIR_REQUIRED"} {
		t.Run("refused: "+status, func(t *testing.T) {
			base, _ := l2Server(t, []int{http.StatusOK}, `{"status":"`+status+`","transactionId":"tx-1"}`)

			got, err := newRelayerClient(t, base).AuthorizeSessionSigner(t.Context(), testBuilder, req)
			require.ErrorIs(t, err, ErrRelayerRefused)
			assert.Equal(t, status, got.Status)
		})
	}

	t.Run("a refused builder key", func(t *testing.T) {
		base, _ := l2Server(t, []int{http.StatusUnauthorized}, `{"error":"invalid builder credentials"}`)

		_, err := newRelayerClient(t, base).AuthorizeSessionSigner(t.Context(), testBuilder, req)
		require.ErrorIs(t, err, ErrUnauthorized)
	})
}

func TestRelayerTransaction(t *testing.T) {
	tests := []struct {
		response     string
		want         RelayerTransaction
		done, failed bool
	}{
		{`{"state":"STATE_NEW","transaction_id":"tx-1","transaction_hash":"","error_msg":null}`, RelayerTransaction{State: "STATE_NEW"}, false, false},
		{`{"state":"STATE_CONFIRMED","transaction_id":"tx-1","transaction_hash":"0xhash"}`, RelayerTransaction{State: TransactionConfirmed, Hash: "0xhash"}, true, false},
		{`{"state":"STATE_FAILED","transaction_id":"tx-1","error_msg":"reverted"}`, RelayerTransaction{State: TransactionFailed, Error: "reverted"}, false, true},
		{`{"state":"STATE_INVALID","transaction_id":"tx-1"}`, RelayerTransaction{State: TransactionInvalid}, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.want.State, func(t *testing.T) {
			base, seen := l2Server(t, []int{http.StatusOK}, tt.response)

			got, err := newRelayerClient(t, base).Transaction(t.Context(), testBuilder, "tx/1")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.done, got.Done())
			assert.Equal(t, tt.failed, got.Failed())

			assert.Equal(t, "/v1/account/transactions/tx/1", seen()[0].path)
		})
	}
}
