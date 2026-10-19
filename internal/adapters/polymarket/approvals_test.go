package polymarket

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

type approvalMocks struct {
	data    *polymarketmocks.MockDataClient
	rpc     *polymarketmocks.MockRPCClient
	relayer *polymarketmocks.MockRelayerClient
}

func newApprovals(t *testing.T, builder bool) (*Approvals, approvalMocks) {
	t.Helper()

	m := approvalMocks{
		data:    polymarketmocks.NewMockDataClient(t),
		rpc:     polymarketmocks.NewMockRPCClient(t),
		relayer: polymarketmocks.NewMockRelayerClient(t),
	}
	a := NewApprovals(m.data, m.rpc, m.relayer, func() (pm.BuilderCredentials, bool) { return testBuilder, builder })
	a.now = func() time.Time { return time.Unix(1760875200, 300_000_000) }

	return a, m
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, v))
}

func derived() pm.Wallets {
	return pm.Production.WalletsOf(common.HexToAddress(string(anvilAddress)))
}

func TestApprovalsStatus(t *testing.T) {
	a, m := newApprovals(t, false)

	var snap struct{ Data pm.ApprovalsSnapshot }
	readJSON(t, "../../../pkg/clients/polymarket/testdata/approvals.json", &snap)

	wallet := domain.WalletAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	m.data.EXPECT().Approvals(mock.Anything, string(wallet)).Return(snap.Data, nil)

	missing, checked, err := a.Status(t.Context(), wallet)
	require.NoError(t, err)
	assert.Len(t, missing, 17, "none granted")
	assert.Equal(t, snap.Data.CheckedAt.UTC(), checked)
	assert.Equal(t, domain.Approval{
		Token: "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB", Spender: "0xE111180000d2663C0091e4f400237545B87B996B",
		Standard: "ERC20", Name: "pUSD to the CTF Exchange",
	}, missing[0])
	assert.Equal(t, "V2 outcome tokens to auto-redeem", missing[16].Name)
}

func TestApprovalsMissingOnChain(t *testing.T) {
	a, m := newApprovals(t, false)
	wallet := domain.WalletAddress(derived().Safe.Hex())

	var checks []pm.Call

	m.rpc.EXPECT().Call(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, calls []pm.Call) ([][]byte, error) {
		checks = calls
		out := make([][]byte, len(calls))
		for i := range calls {
			out[i] = make([]byte, 32)
		}

		out[0] = bytes.Repeat([]byte{0xff}, 32) // all of it
		out[7][31] = 1                          // an operator approved

		return out, nil
	})

	missing, err := a.Missing(t.Context(), wallet)
	require.NoError(t, err)
	assert.Len(t, missing, 15)
	assert.Len(t, checks, 17)
	assert.Equal(t, "pUSD to the Neg Risk Exchange", missing[0].Name)
}

