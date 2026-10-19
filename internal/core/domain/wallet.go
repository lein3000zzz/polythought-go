package domain

import (
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

type WalletID string

var (
	ErrInvalidAddress = errors.New("invalid wallet address")
	ErrWalletExists   = errors.New("wallet already linked")
)

// ParseAddress validates an Ethereum address and returns it EIP-55 checksummed.
// Mixed-case input must carry a correct checksum, which catches typos; all-lowercase or
// all-uppercase input has none to check and is accepted.
func ParseAddress(raw string) (WalletAddress, error) {
	s := strings.TrimSpace(raw)

	hexPart, ok := strings.CutPrefix(s, "0x")
	if !ok || len(hexPart) != 40 {
		return "", ErrInvalidAddress
	}

	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", ErrInvalidAddress
	}

	sum := checksum(hexPart)

	lower, upper := strings.ToLower(hexPart), strings.ToUpper(hexPart)
	if hexPart != lower && hexPart != upper && hexPart != sum {
		return "", ErrInvalidAddress
	}

	return WalletAddress("0x" + sum), nil
}

// checksum applies EIP-55: a hex letter is upper case when the matching nibble of
// keccak256(lowercase address) is 8 or more.
func checksum(hexPart string) string {
	lower := strings.ToLower(hexPart)

	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(lower))
	digest := hex.EncodeToString(h.Sum(nil))

	out := []byte(lower)
	for i, c := range out {
		if c >= 'a' && c <= 'f' && digest[i] >= '8' {
			out[i] = c - 'a' + 'A'
		}
	}

	return string(out)
}

// Wallet is a Polymarket account a user follows: the address that holds positions
// (the proxy or "funder" wallet for proxy and safe accounts).
type Wallet struct {
	ID             WalletID
	UserID         UserID
	Address        WalletAddress
	Label          string
	CreatedAt      time.Time
	HasCredentials bool
	HasKey         bool // a private key imported: it can trade (D153)
}

// SignatureType is how a Polymarket account signs orders.
type SignatureType int

const (
	SignatureEOA           SignatureType = 0
	SignaturePolyProxy     SignatureType = 1
	SignatureGnosisSafe    SignatureType = 2
	SignatureDepositWallet SignatureType = 3
)

func (s SignatureType) Valid() bool {
	return s >= SignatureEOA && s <= SignatureDepositWallet
}

// PolymarketCredentials are the L2 API credentials of one wallet. They exist decrypted
// only in memory, for signing requests.
type PolymarketCredentials struct {
	APIKey     string
	Secret     string
	Passphrase string

	// Signer is the address the key belongs to, sent as POLY_ADDRESS. It equals the
	// wallet address for EOA and deposit-wallet accounts.
	Signer        WalletAddress
	SignatureType SignatureType
}

// CredentialsInfo describes stored credentials without revealing them.
type CredentialsInfo struct {
	WalletID      WalletID
	APIKeyHint    string
	Signer        WalletAddress
	SignatureType SignatureType
	HasKey        bool // made from an imported private key, which is kept too
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

var (
	// ErrKeyImportOff means the instance does not take private keys (D21).
	ErrKeyImportOff = errors.New("this instance does not take private keys")
	// ErrKeyNotForWallet means a private key controls neither the wallet nor its
	// proxy, Safe or Deposit Wallet (D155).
	ErrKeyNotForWallet = errors.New("this key does not control the wallet")
)
