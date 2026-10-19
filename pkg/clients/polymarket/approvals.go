package polymarket

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
)

// Trading approvals (D160): what a wallet lets Polymarket's contracts move for it, as
// @polymarket/client's trading setup grants them. Checked against vectors it generates
// (D151).

// Contracts are the contracts Polymarket trades through.
type Contracts struct {
	Collateral        common.Address // pUSD
	ConditionalTokens common.Address // outcome tokens of v1 markets (CTF)
	PositionManager   common.Address // outcome tokens of v2 markets

	CTFExchange, NegRiskExchange, ExchangeV3    common.Address
	CollateralAdapter, NegRiskCollateralAdapter common.Address
	V2Router, PerpsDeposit                      common.Address
	AutoRedeem, BinaryModule, NegRiskModule     common.Address
	SafeMultisend, RelayHub                     common.Address
}

// ProductionContracts are Polygon's.
var ProductionContracts = Contracts{
	Collateral:               common.HexToAddress("0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB"),
	ConditionalTokens:        common.HexToAddress("0x4D97DCd97eC945f40cF65F87097ACe5EA0476045"),
	PositionManager:          common.HexToAddress("0x006F54F7f9A22e0000CC2AB60031000000ae9fEF"),
	CTFExchange:              common.HexToAddress(CTFExchange.Address),
	NegRiskExchange:          common.HexToAddress(NegRiskExchange.Address),
	ExchangeV3:               common.HexToAddress(ExchangeV3.Address),
	CollateralAdapter:        common.HexToAddress("0xAdA100Db00Ca00073811820692005400218FcE1f"),
	NegRiskCollateralAdapter: common.HexToAddress("0xadA2005600Dec949baf300f4C6120000bDB6eAab"),
	V2Router:                 common.HexToAddress("0x12121212006e4CD160D18e3f00711DA5c3372600"),
	PerpsDeposit:             common.HexToAddress("0xDCa4af75705dbB50f62437045afF9921947917d2"),
	AutoRedeem:               common.HexToAddress("0xa1200000d0002264C9a1698e001292D00E1b00af"),
	BinaryModule:             common.HexToAddress("0x1000008dD9001B968442c1000017eaE6E0dA00Ba"),
	NegRiskModule:            common.HexToAddress("0x200000900045e3B6259600682756002200028933"),
	SafeMultisend:            common.HexToAddress("0xA238CBeb142c10Ef7Ad8442C6D1f9E89e07e7761"),
	RelayHub:                 common.HexToAddress("0xD216153c06E857cD7f72665E0aF1d7D82172F494"),
}

// ApprovalStandard is the kind of token an approval is on.
type ApprovalStandard string

const (
	ERC20   ApprovalStandard = "ERC20"   // all of it, to a spender
	ERC1155 ApprovalStandard = "ERC1155" // all of them, to an operator
)

// Approval is one a wallet grants: Spender may move all of Token for it.
type Approval struct {
	Standard ApprovalStandard
	Token    common.Address
	Spender  common.Address
}

// TradingApprovals are the approvals @polymarket/client's trading setup grants, in its
// order: the collateral to the exchanges and adapters, then the outcome tokens.
func (c Contracts) TradingApprovals() []Approval {
	out := make([]Approval, 0, 17)

	for _, spender := range []common.Address{
		c.CTFExchange, c.NegRiskExchange, c.CollateralAdapter, c.NegRiskCollateralAdapter,
		c.V2Router, c.ExchangeV3, c.PerpsDeposit,
	} {
		out = append(out, Approval{Standard: ERC20, Token: c.Collateral, Spender: spender})
	}

	for _, operator := range []common.Address{
		c.CTFExchange, c.NegRiskExchange, c.CollateralAdapter, c.NegRiskCollateralAdapter,
		c.AutoRedeem, c.BinaryModule, c.NegRiskModule,
	} {
		out = append(out, Approval{Standard: ERC1155, Token: c.ConditionalTokens, Spender: operator})
	}

	for _, operator := range []common.Address{c.V2Router, c.ExchangeV3, c.AutoRedeem} {
		out = append(out, Approval{Standard: ERC1155, Token: c.PositionManager, Spender: operator})
	}

	return out
}

// Call is a contract call a wallet makes, sending no value.
type Call struct {
	To   common.Address
	Data []byte
}

