package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

// The official client's signatures (make order-vectors): the same orders signed here must
// come out the same, byte for byte.
type liveVectors struct {
	Keys       struct{ Owner, Session string }
	Signatures []struct {
		Name      string
		Signature string
	}
}

func loadLiveVectors(t *testing.T) liveVectors {
	t.Helper()

	b, err := os.ReadFile("../../../pkg/clients/polymarket/testdata/order_vectors.json")
	require.NoError(t, err)

	var v liveVectors
	require.NoError(t, json.Unmarshal(b, &v))

	return v
}

func (v liveVectors) signature(t *testing.T, name string) string {
	t.Helper()

	for _, s := range v.Signatures {
		if s.Name == name {
			return s.Signature
		}
	}

	t.Fatalf("no vector %q", name)

	return ""
}

const (
	vectorToken  = domain.TokenID("71321045679252212594626385532706912750332728571942532289631379312455583992563")
	vectorSalt   = 479249096354
	ownerAddress = domain.WalletAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	safeAddress  = domain.WalletAddress("0x3333333333333333333333333333333333333333")
	liveDeposit  = domain.WalletAddress("0x1111111111111111111111111111111111111111")
	liveSession  = domain.WalletAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// exchange is one with the vectors' salt and time.
func exchange(t *testing.T) (*Exchange, *polymarketmocks.MockClobTradingClient, *polymarketmocks.MockClobAccountClient) {
	clob := polymarketmocks.NewMockClobTradingClient(t)
	account := polymarketmocks.NewMockClobAccountClient(t)

	e := NewExchange(clob, account)
	e.now = func() time.Time { return time.UnixMilli(1760875200000) }
	e.salt = func() (uint64, error) { return vectorSalt, nil }

	return e, clob, account
}

func TestLiveOrdersAreSignedAsTheOfficialClientSignsThem(t *testing.T) {
	v := loadLiveVectors(t)

	tests := []struct {
		name, vector string
		key          ports.OrderKey
		order        ports.LiveOrder
		exchange     pm.Exchange
	}{
		{
			name: "an EOA's buy of shares", vector: "an EOA, a buy, on ctf",
			key: ports.OrderKey{
				Wallet: domain.Wallet{Address: ownerAddress}, Key: v.Keys.Owner,
				Credentials: domain.PolymarketCredentials{Signer: ownerAddress, SignatureType: domain.SignatureEOA},
			},
			order:    ports.LiveOrder{TokenID: vectorToken, Side: domain.SideBuy, Type: domain.OrderFAK, Price: d("0.55"), Shares: d("10"), TickSize: d("0.01")},
			exchange: pm.CTFExchange,
		},
		{
			name: "the same as a buy of dollars", vector: "an EOA, a buy, on ctf",
			key: ports.OrderKey{
				Wallet: domain.Wallet{Address: ownerAddress}, Key: v.Keys.Owner,
				Credentials: domain.PolymarketCredentials{Signer: ownerAddress, SignatureType: domain.SignatureEOA},
			},
			order:    ports.LiveOrder{TokenID: vectorToken, Side: domain.SideBuy, Type: domain.OrderFOK, Price: d("0.55"), Dollars: d("5.5"), TickSize: d("0.01")},
			exchange: pm.CTFExchange,
		},
		{
			name: "a Safe's sell on a neg risk market", vector: "a Safe, a sell, on negRisk",
			key: ports.OrderKey{
				Wallet: domain.Wallet{Address: safeAddress}, Key: v.Keys.Owner,
				Credentials: domain.PolymarketCredentials{Signer: ownerAddress, SignatureType: domain.SignatureGnosisSafe},
			},
			order:    ports.LiveOrder{TokenID: vectorToken, Side: domain.SideSell, Type: domain.OrderFAK, Price: d("0.55"), Shares: d("10"), TickSize: d("0.01"), NegRisk: true},
			exchange: pm.NegRiskExchange,
		},
		{
			name: "a Deposit Wallet's buy by its session key", vector: "a session key for a Deposit Wallet, a buy, on ctf",
			key: ports.OrderKey{
				Wallet: domain.Wallet{Address: liveDeposit}, Key: v.Keys.Session,
				Credentials: domain.PolymarketCredentials{Signer: liveSession, SignatureType: domain.SignatureDepositWallet, SessionKey: true},
			},
			order:    ports.LiveOrder{TokenID: vectorToken, Side: domain.SideBuy, Type: domain.OrderFAK, Price: d("0.55"), Shares: d("10"), TickSize: d("0.01")},
			exchange: pm.CTFExchange,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, clob, _ := exchange(t)

			var sent pm.OrderPost
			clob.EXPECT().PostOrder(mock.Anything, toL2(tt.key.Credentials), mock.Anything).
				RunAndReturn(func(_ context.Context, _ pm.L2Credentials, p pm.OrderPost) (pm.PostedOrder, error) {
					sent = p
					id, err := p.ID()

					return pm.PostedOrder{ID: id, Status: pm.PostMatched, TradeIDs: []string{"trade-1"}}, err
				})

			placed, err := e.Place(context.Background(), tt.key, tt.order)
			require.NoError(t, err)

			// Byte for byte: the official client writes a session signer's address into its
			// envelope's hex checksummed, the same bytes.
			assert.Equal(t, common.FromHex(v.signature(t, tt.vector)), common.FromHex(sent.Order.Signature))
			assert.Equal(t, tt.exchange, sent.Exchange)
			assert.Equal(t, pm.OrderType(tt.order.Type), sent.Type)
			assert.Equal(t, ports.LiveMatched, placed.State)
			assert.Equal(t, []string{"trade-1"}, placed.TradeIDs)

			id, err := sent.ID()
			require.NoError(t, err)
			assert.Equal(t, id, placed.ID)
		})
	}
}

