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
	HasSessionKey  bool // the key is an authorised session key (D156)
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
	SessionKey    bool // the signer is a Deposit Wallet's session key
}

// CredentialsInfo describes stored credentials without revealing them.
type CredentialsInfo struct {
	WalletID      WalletID
	APIKeyHint    string
	Signer        WalletAddress
	SignatureType SignatureType
	HasKey        bool // made from an imported private key, which is kept too
	SessionKey    bool // made by a session key, the key kept (D156)
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

// SessionKeyState is how far a session key's authorisation is (D156).
type SessionKeyState string

const (
	SessionKeySigning     SessionKeyState = "signing"     // waiting for the owner's signature
	SessionKeyAuthorizing SessionKeyState = "authorizing" // submitted, waiting for the chain
	SessionKeyActive      SessionKeyState = "active"      // the wallet trades with it
	SessionKeyFailed      SessionKeyState = "failed"
)

// SessionKey is a key the server made for a Deposit Wallet, which the wallet's owner
// authorises to place and cancel orders for it until ValidUntil (D156, D158). The key
// itself is never shown.
type SessionKey struct {
	WalletID   WalletID
	Signer     WalletAddress // the key's address
	Owner      WalletAddress // who controls the Deposit Wallet, and signs for it
	State      SessionKeyState
	ValidUntil time.Time
	Error      string // why it failed
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

var (
	// ErrSessionKeysOff means the instance has no Builder key for the Relayer (D156).
	ErrSessionKeysOff = errors.New("session keys need a Builder key, which an admin has not entered")
	// ErrNotDepositWallet means the wallet is not the Deposit Wallet of the browser's
	// account: session keys are for Deposit Wallets only.
	ErrNotDepositWallet = errors.New("the wallet is not this account's Deposit Wallet")
	// ErrWrongSigner means a batch was signed by someone other than its owner.
	ErrWrongSigner = errors.New("the signature is not the owner's")
	// ErrSignatureExpired means a batch was signed after its deadline.
	ErrSignatureExpired = errors.New("the signature came too late, sign again")
	// ErrSessionKeyInUse means the wallet trades with a session key, which only removing
	// the session key ends (D159).
	ErrSessionKeyInUse = errors.New("the wallet trades with a session key: remove the session key first")
	// ErrSessionKeyPending means a session key of the wallet is being authorised already.
	ErrSessionKeyPending = errors.New("a session key is being authorised for the wallet: wait for it")
	// ErrRelayerRefused means Polymarket's Relayer refused a session key's authorisation
	// or revocation, or its transaction failed.
	ErrRelayerRefused = errors.New("polymarket's relayer refused it")
)
