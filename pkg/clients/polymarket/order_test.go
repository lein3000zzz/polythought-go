package polymarket

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vectors are what @polymarket/client gives for the same orders and amounts, made by
// `make order-vectors` from the commit it pins (D151).
type vectors struct {
	SDK  struct{ Commit string }
	Keys struct{ Owner, Session string }

	Signatures []struct {
		Name     string
		ChainID  int64
		Exchange struct{ Address, Version string }
		Session  bool
		Order    struct {
			Salt, Maker, Signer, TokenID, MakerAmount, TakerAmount, Side string
			SignatureType                                                uint8
			Timestamp, Metadata, Builder                                 string
		}
		Digest, Signature string
	}

	LimitAmounts []struct {
		Tick, Side, Price, Size, Maker, Taker string
	}
	MarketAmounts []struct {
		Tick, Side, Amount, Price, Maker, Taker string
		Protect                                 bool
	}
	PositionIDs []struct {
		TokenID string
		V2      bool
	}
}

func loadVectors(t *testing.T) vectors {
	t.Helper()

	var v vectors
	require.NoError(t, json.Unmarshal(fixture(t, "order_vectors.json"), &v))
	require.NotEmpty(t, v.Signatures)

	return v
}

func u64(t *testing.T, s string) uint64 {
	t.Helper()

	n, err := strconv.ParseUint(s, 10, 64)
	require.NoError(t, err)

	return n
}

func b32(t *testing.T, s string) [32]byte {
	t.Helper()

	var out [32]byte
	b, err := hexutil.Decode(s)
	require.NoError(t, err)
	copy(out[:], b)

	return out
}

func TestOrderSigningMatchesTheOfficialClient(t *testing.T) {
	v := loadVectors(t)

	for _, tc := range v.Signatures {
		t.Run(tc.Name, func(t *testing.T) {
			o := Order{
				Salt: u64(t, tc.Order.Salt), Maker: tc.Order.Maker, Signer: tc.Order.Signer, TokenID: tc.Order.TokenID,
				MakerAmount: u64(t, tc.Order.MakerAmount), TakerAmount: u64(t, tc.Order.TakerAmount),
				Side: Side(tc.Order.Side), SignatureType: SignatureType(tc.Order.SignatureType),
				Timestamp: u64(t, tc.Order.Timestamp), Metadata: b32(t, tc.Order.Metadata), Builder: b32(t, tc.Order.Builder),
			}
			ex := Exchange{Address: tc.Exchange.Address, Version: tc.Exchange.Version}

			digest, err := o.Digest(tc.ChainID, ex)
			require.NoError(t, err)
			assert.Equal(t, tc.Digest, hexutil.Encode(digest))

			key := v.Keys.Owner
			if tc.Session {
				key = v.Keys.Session
			}

			signer, err := NewOrderSigner(key, tc.Session)
			require.NoError(t, err)

			sig, err := signer.Sign(tc.ChainID, ex, o)
			require.NoError(t, err)

			// Byte for byte: the official client writes a session signer's address into its
			// envelope's hex as checksummed, mixed case, the same bytes.
			want, got := hexutil.MustDecode(tc.Signature), hexutil.MustDecode(sig)
			assert.Equal(t, want, got)
		})
	}
}

// Polymarket's py-sdk golden digests (tests/unit/test_order_typed_data_golden.py): one
// address making and signing, on a test exchange.
func TestOrderDigestsMatchPublishedOnes(t *testing.T) {
	ex := Exchange{Address: "0x4bfb41d5b3570defd03c39a9a4d8de6bd8b8982e", Version: "2"}
	o := Order{
		Salt: 1, Maker: "0x57ffbc34de23124faeb8387fcd689d314e57accd", Signer: "0x57ffbc34de23124faeb8387fcd689d314e57accd",
		TokenID: "1", MakerAmount: 1_000_000, TakerAmount: 500_000, Side: SideBuy,
	}

	for typ, want := range map[SignatureType]string{
		SignatureEOA:           "0xa511aa6ff25ecfc36227717b080802396f686a8cf012fe38b4c30153b91be7a5",
		SignatureProxy:         "0x6c1e2461f1aceb08efa25fbd99c6087f86ee7e67dab570dc7a0aad194bd9c653",
		SignatureDepositWallet: "0x1b9566eedd9589a73275df23a3a9d9e2e9897e76d31cd46d436f1b824d161b33",
	} {
		o.SignatureType = typ

		digest, err := o.Digest(Polygon, ex)
		require.NoError(t, err)
		assert.Equal(t, want, hexutil.Encode(digest), "signature type %d", typ)
	}
}

func TestOrderAmountsMatchTheOfficialClient(t *testing.T) {
	v := loadVectors(t)
	d := decimal.RequireFromString

	for _, tc := range v.LimitAmounts {
		a, err := LimitAmounts(Side(tc.Side), d(tc.Price), d(tc.Size), d(tc.Tick))
		require.NoError(t, err, "%+v", tc)
		assert.Equal(t, Amounts{Maker: u64(t, tc.Maker), Taker: u64(t, tc.Taker)}, a, "limit %+v", tc)
	}

	for _, tc := range v.MarketAmounts {
		a, err := MarketAmounts(Side(tc.Side), d(tc.Amount), d(tc.Price), d(tc.Tick), tc.Protect)
		require.NoError(t, err, "%+v", tc)
		assert.Equal(t, Amounts{Maker: u64(t, tc.Maker), Taker: u64(t, tc.Taker)}, a, "market %+v", tc)
	}
}

