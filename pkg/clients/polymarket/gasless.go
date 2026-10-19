package polymarket

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// Gasless transactions (D161): a smart wallet's calls, signed by its owner and submitted
// to the Relayer, which pays the gas. A Deposit Wallet runs a Batch; a Safe a Safe
// transaction, through MultiSend for several calls; a proxy wallet a call on its factory.
// As @polymarket/client builds them, checked against what it submits (D151).

// The kinds of Relayer transaction.
const (
	RelayerDepositWallet = "WALLET"
	RelayerSafe          = "SAFE"
	RelayerProxy         = "PROXY"
)

// setupMetadata is what the official client says of trading setup approvals.
const setupMetadata = "Trading setup approvals"

// GaslessTransaction is one a smart wallet's owner signs for the Relayer to submit.
type GaslessTransaction interface {
	// Digest is the hash the owner's key signs.
	Digest() ([]byte, error)
	// Submission is the transaction, signed, as the Relayer's POST /submit takes it.
	Submission(signature []byte) (SubmitRequest, error)
}

// SubmitRequest is a signed gasless transaction, for POST /submit.
type SubmitRequest struct {
	Data                string               `json:"data,omitempty"`
	DepositWalletParams *DepositWalletParams `json:"depositWalletParams,omitempty"`
	From                string               `json:"from"`
	Metadata            string               `json:"metadata,omitempty"`
	Nonce               string               `json:"nonce"`
	ProxyWallet         string               `json:"proxyWallet,omitempty"`
	Signature           string               `json:"signature"`
	SignatureParams     map[string]string    `json:"signatureParams,omitempty"`
	To                  string               `json:"to"`
	Type                string               `json:"type"`
}

// DepositWalletParams are a Deposit Wallet transaction's Batch.
type DepositWalletParams struct {
	Calls         []DepositWalletCall `json:"calls"`
	Deadline      string              `json:"deadline"`
	DepositWallet string              `json:"depositWallet"`
}

// SignGasless signs tx with key, as the owner's wallet would.
func SignGasless(tx GaslessTransaction, key string) ([]byte, error) {
	k, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(key), "0x"))
	if err != nil {
		return nil, errors.New("polymarket: not a private key")
	}

	digest, err := tx.Digest()
	if err != nil {
		return nil, err
	}

	sig, err := crypto.Sign(digest, k)
	if err != nil {
		return nil, err
	}

	sig[64] += 27

	return sig, nil
}

// GaslessSigner is the address that signed tx: its owner, if the signature is theirs.
func GaslessSigner(tx GaslessTransaction, signature []byte) (common.Address, error) {
	if len(signature) != 65 {
		return common.Address{}, errors.New("polymarket: a signature is 65 bytes")
	}

	digest, err := tx.Digest()
	if err != nil {
		return common.Address{}, err
	}

	sig := append([]byte{}, signature...)
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	pub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		return common.Address{}, errors.New("polymarket: not a signature of this transaction")
	}

	return crypto.PubkeyToAddress(*pub), nil
}

// DepositWalletTransaction is a Deposit Wallet's Batch, which its owner signs as EIP-712
// typed data.
type DepositWalletTransaction struct {
	Owner, Wallet string
	Factory       common.Address
	Batch         Batch
	ChainID       int64
}

// NewDepositWalletTransaction batches calls for owner's Deposit Wallet, by the wallet's
// nonce, to be submitted before deadline.
func NewDepositWalletTransaction(owner, wallet, nonce string, deadline time.Time, calls []Call, d Derivation) DepositWalletTransaction {
	return DepositWalletTransaction{
		Owner: owner, Wallet: wallet, Factory: d.DepositWalletFactory, ChainID: Polygon,
		Batch: Batch{Wallet: wallet, Nonce: nonce, Deadline: deadline, Calls: depositWalletCalls(calls)},
	}
}

func (t DepositWalletTransaction) Digest() ([]byte, error) {
	return t.Batch.Digest(t.ChainID)
}

