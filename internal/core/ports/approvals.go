package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// ApprovalBatch is a smart wallet's transaction granting approvals, prepared for its owner
// to sign: by Nonce, a Deposit Wallet's before Deadline; a proxy wallet's with the Relay
// and GasLimit its signature covers.
type ApprovalBatch struct {
	Kind          domain.SignatureType
	Wallet, Owner domain.WalletAddress
	Approvals     []domain.Approval
	Nonce         string
	Deadline      time.Time
	Relay         string
	GasLimit      uint64
}

// SigningRequest is what a browser wallet signs: TypedData with eth_signTypedData_v4 (a
// Deposit Wallet's), or Message, hex, with personal_sign (a Safe's).
type SigningRequest struct {
	TypedData json.RawMessage
	Message   string
}

// TradingApprovals reads and sets up the approvals a wallet trades with (D160 to D163):
// through Polymarket's Data API, a Polygon RPC, and the Relayer with the operator's Builder
// key. Keys cross it decrypted, in memory only.
type TradingApprovals interface {
	// Status is what Polymarket's index shows missing, and when it last looked: public, and
	// perhaps a minute behind.
	Status(ctx context.Context, wallet domain.WalletAddress) (missing []domain.Approval, checkedAt time.Time, err error)
	// Missing reads them on chain.
	Missing(ctx context.Context, wallet domain.WalletAddress) ([]domain.Approval, error)

	// Available says whether an admin entered a Builder key.
	Available() bool
	// Kind says how owner controls wallet: as a proxy wallet, Safe or Deposit Wallet, or
	// itself (an EOA); false when it does not.
	Kind(owner, wallet domain.WalletAddress) (domain.SignatureType, bool)
	// Deployed says whether the smart wallet is on chain.
	Deployed(ctx context.Context, wallet domain.WalletAddress, kind domain.SignatureType) (bool, error)

	// Prepare is the transaction granting approvals from owner's wallet, now: it takes the
	// nonce from the Relayer, and estimates a proxy wallet's gas.
	Prepare(ctx context.Context, kind domain.SignatureType, owner, wallet domain.WalletAddress, approvals []domain.Approval) (ApprovalBatch, error)
	// SigningRequest is the batch as a browser wallet signs it.
	SigningRequest(b ApprovalBatch) (SigningRequest, error)
	// Signer is who signed the batch; domain.ErrInvalidInput when it is no signature.
	Signer(b ApprovalBatch, signature string) (domain.WalletAddress, error)
	// Sign signs the batch with the owner's key.
	Sign(b ApprovalBatch, key string) (string, error)
	// Submit sends the signed batch, returning the transaction to follow.
	Submit(ctx context.Context, b ApprovalBatch, signature string) (transactionID string, err error)
	// Confirmed says whether the transaction is confirmed: false while it is pending, and
	// domain.ErrRelayerRefused, saying why, when it failed.
	Confirmed(ctx context.Context, transactionID string) (bool, error)
}

// StoredApprovalSetup is a setup with the batch its owner signs or signed.
type StoredApprovalSetup struct {
	Info          domain.ApprovalSetup
	Approvals     []domain.Approval
	Nonce         string
	Deadline      time.Time
	TransactionID string
}

// ApprovalSetupRepository keeps the last approval setup of each wallet.
type ApprovalSetupRepository interface {
	// SaveApprovalSetup creates or replaces the wallet's setup.
	SaveApprovalSetup(ctx context.Context, s StoredApprovalSetup) error
	// ApprovalSetup is the wallet's; domain.ErrNotFound without one.
	ApprovalSetup(ctx context.Context, wallet domain.WalletID) (StoredApprovalSetup, error)
	// SubmittedApprovalSetups are those of every wallet waiting for the chain.
	SubmittedApprovalSetups(ctx context.Context) ([]StoredApprovalSetup, error)
}
