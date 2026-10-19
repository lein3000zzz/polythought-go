package polymarket

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type approvalVectors struct {
	Keys      struct{ Owner string }
	Approvals []struct {
		Name       string
		WalletType int
		Owner      string
		Wallet     string
		Checks     []struct{ To, Data string }
		Approved   []bool
		Submitted  json.RawMessage
	}
}

// The official client's wallet types.
const (
	vectorProxy         = 1
	vectorSafe          = 2
	vectorDepositWallet = 3
)

func loadApprovalVectors(t *testing.T) approvalVectors {
	t.Helper()

	var v approvalVectors
	require.NoError(t, json.Unmarshal(fixture(t, "order_vectors.json"), &v))
	require.Len(t, v.Approvals, 6)

	return v
}

func TestTradingApprovalsMatchTheOfficialClient(t *testing.T) {
	v := loadApprovalVectors(t)
	approvals := ProductionContracts.TradingApprovals()
	require.Len(t, approvals, 17)

	for _, tc := range v.Approvals {
		t.Run(tc.Name, func(t *testing.T) {
			wallet := common.HexToAddress(tc.Wallet)

			// The same checks, in the same order.
			require.Len(t, tc.Checks, len(approvals))
			for i, a := range approvals {
				check := a.Check(wallet)
				assert.Equal(t, common.HexToAddress(tc.Checks[i].To), check.To, "check %d", i)
				assert.Equal(t, tc.Checks[i].Data, hexutil.Encode(check.Data), "check %d", i)
			}

			var missing []Call
			for i, a := range approvals {
				if !tc.Approved[i] {
					missing = append(missing, a.Grant())
				}
			}

			if len(missing) == 0 {
				assert.Equal(t, "null", string(tc.Submitted), "nothing to grant, nothing submitted")
				return
			}

			var tx GaslessTransaction

			switch tc.WalletType {
			case vectorDepositWallet:
				tx = NewDepositWalletTransaction(tc.Owner, tc.Wallet, "7", time.Unix(1760875800, 0), missing, Production)
			case vectorSafe:
				tx = NewSafeTransaction(tc.Owner, tc.Wallet, "7", missing, ProductionContracts)
			case vectorProxy:
				data, err := ProxyCall(missing)
				require.NoError(t, err)
				tx = NewProxyTransaction(tc.Owner, tc.Wallet, "7", "0x1111111111111111111111111111111111111111", 123456, data, Production, ProductionContracts)
			default:
				t.Fatalf("wallet type %d", tc.WalletType)
			}

			sig, err := SignGasless(tx, v.Keys.Owner)
			require.NoError(t, err)

			signer, err := GaslessSigner(tx, sig)
			require.NoError(t, err)
			assert.Equal(t, common.HexToAddress(tc.Owner), signer)

			req, err := tx.Submission(sig)
			require.NoError(t, err)

			got, err := json.Marshal(req)
			require.NoError(t, err)
			assert.JSONEq(t, string(tc.Submitted), string(got))
		})
	}
}

func TestApprovalGranted(t *testing.T) {
	erc20 := Approval{Standard: ERC20}
	erc1155 := Approval{Standard: ERC1155}

	full := common.LeftPadBytes(maxUint256.Bytes(), 32)
	some := common.LeftPadBytes([]byte{0x10}, 32)
	none := make([]byte, 32)

	for _, tc := range []struct {
		a      Approval
		result []byte
		want   bool
	}{
		{erc20, full, true},
		{erc20, some, false}, // the setup grants all of it
		{erc20, none, false},
		{erc1155, common.LeftPadBytes([]byte{1}, 32), true},
		{erc1155, none, false},
	} {
		got, err := tc.a.Granted(tc.result)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}

	_, err := erc20.Granted([]byte{1})
	require.Error(t, err)
}

func TestGaslessSignerOfAnotherTransaction(t *testing.T) {
	v := loadApprovalVectors(t)
	calls := []Call{ProductionContracts.TradingApprovals()[0].Grant()}

	tx := NewSafeTransaction(v.Approvals[2].Owner, v.Approvals[2].Wallet, "7", calls, ProductionContracts)
	sig, err := SignGasless(tx, v.Keys.Owner)
	require.NoError(t, err)

	other := NewSafeTransaction(v.Approvals[2].Owner, v.Approvals[2].Wallet, "8", calls, ProductionContracts)
	signer, err := GaslessSigner(other, sig)
	require.NoError(t, err)
	assert.NotEqual(t, common.HexToAddress(v.Approvals[2].Owner), signer)

	_, err = GaslessSigner(tx, sig[:64])
	require.Error(t, err)

	_, err = SignGasless(tx, "not a key")
	require.Error(t, err)
}
