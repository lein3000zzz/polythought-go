package polymarket

import (
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// headerNonce is L1's, besides the address, signature and timestamp it shares with L2.
const headerNonce = "POLY_NONCE"

const clobAuthMessage = "This message attests that I control the given wallet"

// L1Auth authenticates the requests that make an account's API key: a ClobAuth
// signature by its key, as @polymarket/client signs it, made afresh for each attempt.
type L1Auth struct {
	Signer  OrderSigner
	ChainID int64
	Nonce   uint64
}

func (a L1Auth) headers(now time.Time, _, _ string, _ []byte) (http.Header, error) {
	ts := now.Unix()

	sig, err := a.Signer.SignClobAuth(a.ChainID, ts, a.Nonce)
	if err != nil {
		return nil, err
	}

	h := http.Header{}
	h.Set(headerAddress, a.Signer.Address())
	h.Set(headerSignature, sig)
	h.Set(headerTimestamp, strconv.FormatInt(ts, 10))
	h.Set(headerNonce, strconv.FormatUint(a.Nonce, 10))

	return h, nil
}

// SignClobAuth signs that the key controls its address, at timestamp (seconds) with
// nonce: what the CLOB asks to make or give again an API key.
func (s OrderSigner) SignClobAuth(chainID, timestamp int64, nonce uint64) (string, error) {
	td := apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": {{Name: "name", Type: "string"}, {Name: "version", Type: "string"}, {Name: "chainId", Type: "uint256"}},
			"ClobAuth": {
				{Name: "address", Type: "address"}, {Name: "timestamp", Type: "string"},
				{Name: "nonce", Type: "uint256"}, {Name: "message", Type: "string"},
			},
		},
		PrimaryType: "ClobAuth",
		Domain: apitypes.TypedDataDomain{
			Name: "ClobAuthDomain", Version: "1", ChainId: (*ethmath.HexOrDecimal256)(big.NewInt(chainID)),
		},
		Message: apitypes.TypedDataMessage{
			"address": s.Address(), "timestamp": strconv.FormatInt(timestamp, 10),
			"nonce": strconv.FormatUint(nonce, 10), "message": clobAuthMessage,
		},
	}

	digest, _, err := apitypes.TypedDataAndHash(td)
	if err != nil {
		return "", err
	}

	sig, err := crypto.Sign(digest, s.key)
	if err != nil {
		return "", err
	}

	sig[64] += 27

	return hexutil.Encode(sig), nil
}