func TestApprovalsKind(t *testing.T) {
	a, _ := newApprovals(t, true)
	d := derived()

	for wallet, want := range map[common.Address]domain.SignatureType{
		common.HexToAddress(string(anvilAddress)): domain.SignatureEOA,
		d.Proxy: domain.SignaturePolyProxy, d.Safe: domain.SignatureGnosisSafe, d.DepositBeacon: domain.SignatureDepositWallet,
	} {
		got, ok := a.Kind(anvilAddress, domain.WalletAddress(wallet.Hex()))
		require.True(t, ok)
		assert.Equal(t, want, got)
	}

	_, ok := a.Kind(anvilAddress, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	assert.False(t, ok)
}

// TestApprovalsSubmitWhatTheOfficialClientDoes builds, signs and submits each smart
// wallet's setup through the adapter, and compares what reaches the Relayer with what
// @polymarket/client submitted for the same (the vectors).
func TestApprovalsSubmitWhatTheOfficialClientDoes(t *testing.T) {
	var v struct {
		Approvals []struct {
			Name, Owner, Wallet string
			WalletType          int
			Approved            []bool
			Submitted           json.RawMessage
		}
	}
	readJSON(t, "../../../pkg/clients/polymarket/testdata/order_vectors.json", &v)

	kinds := map[int]domain.SignatureType{1: domain.SignaturePolyProxy, 2: domain.SignatureGnosisSafe, 3: domain.SignatureDepositWallet}
	types := map[int]string{1: pm.RelayerProxy, 2: pm.RelayerSafe, 3: pm.RelayerDepositWallet}

	for _, tc := range v.Approvals {
		if string(tc.Submitted) == "null" {
			continue
		}

		t.Run(tc.Name, func(t *testing.T) {
			a, m := newApprovals(t, true)
			all := a.toDomain(pm.ProductionContracts.TradingApprovals())

			var missing []domain.Approval
			for i, x := range all {
				if !tc.Approved[i] {
					missing = append(missing, x)
				}
			}

			m.relayer.EXPECT().ExecuteParams(mock.Anything, testBuilder, tc.Owner, types[tc.WalletType]).
				Return(pm.ExecuteParams{Address: "0x1111111111111111111111111111111111111111", Nonce: "7"}, nil)
			if tc.WalletType == 1 {
				m.rpc.EXPECT().EstimateGas(mock.Anything, common.HexToAddress(tc.Owner), mock.Anything).Return(123456, nil)
			}

			b, err := a.Prepare(t.Context(), kinds[tc.WalletType], domain.WalletAddress(tc.Owner), domain.WalletAddress(tc.Wallet), missing)
			require.NoError(t, err)

			sig, err := a.Sign(b, anvilKey)
			require.NoError(t, err)

			signer, err := a.Signer(b, sig)
			require.NoError(t, err)
			assert.Equal(t, domain.WalletAddress(tc.Owner), signer)

			var sent pm.SubmitRequest
			m.relayer.EXPECT().Submit(mock.Anything, testBuilder, mock.Anything).
				RunAndReturn(func(_ context.Context, _ pm.BuilderCredentials, req pm.SubmitRequest) (pm.RelayerSubmission, error) {
					sent = req
					return pm.RelayerSubmission{State: "STATE_NEW", TransactionID: "tx-1"}, nil
				})

			id, err := a.Submit(t.Context(), b, sig)
			require.NoError(t, err)
			assert.Equal(t, "tx-1", id)

			got, err := json.Marshal(sent)
			require.NoError(t, err)
			assert.JSONEq(t, string(tc.Submitted), string(got))
		})
	}
}

func TestApprovalsSigningRequest(t *testing.T) {
	a, _ := newApprovals(t, true)
	all := a.toDomain(pm.ProductionContracts.TradingApprovals())
	d := derived()

	deposit := ports.ApprovalBatch{Kind: domain.SignatureDepositWallet, Owner: anvilAddress,
		Wallet: domain.WalletAddress(d.DepositBeacon.Hex()), Approvals: all, Nonce: "7", Deadline: time.Unix(1760875800, 0)}
	req, err := a.SigningRequest(deposit)
	require.NoError(t, err)
	assert.Contains(t, string(req.TypedData), `"primaryType":"Batch"`)
	assert.Empty(t, req.Message)

	safe := ports.ApprovalBatch{Kind: domain.SignatureGnosisSafe, Owner: anvilAddress,
		Wallet: domain.WalletAddress(d.Safe.Hex()), Approvals: all[:1], Nonce: "7"}
	req, err = a.SigningRequest(safe)
	require.NoError(t, err)
	assert.Regexp(t, `^0x[0-9a-f]{64}$`, req.Message, "the Safe transaction's hash, for personal_sign")
	assert.Nil(t, req.TypedData)

	proxy := ports.ApprovalBatch{Kind: domain.SignaturePolyProxy, Owner: anvilAddress,
		Wallet: domain.WalletAddress(d.Proxy.Hex()), Approvals: all, Nonce: "7"}
	_, err = a.SigningRequest(proxy)
	require.ErrorIs(t, err, domain.ErrNeedsImportedKey, "no browser signs for a proxy wallet")
}

func TestApprovalsGrantOnlyTheTradingSetup(t *testing.T) {
	a, _ := newApprovals(t, true)
	all := a.toDomain(pm.ProductionContracts.TradingApprovals())

	other := all[0]
	other.Spender = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"

	b := ports.ApprovalBatch{Kind: domain.SignatureGnosisSafe, Owner: anvilAddress,
		Wallet: domain.WalletAddress(derived().Safe.Hex()), Approvals: []domain.Approval{all[1], other}, Nonce: "7"}

	_, err := a.Sign(b, anvilKey)
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	b.Approvals = nil
	_, err = a.SigningRequest(b)
	require.ErrorIs(t, err, domain.ErrInvalidInput, "nothing to grant")
}

func TestApprovalsWithoutBuilderKey(t *testing.T) {
	a, _ := newApprovals(t, false)
	assert.False(t, a.Available())

	_, err := a.Prepare(t.Context(), domain.SignatureGnosisSafe, anvilAddress, "0x01", nil)
	require.ErrorIs(t, err, domain.ErrNoBuilderKey)

	_, err = a.Deployed(t.Context(), "0x01", domain.SignatureGnosisSafe)
	require.ErrorIs(t, err, domain.ErrNoBuilderKey)

	_, err = a.Submit(t.Context(), ports.ApprovalBatch{}, "0x")
	require.ErrorIs(t, err, domain.ErrNoBuilderKey)

	_, err = a.Confirmed(t.Context(), "tx-1")
	require.ErrorIs(t, err, domain.ErrNoBuilderKey)
}

func TestApprovalsDeployedAndEOAs(t *testing.T) {
	a, m := newApprovals(t, true)
	m.relayer.EXPECT().Deployed(mock.Anything, testBuilder, "0xWallet", true).Return(false, nil)

	deployed, err := a.Deployed(t.Context(), "0xWallet", domain.SignatureDepositWallet)
	require.NoError(t, err)
	assert.False(t, deployed)

	_, err = a.Prepare(t.Context(), domain.SignatureEOA, anvilAddress, anvilAddress, nil)
	require.ErrorIs(t, err, domain.ErrEOAApprovals)
}