func (t DepositWalletTransaction) Submission(signature []byte) (SubmitRequest, error) {
	return SubmitRequest{
		DepositWalletParams: &DepositWalletParams{
			Calls: t.Batch.Calls, Deadline: strconv.FormatInt(t.Batch.Deadline.Unix(), 10), DepositWallet: t.Wallet,
		},
		From: t.Owner, Metadata: setupMetadata, Nonce: t.Batch.Nonce, Signature: hexutil.Encode(signature),
		To: t.Factory.Hex(), Type: RelayerDepositWallet,
	}, nil
}

// SafeTransaction is a Safe transaction, which its owner signs as a message: the hash of
// its EIP-712 typed data.
type SafeTransaction struct {
	Owner, Safe string
	To          common.Address
	Data        []byte
	Operation   uint8 // 0 a call, 1 a delegatecall to MultiSend
	Nonce       string
	ChainID     int64
}

// NewSafeTransaction makes calls a transaction of owner's Safe: one call as it is, more
// through MultiSend.
func NewSafeTransaction(owner, safe, nonce string, calls []Call, c Contracts) SafeTransaction {
	t := SafeTransaction{Owner: owner, Safe: safe, Nonce: nonce, ChainID: Polygon}

	if len(calls) == 1 {
		t.To, t.Data = calls[0].To, calls[0].Data
		return t
	}

	t.To, t.Data, t.Operation = c.SafeMultisend, multiSend(calls), 1

	return t
}

var multiSendSelector = selector("multiSend(bytes)")

// multiSend packs calls for MultiSend: each its operation (a call), address, value, data
// length and data, the lot ABI-encoded as bytes.
func multiSend(calls []Call) []byte {
	var packed []byte
	for _, c := range calls {
		packed = concat(packed, []byte{0}, c.To.Bytes(), word(nil), word(big.NewInt(int64(len(c.Data))).Bytes()), c.Data)
	}

	padded := common.RightPadBytes(packed, (len(packed)+31)/32*32)

	return concat(multiSendSelector, word([]byte{0x20}), word(big.NewInt(int64(len(packed))).Bytes()), padded)
}

// TypedData is the Safe transaction as EIP-712 typed data, gas paid by the Relayer.
func (t SafeTransaction) TypedData() apitypes.TypedData {
	zero := common.Address{}.Hex()

	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}},
			"SafeTx": {
				{Name: "to", Type: "address"}, {Name: "value", Type: "uint256"}, {Name: "data", Type: "bytes"},
				{Name: "operation", Type: "uint8"}, {Name: "safeTxGas", Type: "uint256"}, {Name: "baseGas", Type: "uint256"},
				{Name: "gasPrice", Type: "uint256"}, {Name: "gasToken", Type: "address"},
				{Name: "refundReceiver", Type: "address"}, {Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "SafeTx",
		Domain: apitypes.TypedDataDomain{
			ChainId: (*ethmath.HexOrDecimal256)(big.NewInt(t.ChainID)), VerifyingContract: t.Safe,
		},
		Message: apitypes.TypedDataMessage{
			"to": t.To.Hex(), "value": "0", "data": hexutil.Encode(t.Data), "operation": strconv.Itoa(int(t.Operation)),
			"safeTxGas": "0", "baseGas": "0", "gasPrice": "0", "gasToken": zero, "refundReceiver": zero, "nonce": t.Nonce,
		},
	}
}

// Hash is the Safe transaction's hash, the message its owner signs.
func (t SafeTransaction) Hash() ([]byte, error) {
	hash, _, err := apitypes.TypedDataAndHash(t.TypedData())
	return hash, err
}

func (t SafeTransaction) Digest() ([]byte, error) {
	hash, err := t.Hash()
	if err != nil {
		return nil, err
	}

	return accounts.TextHash(hash), nil
}

