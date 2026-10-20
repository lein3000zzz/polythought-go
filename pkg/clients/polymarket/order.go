package polymarket

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// Orders are EIP-712 typed data, signed for one of Polymarket's exchanges with
// go-ethereum (D148), as @polymarket/client signs them; the tests hold them to vectors
// it generates (D151).

// Polygon is the chain Polymarket's exchanges are on.
const Polygon = 137

// Exchange is a contract orders are signed for: the EIP-712 domain's verifying contract
// and version.
type Exchange struct {
	Name    string
	Address string
	Version string
}

var (
	CTFExchange     = Exchange{Name: "CTF Exchange", Address: "0xE111180000d2663C0091e4f400237545B87B996B", Version: "2"}
	NegRiskExchange = Exchange{Name: "Neg Risk CTF Exchange", Address: "0xe2222d279d744050d28e00520010520000310F59", Version: "2"}
	ExchangeV3      = Exchange{Name: "Exchange V3", Address: "0xe3333700cA9d93003F00f0F71f8515005F6c00Aa", Version: "3"}
)

// v2Reserved are the bits a protocol v2 position ID leaves zero.
var v2Reserved = new(big.Int).Lsh(new(big.Int).SetUint64(math.MaxUint64), 40)

// ExchangeFor is the exchange an outcome's orders are signed for: ExchangeV3 for a
// protocol v2 position ID, told by its reserved bits as the official client tells it;
// otherwise the neg risk exchange for a neg risk market, and the CTF exchange.
func ExchangeFor(tokenID string, negRisk bool) (Exchange, error) {
	id, ok := new(big.Int).SetString(tokenID, 10)
	if !ok || id.Sign() < 0 {
		return Exchange{}, fmt.Errorf("polymarket: token ID %q is not a number", tokenID)
	}

	switch {
	case new(big.Int).And(id, v2Reserved).Sign() == 0:
		return ExchangeV3, nil
	case negRisk:
		return NegRiskExchange, nil
	default:
		return CTFExchange, nil
	}
}

// SignatureType says who makes an order and how its signature is checked.
type SignatureType uint8

const (
	// SignatureEOA: the key's own address makes and signs. New ones must be allowlisted.
	SignatureEOA SignatureType = 0
	// SignatureProxy: a Polymarket proxy wallet makes; its owner's key signs.
	SignatureProxy SignatureType = 1
	// SignatureSafe: a Gnosis Safe makes; its owner's key signs.
	SignatureSafe SignatureType = 2
	// SignatureDepositWallet (POLY_1271): a Deposit Wallet makes and signs, through
	// ERC-1271, with its owner's key or a session key.
	SignatureDepositWallet SignatureType = 3
)

// MaxSalt is the largest salt: it travels as a JSON number, from which the CLOB rebuilds
// the hash.
const MaxSalt = 1<<53 - 1

// NewSalt is a random salt within MaxSalt.
func NewSalt() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint64(b[:]) & MaxSalt, nil
}

// Order is what an order signs: the amounts in millionths, the timestamp in milliseconds.
// Its expiration, if any, is not signed: it goes with the signed order.
type Order struct {
	Salt          uint64
	Maker         string
	Signer        string
	TokenID       string // decimal
	MakerAmount   uint64
	TakerAmount   uint64
	Side          Side
	SignatureType SignatureType
	Timestamp     uint64
	Metadata      [32]byte
	Builder       [32]byte // a builder code, or zero
}

const orderTypeString = "Order(uint256 salt,address maker,address signer,uint256 tokenId," +
	"uint256 makerAmount,uint256 takerAmount,uint8 side,uint8 signatureType,uint256 timestamp," +
	"bytes32 metadata,bytes32 builder)"

