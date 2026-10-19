package polymarket

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type l1Vectors struct {
	Keys     struct{ Owner string }
	ClobAuth []struct {
		Address   string
		ChainID   int64
		Timestamp int64
		Nonce     uint64
		Signature string
	}
	Wallets []struct {
		Signer, Proxy, Safe, DepositBeacon, DepositUups string
	}
}

func loadL1Vectors(t *testing.T) l1Vectors {
	t.Helper()

	var v l1Vectors
	require.NoError(t, json.Unmarshal(fixture(t, "order_vectors.json"), &v))
	require.NotEmpty(t, v.ClobAuth)
	require.NotEmpty(t, v.Wallets)

	return v
}

func TestClobAuthMatchesTheOfficialClient(t *testing.T) {
	v := loadL1Vectors(t)

	signer, err := NewOrderSigner(v.Keys.Owner, false)
	require.NoError(t, err)

	for _, tc := range v.ClobAuth {
		assert.Equal(t, tc.Address, signer.Address())

		sig, err := signer.SignClobAuth(tc.ChainID, tc.Timestamp, tc.Nonce)
		require.NoError(t, err)
		assert.Equal(t, hexutil.MustDecode(tc.Signature), hexutil.MustDecode(sig), "nonce %d", tc.Nonce)
	}
}

func TestWalletsMatchTheOfficialClient(t *testing.T) {
	for _, tc := range loadL1Vectors(t).Wallets {
		signer := common.HexToAddress(tc.Signer)
		w := Production.WalletsOf(signer)

		assert.Equal(t, common.HexToAddress(tc.Proxy), w.Proxy, "proxy of %s", tc.Signer)
		assert.Equal(t, common.HexToAddress(tc.Safe), w.Safe, "safe of %s", tc.Signer)
		assert.Equal(t, common.HexToAddress(tc.DepositBeacon), w.DepositBeacon, "deposit wallet (beacon) of %s", tc.Signer)
		assert.Equal(t, common.HexToAddress(tc.DepositUups), w.DepositUUPS, "deposit wallet (UUPS) of %s", tc.Signer)

		for wallet, want := range map[common.Address]SignatureType{
			signer: SignatureEOA, w.Proxy: SignatureProxy, w.Safe: SignatureSafe,
			w.DepositBeacon: SignatureDepositWallet, w.DepositUUPS: SignatureDepositWallet,
		} {
			got, ok := Production.Classify(signer, wallet)
			require.True(t, ok)
			assert.Equal(t, want, got)
		}

		_, ok := Production.Classify(signer, common.HexToAddress("0x2222222222222222222222222222222222222222"))
		assert.False(t, ok, "a wallet it does not control")
	}
}

func TestCreateOrDeriveAPIKey(t *testing.T) {
	v := loadL1Vectors(t)
	signer, err := NewOrderSigner(v.Keys.Owner, false)
	require.NoError(t, err)

	creds := `{"apiKey":"key-1","secret":"c2VjcmV0","passphrase":"pass"}`

	t.Run("made the first time", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusOK}, creds)
		c := newL2Client(t, base, observability.NoopUtility())

		got, err := c.CreateOrDeriveAPIKey(t.Context(), L1Auth{Signer: signer, ChainID: Polygon})
		require.NoError(t, err)
		assert.Equal(t, L2Credentials{Address: signer.Address(), APIKey: "key-1", Secret: "c2VjcmV0", Passphrase: "pass"}, got)

		req := seen()[0]
		assert.Equal(t, "POST /auth/api-key", req.method+" "+req.path)
		assert.Equal(t, signer.Address(), req.header.Get("POLY_ADDRESS"))
		assert.Equal(t, "0", req.header.Get("POLY_NONCE"))

		// The signature is ClobAuth's at the request's timestamp.
		ts := req.header.Get("POLY_TIMESTAMP")
		var at int64
		require.NoError(t, json.Unmarshal([]byte(ts), &at))
		want, err := signer.SignClobAuth(Polygon, at, 0)
		require.NoError(t, err)
		assert.Equal(t, want, req.header.Get("POLY_SIGNATURE"))
	})

	t.Run("given again when the account has one", func(t *testing.T) {
		base, seen := l2Server(t, []int{http.StatusBadRequest, http.StatusOK}, creds)
		c := newL2Client(t, base, observability.NoopUtility())

		got, err := c.CreateOrDeriveAPIKey(t.Context(), L1Auth{Signer: signer, ChainID: Polygon})
		require.NoError(t, err)
		assert.Equal(t, "key-1", got.APIKey)

		reqs := seen()
		require.Len(t, reqs, 2)
		assert.Equal(t, "GET /auth/derive-api-key", reqs[1].method+" "+reqs[1].path)
	})

	t.Run("a refused signature", func(t *testing.T) {
		base, _ := l2Server(t, []int{http.StatusUnauthorized}, `{"error":"Invalid L1 Request headers"}`)
		c := newL2Client(t, base, observability.NoopUtility())

		_, err := c.CreateOrDeriveAPIKey(t.Context(), L1Auth{Signer: signer, ChainID: Polygon})
		require.ErrorIs(t, err, ErrUnauthorized)
	})
}