func (t SafeTransaction) Submission(signature []byte) (SubmitRequest, error) {
	if len(signature) != 65 {
		return SubmitRequest{}, errors.New("polymarket: a signature is 65 bytes")
	}

	// A Safe tells a signed message from typed data by v: 31 or 32 for a message.
	packed := append([]byte{}, signature...)
	if v := packed[64]; v < 27 {
		packed[64] = v + 31
	} else {
		packed[64] = v + 4
	}

	zero := common.Address{}.Hex()

	return SubmitRequest{
		Data: hexutil.Encode(t.Data), From: t.Owner, Metadata: setupMetadata, Nonce: t.Nonce, ProxyWallet: t.Safe,
		Signature: hexutil.Encode(packed),
		SignatureParams: map[string]string{
			"baseGas": "0", "gasPrice": "0", "gasToken": zero, "operation": strconv.Itoa(int(t.Operation)),
			"refundReceiver": zero, "safeTxnGas": "0",
		},
		To: t.To.Hex(), Type: RelayerSafe,
	}, nil
}

// ProxyTransaction is a proxy wallet's call on its factory through the RelayHub, which
// its owner signs as a message: the hash of the relay request.
type ProxyTransaction struct {
	Owner, Wallet string
	Factory       common.Address
	Data          []byte
	Nonce         string
	GasLimit      uint64
	Relay         string // the Relayer's address, from its execute params
	RelayHub      common.Address
}

// NewProxyTransaction makes calls a transaction of owner's proxy wallet. gasLimit is the
// call's estimate.
func NewProxyTransaction(owner, wallet, nonce, relay string, gasLimit uint64, data []byte, d Derivation, c Contracts) ProxyTransaction {
	return ProxyTransaction{
		Owner: owner, Wallet: wallet, Factory: d.ProxyFactory, Data: data,
		Nonce: nonce, GasLimit: gasLimit, Relay: relay, RelayHub: c.RelayHub,
	}
}

var proxyABI = mustABI(`[{"type":"function","name":"proxy","inputs":[{"name":"calls","type":"tuple[]","components":[
	{"name":"typeCode","type":"uint8"},{"name":"to","type":"address"},{"name":"value","type":"uint256"},{"name":"data","type":"bytes"}]}],
	"outputs":[{"name":"","type":"bytes[]"}]}]`)

func mustABI(definition string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		panic(err)
	}

	return a
}

// ProxyCall is the factory call making calls from the proxy wallet:
// proxy((uint8 typeCode, address to, uint256 value, bytes data)[]), each a call (1).
func ProxyCall(calls []Call) ([]byte, error) {
	type proxyCall struct {
		TypeCode uint8
		To       common.Address
		Value    *big.Int
		Data     []byte
	}

	args := make([]proxyCall, len(calls))
	for i, c := range calls {
		args[i] = proxyCall{TypeCode: 1, To: c.To, Value: new(big.Int), Data: c.Data}
	}

	return proxyABI.Pack("proxy", args)
}

// Hash is the relay request's hash, the message the owner signs: "rlx:", from, to, data,
// the relayer's fee, the gas price, the gas limit, the nonce, the RelayHub and the relay.
func (t ProxyTransaction) Hash() ([]byte, error) {
	nonce, ok := new(big.Int).SetString(t.Nonce, 10)
	if !ok {
		return nil, errors.New("polymarket: a proxy wallet's nonce is a number")
	}

	return crypto.Keccak256(
		[]byte("rlx:"), common.HexToAddress(t.Owner).Bytes(), t.Factory.Bytes(), t.Data,
		word(nil), word(nil), word(new(big.Int).SetUint64(t.GasLimit).Bytes()), word(nonce.Bytes()),
		t.RelayHub.Bytes(), common.HexToAddress(t.Relay).Bytes(),
	), nil
}

func (t ProxyTransaction) Digest() ([]byte, error) {
	hash, err := t.Hash()
	if err != nil {
		return nil, err
	}

	return accounts.TextHash(hash), nil
}

func (t ProxyTransaction) Submission(signature []byte) (SubmitRequest, error) {
	return SubmitRequest{
		Data: hexutil.Encode(t.Data), From: t.Owner, Metadata: setupMetadata, Nonce: t.Nonce, ProxyWallet: t.Wallet,
		Signature: hexutil.Encode(signature),
		SignatureParams: map[string]string{
			"gasLimit": strconv.FormatUint(t.GasLimit, 10), "gasPrice": "0", "relay": t.Relay,
			"relayHub": t.RelayHub.Hex(), "relayerFee": "0",
		},
		To: t.Factory.Hex(), Type: RelayerProxy,
	}, nil
}
