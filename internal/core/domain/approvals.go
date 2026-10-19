package domain

import (
	"errors"
	"time"
)

// Approval is one a wallet grants Polymarket's contracts so it can trade (D160): Spender
// may move all of Token for it.
type Approval struct {
	Token    WalletAddress
	Spender  WalletAddress
	Standard string // ERC20, or ERC1155
	Name     string // what it is for people, like "pUSD to the CTF Exchange"
}

// ApprovalSetupState is how far setting up a wallet's approvals is.
type ApprovalSetupState string

const (
	ApprovalSetupSigning   ApprovalSetupState = "signing"   // waiting for the owner's signature
	ApprovalSetupSubmitted ApprovalSetupState = "submitted" // waiting for the chain
	ApprovalSetupDone      ApprovalSetupState = "done"
	ApprovalSetupFailed    ApprovalSetupState = "failed"
)

// ApprovalSetup is the last setting up of a smart wallet's approvals, through the Relayer
// (D161): Kind is the wallet's, a proxy wallet, Safe or Deposit Wallet.
type ApprovalSetup struct {
	WalletID  WalletID
	Owner     WalletAddress // who signed, or signs, for the wallet
	Kind      SignatureType
	Granting  int // how many approvals it grants
	State     ApprovalSetupState
	Error     string // why it failed
	CreatedAt time.Time
	UpdatedAt time.Time
}

var (
	// ErrNoBuilderKey means the instance has no Builder key for the Relayer, which smart
	// wallets' transactions need (D156, D161).
	ErrNoBuilderKey = errors.New("this needs a Builder key, which an admin has not entered")
	// ErrEOAApprovals means the wallet is an EOA, which grants approvals with its own
	// wallet, paying the gas (D162).
	ErrEOAApprovals = errors.New("an EOA wallet grants its approvals itself, with its own wallet")
	// ErrNeedsImportedKey means a proxy wallet's approvals are set up with its imported key
	// only: its owner signs in with email, and cannot sign here (D161).
	ErrNeedsImportedKey = errors.New("a proxy wallet's approvals need its imported private key")
	// ErrNotOwner means the browser's account does not control the wallet.
	ErrNotOwner = errors.New("this account does not control the wallet")
	// ErrNotDeployed means the smart wallet is not on chain yet.
	ErrNotDeployed = errors.New("the wallet is not deployed yet: deposit to it on polymarket.com first")
	// ErrSetupPending means the wallet's approvals are being set up already.
	ErrSetupPending = errors.New("the wallet's approvals are being set up: wait for it")
)