var (
	approveSelector           = selector("approve(address,uint256)")
	allowanceSelector         = selector("allowance(address,address)")
	setApprovalForAllSelector = selector("setApprovalForAll(address,bool)")
	isApprovedForAllSelector  = selector("isApprovedForAll(address,address)")

	maxUint256 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
)

func selector(signature string) []byte {
	return crypto.Keccak256([]byte(signature))[:4]
}

func word(b []byte) []byte {
	return common.LeftPadBytes(b, 32)
}

// Grant is the call granting the approval: approve(spender, max), or
// setApprovalForAll(operator, true).
func (a Approval) Grant() Call {
	if a.Standard == ERC20 {
		return Call{To: a.Token, Data: concat(approveSelector, word(a.Spender.Bytes()), word(maxUint256.Bytes()))}
	}

	return Call{To: a.Token, Data: concat(setApprovalForAllSelector, word(a.Spender.Bytes()), word([]byte{1}))}
}

// Check is the call reading whether wallet granted it: allowance(wallet, spender), or
// isApprovedForAll(wallet, operator).
func (a Approval) Check(wallet common.Address) Call {
	sel := isApprovedForAllSelector
	if a.Standard == ERC20 {
		sel = allowanceSelector
	}

	return Call{To: a.Token, Data: concat(sel, word(wallet.Bytes()), word(a.Spender.Bytes()))}
}

// Granted reads Check's result: an allowance of all of it, as the setup grants, or an
// operator approved.
func (a Approval) Granted(result []byte) (bool, error) {
	if len(result) != 32 {
		return false, errors.New("polymarket: an approval check returns one word")
	}

	n := new(big.Int).SetBytes(result)
	if a.Standard == ERC20 {
		return n.Cmp(maxUint256) == 0, nil
	}

	return n.Sign() != 0, nil
}

// depositWalletCalls are calls as a Deposit Wallet's Batch takes them.
func depositWalletCalls(calls []Call) []DepositWalletCall {
	out := make([]DepositWalletCall, len(calls))
	for i, c := range calls {
		out[i] = DepositWalletCall{Target: c.To.Hex(), Value: "0", Data: hexutil.Encode(c.Data)}
	}

	return out
}

// ApprovalsSnapshot is what the Data API indexed of a wallet's approvals: a row for each
// contract of Polymarket's catalog, which grows on its own.
type ApprovalsSnapshot struct {
	Address   string        `json:"address"`
	ChainID   int64         `json:"chain_id"`
	CheckedAt time.Time     `json:"checked_at"`
	Contracts []ApprovalRow `json:"contracts"`
}

// ApprovalRow is one approval of the catalog.
type ApprovalRow struct {
	ID       string           `json:"id"`
	Feature  string           `json:"feature"`
	Token    string           `json:"token"`
	Spender  string           `json:"spender"`
	Standard ApprovalStandard `json:"standard"`
	Amount   string           `json:"amount"` // ERC-20: "max", or the allowance
	Approved bool             `json:"approved"`
}

// Missing are those of required the snapshot does not show granted, as the official
// client reads it: an ERC-20 row approved for all of it, an ERC-1155 row approved. Each
// required approval must have exactly one row.
func (s ApprovalsSnapshot) Missing(wallet common.Address, required []Approval) ([]Approval, error) {
	if common.HexToAddress(s.Address) != wallet || s.ChainID != Polygon {
		return nil, errors.New("polymarket: the approvals are of another wallet or chain")
	}

	var missing []Approval

	for _, a := range required {
		var found []ApprovalRow

		for _, row := range s.Contracts {
			if common.HexToAddress(row.Token) == a.Token && common.HexToAddress(row.Spender) == a.Spender {
				found = append(found, row)
			}
		}

		if len(found) != 1 || found[0].Standard != a.Standard {
			return nil, fmt.Errorf("polymarket: no single %s approval of %s to %s", a.Standard, a.Token, a.Spender)
		}

		if !found[0].Approved || (a.Standard == ERC20 && !allOf(found[0].Amount)) {
			missing = append(missing, a)
		}
	}

	return missing, nil
}

func allOf(amount string) bool {
	if strings.EqualFold(amount, "max") {
		return true
	}

	n, ok := new(big.Int).SetString(amount, 10)

	return ok && n.Cmp(maxUint256) == 0
}