func TestLiveOrderWithABuilderCode(t *testing.T) {
	e, clob, _ := exchange(t)
	key := ports.OrderKey{
		Wallet: domain.Wallet{Address: ownerAddress}, Key: loadLiveVectors(t).Keys.Owner,
		Credentials: domain.PolymarketCredentials{Signer: ownerAddress, SignatureType: domain.SignatureEOA},
	}
	order := ports.LiveOrder{TokenID: vectorToken, Side: domain.SideBuy, Type: domain.OrderFAK, Price: d("0.55"), Shares: d("10"), TickSize: d("0.01")}

	var sent pm.OrderPost
	clob.EXPECT().PostOrder(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ pm.L2Credentials, p pm.OrderPost) (pm.PostedOrder, error) {
			sent = p
			return pm.PostedOrder{ID: "0x1", Status: pm.PostMatched}, nil
		})

	order.Builder = "0x00000000000000000000000000000000000000000000000000000000000000b1"
	_, err := e.Place(context.Background(), key, order)
	require.NoError(t, err)
	assert.Equal(t, byte(0xb1), sent.Order.Builder[31])

	order.Builder = "b1"
	_, err = e.Place(context.Background(), key, order)
	require.ErrorContains(t, err, "not 32 bytes")
}

func TestLivePlacements(t *testing.T) {
	key := ports.OrderKey{
		Wallet: domain.Wallet{Address: ownerAddress}, Key: loadLiveVectors(t).Keys.Owner,
		Credentials: domain.PolymarketCredentials{Signer: ownerAddress, SignatureType: domain.SignatureEOA},
	}
	order := ports.LiveOrder{TokenID: vectorToken, Side: domain.SideBuy, Type: domain.OrderFAK, Price: d("0.55"), Shares: d("10"), TickSize: d("0.01")}

	tests := []struct {
		name   string
		posted pm.PostedOrder
		err    error
		check  func(t *testing.T, p ports.LivePlacement, err error)
	}{
		{
			name: "delayed", posted: pm.PostedOrder{ID: "0x1", Status: pm.PostDelayed},
			check: func(t *testing.T, p ports.LivePlacement, err error) {
				require.NoError(t, err)
				assert.Equal(t, ports.LiveDelayed, p.State)
			},
		},
		{
			name: "refused, in the owner's words and Polymarket's",
			err:  &pm.OrderRefusedError{Code: pm.RefusedBalance, Message: "not enough balance / allowance", StatusCode: 400},
			check: func(t *testing.T, p ports.LivePlacement, err error) {
				require.NoError(t, err)
				assert.Equal(t, ports.LiveRefused, p.State)
				assert.Equal(t, "not enough pUSD or shares in the wallet, or its trading approvals are missing (Polymarket: not enough balance / allowance)", p.Reason)
				assert.NotEmpty(t, p.ID)
			},
		},
		{
			name: "a FAK with nothing to match is no refusal: it did not fill",
			err:  &pm.OrderRefusedError{Code: pm.RefusedFAKNotFilled, Message: "no orders found to match with FAK order."},
			check: func(t *testing.T, p ports.LivePlacement, err error) {
				require.NoError(t, err)
				assert.Equal(t, ports.LiveDone, p.State)
				assert.Equal(t, "the book had nothing to match within its price", p.Reason)
			},
		},
		{
			name: "in doubt", err: &pm.OrderUncertainError{ID: "0x1", Err: errors.New("no answer")},
			check: func(t *testing.T, p ports.LivePlacement, err error) {
				require.NoError(t, err)
				assert.Equal(t, ports.LiveUnknown, p.State)
			},
		},
		{
			name: "credentials refused", err: pm.ErrUnauthorized,
			check: func(t *testing.T, p ports.LivePlacement, err error) {
				require.NoError(t, err)
				assert.Equal(t, ports.LiveRefused, p.State)
				assert.Contains(t, p.Reason, "API credentials")
			},
		},
		{
			name: "not sent at all", err: errors.New("dial tcp: refused"),
			check: func(t *testing.T, _ ports.LivePlacement, err error) {
				require.ErrorIs(t, err, domain.ErrUnavailable)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, clob, _ := exchange(t)
			clob.EXPECT().PostOrder(mock.Anything, mock.Anything, mock.Anything).Return(tt.posted, tt.err)

			p, err := e.Place(context.Background(), key, order)
			tt.check(t, p, err)
		})
	}

	t.Run("off the market's grid: not sent", func(t *testing.T) {
		e, _, _ := exchange(t)

		off := order
		off.Price = d("0.555")

		_, err := e.Place(context.Background(), key, off)
		require.ErrorIs(t, err, pm.ErrOffGrid)
	})
}