func TestOffTheGridIsRefused(t *testing.T) {
	d := decimal.RequireFromString

	for name, err := range map[string]error{
		"a price off the tick":        func() error { _, err := LimitAmounts(SideBuy, d("0.555"), d("10"), d("0.01")); return err }(),
		"a price of 1":                func() error { _, err := LimitAmounts(SideBuy, d("1"), d("10"), d("0.01")); return err }(),
		"a price under the tick":      func() error { _, err := LimitAmounts(SideSell, d("0.001"), d("10"), d("0.01")); return err }(),
		"a size of three decimals":    func() error { _, err := LimitAmounts(SideBuy, d("0.55"), d("10.001"), d("0.01")); return err }(),
		"no size":                     func() error { _, err := LimitAmounts(SideBuy, d("0.55"), d("0"), d("0.01")); return err }(),
		"dollars of three decimals":   func() error { _, err := MarketAmounts(SideBuy, d("10.005"), d("0.55"), d("0.01"), false); return err }(),
		"a tick the market never has": func() error { _, err := LimitAmounts(SideBuy, d("0.5"), d("10"), d("0.05")); return err }(),
	} {
		assert.ErrorIs(t, err, ErrOffGrid, name)
	}
}

func TestExchangeFor(t *testing.T) {
	for _, tc := range loadVectors(t).PositionIDs {
		ex, err := ExchangeFor(tc.TokenID, false)
		require.NoError(t, err)
		assert.Equal(t, tc.V2, ex == ExchangeV3, tc.TokenID)
	}

	ex, err := ExchangeFor("71321045679252212594626385532706912750332728571942532289631379312455583992563", true)
	require.NoError(t, err)
	assert.Equal(t, NegRiskExchange, ex)

	_, err = ExchangeFor("0xabc", false)
	require.Error(t, err)
}

func TestSigningRefusesWhatWouldNotBeValid(t *testing.T) {
	v := loadVectors(t)
	owner, err := NewOrderSigner(v.Keys.Owner, false)
	require.NoError(t, err)

	base := Order{
		Salt: 1, Maker: owner.Address(), Signer: owner.Address(), TokenID: "1234",
		MakerAmount: 5_500_000, TakerAmount: 10_000_000, Side: SideBuy, SignatureType: SignatureEOA,
	}

	for name, o := range map[string]Order{
		"a salt past 2^53":           func() Order { o := base; o.Salt = MaxSalt + 1; return o }(),
		"another signer":             func() Order { o := base; o.Signer = "0x2222222222222222222222222222222222222222"; return o }(),
		"a proxy signing for itself": func() Order { o := base; o.SignatureType = SignatureProxy; return o }(),
		"a Deposit Wallet signed by another": func() Order {
			o := base
			o.SignatureType, o.Maker = SignatureDepositWallet, "0x1111111111111111111111111111111111111111"

			return o
		}(),
		"no side":     func() Order { o := base; o.Side = ""; return o }(),
		"no token ID": func() Order { o := base; o.TokenID = "yes"; return o }(),
	} {
		_, err := owner.Sign(Polygon, CTFExchange, o)
		assert.Error(t, err, name)
	}

	session, err := NewOrderSigner(v.Keys.Session, true)
	require.NoError(t, err)

	_, err = session.Sign(Polygon, CTFExchange, base)
	assert.ErrorContains(t, err, "a session key signs for a Deposit Wallet only")

	salt, err := NewSalt()
	require.NoError(t, err)
	assert.LessOrEqual(t, salt, uint64(MaxSalt))
}

// The order as POST /order takes it, as @polymarket/client's createSendOrderPayload
// writes it (packages/client/src/actions/orders/post.ts).
func TestSignedOrderJSON(t *testing.T) {
	o := SignedOrder{
		Order: Order{
			Salt: 479249096354, Maker: "0x1111111111111111111111111111111111111111", Signer: "0x1111111111111111111111111111111111111111",
			TokenID: "1234", MakerAmount: 100_000_000, TakerAmount: 50_000_000, Side: SideSell,
			SignatureType: SignatureDepositWallet, Timestamp: 1710000000000,
		},
		Signature: "0xabc", Expiration: 1710000300,
	}

	b, err := json.Marshal(o)
	require.NoError(t, err)

	zero := "0x0000000000000000000000000000000000000000000000000000000000000000"
	assert.JSONEq(t, `{
		"builder": "`+zero+`", "expiration": "1710000300", "maker": "0x1111111111111111111111111111111111111111",
		"makerAmount": "100000000", "metadata": "`+zero+`", "salt": 479249096354, "side": "SELL", "signature": "0xabc",
		"signatureType": 3, "signer": "0x1111111111111111111111111111111111111111", "takerAmount": "50000000",
		"timestamp": "1710000000000", "tokenId": "1234"
	}`, string(b))
}
