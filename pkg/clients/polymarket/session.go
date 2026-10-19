package polymarket

import (
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// Session keys (D156): a Deposit Wallet's owner signs a Batch the wallet runs, calling
// authorizeSessionSigner on itself, and the Relayer submits it with a Builder key; the
// same to revoke one. As @polymarket/client builds them, checked against vectors it
// generates (D151).

// SessionKeyLifetime is how long a session key is authorised for: 180 days less 5 hours,
// the only lifetime the Relayer takes.
const SessionKeyLifetime = 4_315 * time.Hour

// BatchDeadline is how long the owner's signature of a Batch is good for.
const BatchDeadline = 10 * time.Minute

// SessionScopeCLOB lets a session key place and cancel orders, and nothing else (D158).
const SessionScopeCLOB = "CLOB"

// BuilderCredentials are the operator's Builder API key, which the Relayer takes for
// session keys.
type BuilderCredentials struct {
	Key, Secret, Passphrase string
}

func (b BuilderCredentials) headers(now time.Time, method, path string, body []byte) (http.Header, error) {
	ts := now.Unix()

	sig, err := L2Credentials{Secret: b.Secret}.Sign(ts, method, path, body)
	if err != nil {
		return nil, err
	}

	h := http.Header{}
	h.Set("POLY_BUILDER_API_KEY", b.Key)
	h.Set("POLY_BUILDER_PASSPHRASE", b.Passphrase)
	h.Set("POLY_BUILDER_SIGNATURE", sig)
	h.Set("POLY_BUILDER_TIMESTAMP", strconv.FormatInt(ts, 10))

	return h, nil
}

// idempotent sends a Builder-signed request once, however often it is retried.
type idempotent struct {
	auth BuilderCredentials
	key  string
}

func (i idempotent) headers(now time.Time, method, path string, body []byte) (http.Header, error) {
	h, err := i.auth.headers(now, method, path, body)
	if err != nil {
		return nil, err
	}

	h.Set("Idempotency-Key", i.key)

	return h, nil
}

// DepositWalletCall is one call a Deposit Wallet makes in a Batch.
type DepositWalletCall struct {
	Target string `json:"target"`
	Value  string `json:"value"`
	Data   string `json:"data"`
}

var (
	authorizeSelector = crypto.Keccak256([]byte("authorizeSessionSigner(address,uint256)"))[:4]
	revokeSelector    = crypto.Keccak256([]byte("revokeSessionSigner(address)"))[:4]
)

// AuthorizeSessionSignerCall authorises signer on wallet until validUntil.
func AuthorizeSessionSignerCall(wallet, signer string, validUntil time.Time) DepositWalletCall {
	data := append(append([]byte{}, authorizeSelector...), common.LeftPadBytes(common.HexToAddress(signer).Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(big.NewInt(validUntil.Unix()).Bytes(), 32)...)

	return DepositWalletCall{Target: wallet, Value: "0", Data: hexutil.Encode(data)}
}

// RevokeSessionSignerCall revokes signer on wallet.
func RevokeSessionSignerCall(wallet, signer string) DepositWalletCall {
	data := append(append([]byte{}, revokeSelector...), common.LeftPadBytes(common.HexToAddress(signer).Bytes(), 32)...)

	return DepositWalletCall{Target: wallet, Value: "0", Data: hexutil.Encode(data)}
}

// Batch is what a Deposit Wallet's owner signs for it to run calls: by the wallet's
// nonce, before deadline.
type Batch struct {
	Wallet   string
	Nonce    string
	Deadline time.Time
	Calls    []DepositWalletCall
}

// TypedData is the Batch as EIP-712 typed data: what the owner's browser wallet signs with
// eth_signTypedData_v4, and what the server checks the signature against.
func (b Batch) TypedData(chainID int64) apitypes.TypedData {
	calls := make([]interface{}, len(b.Calls))
	for i, c := range b.Calls {
		calls[i] = map[string]interface{}{"target": c.Target, "value": c.Value, "data": c.Data}
	}

	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {
				{Name: "name", Type: "string"}, {Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"},
			},
			"Batch": {
				{Name: "wallet", Type: "address"}, {Name: "nonce", Type: "uint256"},
				{Name: "deadline", Type: "uint256"}, {Name: "calls", Type: "Call[]"},
			},
			"Call": {{Name: "target", Type: "address"}, {Name: "value", Type: "uint256"}, {Name: "data", Type: "bytes"}},
		},
		PrimaryType: "Batch",
		Domain: apitypes.TypedDataDomain{
			Name: "DepositWallet", Version: "1",
			ChainId: (*ethmath.HexOrDecimal256)(big.NewInt(chainID)), VerifyingContract: b.Wallet,
		},
		Message: apitypes.TypedDataMessage{
			"wallet": b.Wallet, "nonce": b.Nonce, "deadline": strconv.FormatInt(b.Deadline.Unix(), 10), "calls": calls,
		},
	}
}

// TypedDataJSON is TypedData as eth_signTypedData_v4 takes it, chainId a number as
// browser wallets compare it with the chain they are on.
func (b Batch) TypedDataJSON(chainID int64) ([]byte, error) {
	td := b.TypedData(chainID)

	return json.Marshal(map[string]any{
		"types":       td.Types,
		"primaryType": td.PrimaryType,
		"domain": map[string]any{
			"name": td.Domain.Name, "version": td.Domain.Version, "chainId": chainID, "verifyingContract": b.Wallet,
		},
		"message": td.Message,
	})
}

// Digest is the hash the owner signs.
func (b Batch) Digest(chainID int64) ([]byte, error) {
	digest, _, err := apitypes.TypedDataAndHash(b.TypedData(chainID))
	return digest, err
}

// Signer is the address that signed the Batch: its owner, if the signature is theirs.
func (b Batch) Signer(chainID int64, signature string) (string, error) {
	sig, err := hexutil.Decode(signature)
	if err != nil || len(sig) != 65 {
		return "", errors.New("polymarket: a signature is 65 bytes of hex")
	}

	digest, err := b.Digest(chainID)
	if err != nil {
		return "", err
	}

	sig = append([]byte{}, sig...)
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	pub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		return "", errors.New("polymarket: not a signature of this batch")
	}

	return crypto.PubkeyToAddress(*pub).Hex(), nil
}

// NewSessionKey is a new key, hex, and its address: the session signer.
func NewSessionKey() (key, address string, err error) {
	k, err := crypto.GenerateKey()
	if err != nil {
		return "", "", err
	}

	return hexutil.Encode(crypto.FromECDSA(k)), crypto.PubkeyToAddress(k.PublicKey).Hex(), nil
}

// lowerAddress is an address as the Relayer's session-signer endpoints take it, as the
// official client sends it.
func lowerAddress(a string) string {
	return strings.ToLower(a)
}
