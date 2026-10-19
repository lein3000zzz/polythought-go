package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// SealedSessionKey is a stored session key: the key sealed by a SecretBox until it is
// authorised, when it moves to its wallet's credentials (Key is nil then); and the batch
// its owner signs or signed last.
type SealedSessionKey struct {
	Info          domain.SessionKey
	Key           []byte
	Nonce         string
	Deadline      time.Time
	TransactionID string
}

// ExpiringSessionKey is an active session key soon to expire, with its wallet.
type ExpiringSessionKey struct {
	Key    domain.SessionKey
	Wallet domain.Wallet
}

// SessionKeyRepository keeps Deposit Wallets' session keys (D156): at most one active a
// wallet, and the one being authorised to replace it.
type SessionKeyRepository interface {
	// AddSessionKey adds k to its wallet, dropping the wallet's keys that are not active.
	AddSessionKey(ctx context.Context, k SealedSessionKey) error
	// SaveSessionKey changes a stored key; domain.ErrNotFound if it is gone.
	SaveSessionKey(ctx context.Context, k SealedSessionKey) error
	// SessionKeys are a wallet's, newest first.
	SessionKeys(ctx context.Context, wallet domain.WalletID) ([]SealedSessionKey, error)
	// AuthorizingSessionKeys are those of every wallet submitted to the Relayer.
	AuthorizingSessionKeys(ctx context.Context) ([]SealedSessionKey, error)
	// ActivateSessionKey makes k its wallet's active key, with the credentials it made,
	// at once: the wallet's other keys go. domain.ErrNotFound if k is gone.
	ActivateSessionKey(ctx context.Context, k SealedSessionKey, creds SealedCredentials) error
	// DeleteSessionKeys removes a wallet's session keys, and its credentials if a session
	// key made them.
	DeleteSessionKeys(ctx context.Context, wallet domain.WalletID) error
	// ExpiringSessionKeys are the active keys valid until before, not yet reminded of.
	ExpiringSessionKeys(ctx context.Context, before time.Time) ([]ExpiringSessionKey, error)
	// MarkReminded records that a key's owner was reminded it expires.
	MarkReminded(ctx context.Context, wallet domain.WalletID, signer domain.WalletAddress, at time.Time) error
}

// SessionKeyBatch is what a Deposit Wallet's owner signs: authorising a session key until
// ValidUntil, or revoking it (Revoke). By Nonce, before Deadline.
type SessionKeyBatch struct {
	Wallet, Owner, Signer domain.WalletAddress
	Revoke                bool
	ValidUntil            time.Time
	Nonce                 string
	Deadline              time.Time
}

// SessionKeyRelayer authorises and revokes Deposit Wallet session keys through
// Polymarket's Relayer, with the operator's Builder key (D156). Keys cross it decrypted,
// in memory only.
type SessionKeyRelayer interface {
	// Available says whether an admin entered a Builder key.
	Available() bool
	// NewKey makes a session key: the key, and its address.
	NewKey() (key string, signer domain.WalletAddress, err error)
	// IsDepositWallet says whether wallet is owner's Deposit Wallet.
	IsDepositWallet(owner, wallet domain.WalletAddress) bool

	// Authorization is a batch authorising signer for owner's wallet from now; Revocation
	// one revoking it. Both take the owner's nonce from the Relayer.
	Authorization(ctx context.Context, owner, wallet, signer domain.WalletAddress) (SessionKeyBatch, error)
	Revocation(ctx context.Context, owner, wallet, signer domain.WalletAddress) (SessionKeyBatch, error)
	// TypedData is the batch as EIP-712 typed data, for the browser's eth_signTypedData_v4.
	TypedData(b SessionKeyBatch) (json.RawMessage, error)
	// Signer is who signed the batch; domain.ErrInvalidInput when it is no signature.
	Signer(b SessionKeyBatch, signature string) (domain.WalletAddress, error)

	// Submit sends a signed batch, returning the transaction to follow;
	// domain.ErrRelayerRefused when the Relayer refuses it.
	Submit(ctx context.Context, b SessionKeyBatch, signature string) (transactionID string, err error)
	// Confirmed says whether the transaction is confirmed: false while it is pending, and
	// domain.ErrRelayerRefused, saying why, when it failed.
	Confirmed(ctx context.Context, transactionID string) (bool, error)
}

// SessionKeyReminder tells a user their wallet's session key expires soon. It returns nil
// when it tells nobody, by their choice or for want of a chat, so the key is not asked
// about again.
type SessionKeyReminder interface {
	RemindSessionKey(ctx context.Context, user domain.UserID, w domain.Wallet, until time.Time) error
}