var (
	domainType = []apitypes.Type{
		{Name: "name", Type: "string"}, {Name: "version", Type: "string"},
		{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"},
	}
	orderType = []apitypes.Type{
		{Name: "salt", Type: "uint256"}, {Name: "maker", Type: "address"}, {Name: "signer", Type: "address"},
		{Name: "tokenId", Type: "uint256"}, {Name: "makerAmount", Type: "uint256"}, {Name: "takerAmount", Type: "uint256"},
		{Name: "side", Type: "uint8"}, {Name: "signatureType", Type: "uint8"}, {Name: "timestamp", Type: "uint256"},
		{Name: "metadata", Type: "bytes32"}, {Name: "builder", Type: "bytes32"},
	}
	// typedDataSignType wraps an order for a Deposit Wallet (ERC-7739).
	typedDataSignType = []apitypes.Type{
		{Name: "contents", Type: "Order"}, {Name: "name", Type: "string"}, {Name: "version", Type: "string"},
		{Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}, {Name: "salt", Type: "bytes32"},
	}
)

// sessionMagic ends a session key's signature (ERC-6492).
var sessionMagic = common.FromHex("0x6492649264926492649264926492649264926492649264926492649264926492")

func (o Order) check() error {
	switch {
	case o.Salt > MaxSalt:
		return fmt.Errorf("polymarket: a salt of %d is over %d", o.Salt, uint64(MaxSalt))
	case o.Side != SideBuy && o.Side != SideSell:
		return fmt.Errorf("polymarket: no side %q", o.Side)
	case o.SignatureType > SignatureDepositWallet:
		return fmt.Errorf("polymarket: no signature type %d", o.SignatureType)
	case !common.IsHexAddress(o.Maker) || !common.IsHexAddress(o.Signer):
		return errors.New("polymarket: maker and signer must be addresses")
	}

	if _, ok := new(big.Int).SetString(o.TokenID, 10); !ok {
		return fmt.Errorf("polymarket: token ID %q is not a number", o.TokenID)
	}

	return nil
}

// checkIdentity holds an order to its signature type before it is signed: who makes it
// and who signs it.
func (o Order) checkIdentity() error {
	same := strings.EqualFold(o.Maker, o.Signer)

	switch {
	case (o.SignatureType == SignatureEOA || o.SignatureType == SignatureDepositWallet) && !same:
		return errors.New("polymarket: an EOA's or a Deposit Wallet's order is made and signed by the same address")
	case (o.SignatureType == SignatureProxy || o.SignatureType == SignatureSafe) && same:
		return errors.New("polymarket: a proxy's or a Safe's order is made by the wallet and signed by its owner")
	}

	return nil
}

func (o Order) message() apitypes.TypedDataMessage {
	side := "0"
	if o.Side == SideSell {
		side = "1"
	}

	return apitypes.TypedDataMessage{
		"salt": strconv.FormatUint(o.Salt, 10), "maker": o.Maker, "signer": o.Signer, "tokenId": o.TokenID,
		"makerAmount": strconv.FormatUint(o.MakerAmount, 10), "takerAmount": strconv.FormatUint(o.TakerAmount, 10),
		"side": side, "signatureType": strconv.Itoa(int(o.SignatureType)),
		"timestamp": strconv.FormatUint(o.Timestamp, 10),
		"metadata":  hexutil.Encode(o.Metadata[:]), "builder": hexutil.Encode(o.Builder[:]),
	}
}

func exchangeDomain(chainID int64, ex Exchange) apitypes.TypedDataDomain {
	return apitypes.TypedDataDomain{
		Name: "Polymarket CTF Exchange", Version: ex.Version,
		ChainId: (*ethmath.HexOrDecimal256)(big.NewInt(chainID)), VerifyingContract: ex.Address,
	}
}

// plainTypedData is the order itself on the exchange's domain.
func (o Order) plainTypedData(chainID int64, ex Exchange) apitypes.TypedData {
	return apitypes.TypedData{
		Types:       apitypes.Types{"EIP712Domain": domainType, "Order": orderType},
		PrimaryType: "Order",
		Domain:      exchangeDomain(chainID, ex),
		Message:     o.message(),
	}
}

// typedData is what an order's signature signs: the order itself, or for a Deposit
// Wallet the TypedDataSign that wraps it, on the exchange's domain still.
func (o Order) typedData(chainID int64, ex Exchange) apitypes.TypedData {
	td := o.plainTypedData(chainID, ex)

	if o.SignatureType != SignatureDepositWallet {
		return td
	}

	td.Types["TypedDataSign"] = typedDataSignType
	td.PrimaryType = "TypedDataSign"
	td.Message = apitypes.TypedDataMessage{
		"contents": o.message(), "name": "DepositWallet", "version": "1",
		"chainId": strconv.FormatInt(chainID, 10), "verifyingContract": o.Signer,
		"salt": hexutil.Encode(make([]byte, 32)),
	}

	return td
}

// Digest is the hash an order's signature signs, for ex on chainID.
func (o Order) Digest(chainID int64, ex Exchange) ([]byte, error) {
	if err := o.check(); err != nil {
		return nil, err
	}

	digest, _, err := apitypes.TypedDataAndHash(o.typedData(chainID, ex))

	return digest, err
}

// Hash is the order's hash as the exchange's hashOrder makes it, the EIP-712 hash of the
// order itself whatever wraps its signature: the order's ID on the CLOB, known before it
// is sent (D194). It is Digest but for a Deposit Wallet's orders.
func (o Order) Hash(chainID int64, ex Exchange) (string, error) {
	if err := o.check(); err != nil {
		return "", err
	}

	hash, _, err := apitypes.TypedDataAndHash(o.plainTypedData(chainID, ex))
	if err != nil {
		return "", err
	}

	return hexutil.Encode(hash), nil
}

// OrderSigner signs orders with a private key: the account's own, or a session key
// acting for its Deposit Wallet.
type OrderSigner struct {
	key     *ecdsa.PrivateKey
	session bool
}

// NewOrderSigner signs with key, a hex private key; with session, as a session key.
func NewOrderSigner(key string, session bool) (OrderSigner, error) {
	k, err := crypto.HexToECDSA(strings.TrimPrefix(key, "0x"))
	if err != nil {
		return OrderSigner{}, errors.New("polymarket: not a private key")
	}

	return OrderSigner{key: k, session: session}, nil
}

// Address is the key's address: the signer of an EOA's, a proxy's or a Safe's orders, or
// the session signer of a Deposit Wallet's.
func (s OrderSigner) Address() string {
	return crypto.PubkeyToAddress(s.key.PublicKey).Hex()
}

// Sign signs o for ex on chainID: 65 bytes for an EOA, a proxy or a Safe; the ERC-7739
// wrap, 317 bytes, for a Deposit Wallet; and around that, for a session key, the
// session signer's envelope.
func (s OrderSigner) Sign(chainID int64, ex Exchange, o Order) (string, error) {
	switch {
	case s.session && o.SignatureType != SignatureDepositWallet:
		return "", errors.New("polymarket: a session key signs for a Deposit Wallet only")
	case o.checkIdentity() != nil:
		return "", o.checkIdentity()
	case o.SignatureType != SignatureDepositWallet && !strings.EqualFold(o.Signer, s.Address()):
		return "", fmt.Errorf("polymarket: the order's signer is %s, not this key's %s", o.Signer, s.Address())
	}

	td := o.typedData(chainID, ex)

	digest, err := o.Digest(chainID, ex)
	if err != nil {
		return "", err
	}

	sig, err := crypto.Sign(digest, s.key)
	if err != nil {
		return "", err
	}

	sig[64] += 27 // Ethereum's v

	if o.SignatureType == SignatureDepositWallet {
		if sig, err = wrapForWallet(td, sig); err != nil {
			return "", err
		}
	}

	if s.session {
		if sig, err = sessionEnvelope(s.Address(), sig); err != nil {
			return "", err
		}
	}

	return hexutil.Encode(sig), nil
}

// wrapForWallet is a Deposit Wallet's signature (ERC-7739): the signature, the
// exchange's domain separator, the order's struct hash, its type, and that type's length.
func wrapForWallet(td apitypes.TypedData, sig []byte) ([]byte, error) {
	separator, err := td.HashStruct("EIP712Domain", td.Domain.Map())
	if err != nil {
		return nil, err
	}

	contents, err := td.HashStruct("Order", td.Message["contents"].(apitypes.TypedDataMessage))
	if err != nil {
		return nil, err
	}

	out := append(append(append(sig, separator...), contents...), orderTypeString...)

	return binary.BigEndian.AppendUint16(out, uint16(len(orderTypeString))), nil
}

var sessionArgs = func() abi.Arguments {
	b32, _ := abi.NewType("bytes32", "", nil)
	bs, _ := abi.NewType("bytes", "", nil)

	return abi.Arguments{{Type: b32}, {Type: b32}, {Type: bs}}
}()

// sessionEnvelope is a session key's signature for its Deposit Wallet: the session
// signer, zero, and the wallet's signature, ABI-encoded, then the ERC-6492 magic.
func sessionEnvelope(signer string, sig []byte) ([]byte, error) {
	var id [32]byte
	copy(id[12:], common.HexToAddress(signer).Bytes())

	packed, err := sessionArgs.Pack(id, [32]byte{}, sig)
	if err != nil {
		return nil, err
	}

	return append(packed, sessionMagic...), nil
}

// SignedOrder is an order with its signature, as POST /order takes it. Expiration is in
// seconds, 0 for none, and not signed.
type SignedOrder struct {
	Order

	Signature  string
	Expiration int64
}

// MarshalJSON writes the order as the CLOB takes it, as @polymarket/client sends it:
// amounts and times as text, the salt a number, the side BUY or SELL.
func (s SignedOrder) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Builder       string        `json:"builder"`
		Expiration    string        `json:"expiration"`
		Maker         string        `json:"maker"`
		MakerAmount   string        `json:"makerAmount"`
		Metadata      string        `json:"metadata"`
		Salt          uint64        `json:"salt"`
		Side          Side          `json:"side"`
		Signature     string        `json:"signature"`
		SignatureType SignatureType `json:"signatureType"`
		Signer        string        `json:"signer"`
		TakerAmount   string        `json:"takerAmount"`
		Timestamp     string        `json:"timestamp"`
		TokenID       string        `json:"tokenId"`
	}{
		Builder: "0x" + hex.EncodeToString(s.Builder[:]), Expiration: strconv.FormatInt(s.Expiration, 10),
		Maker: s.Maker, MakerAmount: strconv.FormatUint(s.MakerAmount, 10),
		Metadata: "0x" + hex.EncodeToString(s.Metadata[:]), Salt: s.Salt, Side: s.Side, Signature: s.Signature,
		SignatureType: s.SignatureType, Signer: s.Signer, TakerAmount: strconv.FormatUint(s.TakerAmount, 10),
		Timestamp: strconv.FormatUint(s.Timestamp, 10), TokenID: s.TokenID,
	})
}