func TestLiveOrderState(t *testing.T) {
	key := ports.OrderKey{Credentials: domain.PolymarketCredentials{Signer: ownerAddress}}

	for status, want := range map[string]ports.LiveState{
		"LIVE": ports.LiveResting, "MATCHED": ports.LiveMatched, "DELAYED": ports.LiveDelayed,
		"UNMATCHED": ports.LiveResting, "CANCELED": ports.LiveDone, "CANCELED_MARKET_RESOLVED": ports.LiveDone,
	} {
		e, clob, _ := exchange(t)
		clob.EXPECT().Order(mock.Anything, mock.Anything, "0x1").
			Return(pm.OpenOrder{ID: "0x1", Status: status, SizeMatched: d("4"), AssociateTrades: []string{"trade-1"}}, nil)

		got, err := e.Order(context.Background(), key, "0x1")
		require.NoError(t, err)
		assert.Equal(t, want, got.State, status)
		assert.Equal(t, "4", got.Matched.String())
	}

	e, clob, _ := exchange(t)
	clob.EXPECT().Order(mock.Anything, mock.Anything, "0x2").Return(pm.OpenOrder{}, pm.ErrNotFound)

	got, err := e.Order(context.Background(), key, "0x2")
	require.NoError(t, err)
	assert.Equal(t, ports.LiveNotFound, got.State)
}

func TestLiveTrades(t *testing.T) {
	key := ports.OrderKey{Wallet: domain.Wallet{Address: liveDeposit}, Credentials: domain.PolymarketCredentials{Signer: liveSession}}

	trades := map[string]pm.ClobTrade{
		// Our buy took a sell of Yes at 0.54 and, minting, a buy of No at 0.45: 0.55 here.
		"taker": {ID: "taker", TraderSide: pm.TraderTaker, Status: pm.TradeMined, Size: d("10"), Price: d("0.55"),
			MakerOrders: []pm.MakerOrder{
				{AssetID: string(vectorToken), MatchedAmount: d("6"), Price: d("0.54"), Side: pm.SideSell},
				{AssetID: "other", MatchedAmount: d("4"), Price: d("0.45"), Side: pm.SideBuy},
			}},
		"bare": {ID: "bare", TraderSide: pm.TraderTaker, Status: pm.TradeConfirmed, Size: d("3"), Price: d("0.5")},
		"maker": {ID: "maker", TraderSide: pm.TraderMaker, Status: pm.TradeFailed, MakerOrders: []pm.MakerOrder{
			{MakerAddress: "0xsomeone", MatchedAmount: d("9"), Price: d("0.5")},
			{MakerAddress: "0x1111111111111111111111111111111111111111", MatchedAmount: d("2"), Price: d("0.5")},
		}},
	}

	e, clob, _ := exchange(t)
	for id, tr := range trades {
		clob.EXPECT().Trades(mock.Anything, mock.Anything, pm.ClobTradesQuery{ID: id}).Return(pm.Page[pm.ClobTrade]{Items: []pm.ClobTrade{tr}}, nil)
	}
	clob.EXPECT().Trades(mock.Anything, mock.Anything, pm.ClobTradesQuery{ID: "later"}).Return(pm.Page[pm.ClobTrade]{}, nil)

	got, err := e.Trades(context.Background(), key, vectorToken, []string{"taker", "bare", "maker", "later"})
	require.NoError(t, err)
	require.Len(t, got, 3, "the one not listed yet left out")

	taker := got[0]
	assert.Equal(t, domain.FillMined, taker.Status)
	require.Len(t, taker.Fills, 2)
	assert.Equal(t, "0.54", taker.Fills[0].Price.String())
	assert.Equal(t, "0.55", taker.Fills[1].Price.String(), "the other outcome's price, from 1")
	assert.Equal(t, "2.2", taker.Fills[1].Dollars.String())
	assert.Equal(t, "taker", taker.Fills[1].TradeID)

	assert.Equal(t, domain.FillConfirmed, got[1].Status)
	require.Len(t, got[1].Fills, 1)
	assert.Equal(t, "1.5", got[1].Fills[0].Dollars.String())

	assert.Equal(t, domain.FillFailed, got[2].Status)
	require.Len(t, got[2].Fills, 1, "only the wallet's own maker order")
	assert.Equal(t, "2", got[2].Fills[0].Shares.String())
}

func TestLiveCash(t *testing.T) {
	e, _, account := exchange(t)
	key := ports.OrderKey{Credentials: domain.PolymarketCredentials{Signer: ownerAddress, SignatureType: domain.SignatureDepositWallet}}

	account.EXPECT().BalanceAllowance(mock.Anything, toL2(key.Credentials), pm.BalanceQuery{AssetType: pm.AssetCollateral, SignatureType: 3}).
		Return(pm.BalanceAllowance{Balance: d("12345678")}, nil)

	cash, err := e.Cash(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, "12.345678", cash.String())
}
