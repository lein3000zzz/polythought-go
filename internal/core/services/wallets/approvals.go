package wallets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Trading approvals (D160 to D163): what a wallet lets Polymarket's contracts move, read
// from Polymarket's index for the card, checked on chain before anything is granted, and
// granted through the Relayer for smart wallets: signed by the server with an imported
// key, or in the owner's browser wallet. EOAs grant theirs themselves.

// WithApprovals reads and sets up wallets' trading approvals with approvals, keeping each
// wallet's last setup in repo.
func WithApprovals(repo ports.ApprovalSetupRepository, approvals ports.TradingApprovals) Option {
	return func(s *Service) { s.setups, s.approvals = repo, approvals }
}

// SetUpWay is how this instance can set up a wallet's approvals.
type SetUpWay string

const (
	SetUpNot     SetUpWay = ""        // no Builder key, or an EOA or proxy wallet without a key
	SetUpKey     SetUpWay = "key"     // signed with its imported private key
	SetUpBrowser SetUpWay = "browser" // signed in its owner's browser wallet
)

// ApprovalStatus is a wallet's trading approvals: those Polymarket's index shows missing,
// when it looked, the last setup, and how one can be done; Why not, when it cannot.
type ApprovalStatus struct {
	Missing   []domain.Approval
	CheckedAt time.Time
	Setup     *domain.ApprovalSetup
	Way       SetUpWay
	Why       string
}

func (s *Service) Approvals(ctx context.Context, p auth.Principal, id domain.WalletID) (ApprovalStatus, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return ApprovalStatus{}, err
	}

	if s.approvals == nil {
		return ApprovalStatus{}, domain.ErrNotFound
	}

	missing, checked, err := s.approvals.Status(ctx, w.Address)
	if err != nil {
		return ApprovalStatus{}, err
	}

	out := ApprovalStatus{Missing: missing, CheckedAt: checked}

	if st, err := s.setups.ApprovalSetup(ctx, w.ID); err == nil {
		out.Setup = &st.Info
	} else if !errors.Is(err, domain.ErrNotFound) {
		return ApprovalStatus{}, err
	}

	way, why, err := s.setUpWay(ctx, w)
	if err != nil {
		return ApprovalStatus{}, err
	}

	out.Way, out.Why = way, why

	return out, nil
}

// setUpWay is how w's approvals can be set up: with its imported key when it has one, in
// the browser otherwise; not for EOAs, nor proxy wallets without a key (D161, D162), and
// why not.
func (s *Service) setUpWay(ctx context.Context, w domain.Wallet) (way SetUpWay, why string, err error) {
	if !s.approvals.Available() {
		return SetUpNot, domain.ErrNoBuilderKey.Error(), nil
	}

	if !w.HasCredentials {
		return SetUpBrowser, "", nil
	}

	c, err := s.repo.Credentials(ctx, w.ID)
	if err != nil {
		return SetUpNot, "", err
	}

	switch {
	case c.Info.SignatureType == domain.SignatureEOA:
		return SetUpNot, domain.ErrEOAApprovals.Error(), nil
	case w.HasKey && !w.HasSessionKey:
		return SetUpKey, "", nil
	case c.Info.SignatureType == domain.SignaturePolyProxy:
		return SetUpNot, domain.ErrNeedsImportedKey.Error(), nil
	}

	return SetUpBrowser, "", nil
}

// SetUpApprovals grants the wallet's missing approvals, signed by the server with the
// wallet's imported key (D161). From a browser session only, as keys are. Granting is
// done once the chain confirms it, which ConfirmApprovalSetups follows; with nothing
// missing, nothing is submitted, and the setup grants none.
func (s *Service) SetUpApprovals(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.ApprovalSetup, error) {
	w, err := s.setupWallet(ctx, p, id)
	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	if !w.HasKey || w.HasSessionKey {
		return domain.ApprovalSetup{}, fmt.Errorf("%w: the wallet has no imported private key", domain.ErrNotFound)
	}

	c, err := s.repo.Credentials(ctx, w.ID)
	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	kind := c.Info.SignatureType
	if kind == domain.SignatureEOA {
		return domain.ApprovalSetup{}, domain.ErrEOAApprovals
	}

	b, done, err := s.prepareSetup(ctx, w, kind, c.Info.Signer)
	if err != nil || done != nil {
		return deref(done), err
	}

	key, err := s.box.Open(ctx, c.PrivateKey, aad(w.ID, "private_key"))
	if err != nil {
		return domain.ApprovalSetup{}, fmt.Errorf("open private key: %w", err)
	}

	signature, err := s.approvals.Sign(b, string(key))
	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	st := s.newSetup(w, b, domain.ApprovalSetupSigning)

	return s.submitSetup(ctx, p, st, b, signature)
}

// ApprovalSigning is a setup waiting for the owner's browser wallet to sign Request; none
// when nothing is missing.
type ApprovalSigning struct {
	Setup   domain.ApprovalSetup
	Request *ports.SigningRequest
}

// StartApprovalSetup gives the transaction granting the wallet's missing approvals, for
// owner, the browser's account, to sign: as typed data for a Deposit Wallet, as a message
// for a Safe (D161). From a browser session only.
func (s *Service) StartApprovalSetup(ctx context.Context, p auth.Principal, id domain.WalletID, owner string) (ApprovalSigning, error) {
	w, err := s.setupWallet(ctx, p, id)
	if err != nil {
		return ApprovalSigning{}, err
	}

	ownerAddr, err := domain.ParseAddress(owner)
	if err != nil {
		return ApprovalSigning{}, err
	}

	kind, ok := s.approvals.Kind(ownerAddr, w.Address)

	switch {
	case !ok:
		return ApprovalSigning{}, fmt.Errorf("%w: %s is not %s, nor its proxy wallet, Safe or Deposit Wallet",
			domain.ErrNotOwner, ownerAddr, w.Address)
	case kind == domain.SignatureEOA:
		return ApprovalSigning{}, domain.ErrEOAApprovals
	case kind == domain.SignaturePolyProxy:
		return ApprovalSigning{}, domain.ErrNeedsImportedKey
	}

	b, done, err := s.prepareSetup(ctx, w, kind, ownerAddr)
	if err != nil || done != nil {
		return ApprovalSigning{Setup: deref(done)}, err
	}

	req, err := s.approvals.SigningRequest(b)
	if err != nil {
		return ApprovalSigning{}, err
	}

	st := s.newSetup(w, b, domain.ApprovalSetupSigning)
	if err := s.setups.SaveApprovalSetup(ctx, st); err != nil {
		return ApprovalSigning{}, err
	}

	return ApprovalSigning{Setup: st.Info, Request: &req}, nil
}

// SignApprovalSetup submits the owner's signature of what StartApprovalSetup gave.
func (s *Service) SignApprovalSetup(ctx context.Context, p auth.Principal, id domain.WalletID, signature string) (domain.ApprovalSetup, error) {
	w, err := s.setupWallet(ctx, p, id)
	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	st, err := s.setups.ApprovalSetup(ctx, w.ID)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && st.Info.State != domain.ApprovalSetupSigning) {
		return domain.ApprovalSetup{}, fmt.Errorf("%w: no setup waits for a signature: start again", domain.ErrNotFound)
	}

	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	b := ports.ApprovalBatch{
		Kind: st.Info.Kind, Wallet: w.Address, Owner: st.Info.Owner, Approvals: st.Approvals,
		Nonce: st.Nonce, Deadline: st.Deadline,
	}

	if !b.Deadline.IsZero() && s.now().After(b.Deadline) {
		return domain.ApprovalSetup{}, domain.ErrSignatureExpired
	}

	signature = strings.TrimSpace(signature)

	signer, err := s.approvals.Signer(b, signature)
	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	if !strings.EqualFold(string(signer), string(b.Owner)) {
		return domain.ApprovalSetup{}, fmt.Errorf("%w: it is %s's, and the wallet's owner is %s", domain.ErrWrongSigner, signer, b.Owner)
	}

	return s.submitSetup(ctx, p, st, b, signature)
}

// prepareSetup is the transaction granting w's missing approvals, read on chain (D163);
// done instead when nothing is missing.
func (s *Service) prepareSetup(ctx context.Context, w domain.Wallet, kind domain.SignatureType, owner domain.WalletAddress) (ports.ApprovalBatch, *domain.ApprovalSetup, error) {
	if st, err := s.setups.ApprovalSetup(ctx, w.ID); err == nil && st.Info.State == domain.ApprovalSetupSubmitted {
		return ports.ApprovalBatch{}, nil, domain.ErrSetupPending
	} else if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return ports.ApprovalBatch{}, nil, err
	}

	missing, err := s.approvals.Missing(ctx, w.Address)
	if err != nil {
		return ports.ApprovalBatch{}, nil, err
	}

	if len(missing) == 0 {
		now := s.now()
		return ports.ApprovalBatch{}, &domain.ApprovalSetup{
			WalletID: w.ID, Owner: owner, Kind: kind, State: domain.ApprovalSetupDone, CreatedAt: now, UpdatedAt: now,
		}, nil
	}

	deployed, err := s.approvals.Deployed(ctx, w.Address, kind)
	if err != nil {
		return ports.ApprovalBatch{}, nil, err
	}

	if !deployed {
		return ports.ApprovalBatch{}, nil, domain.ErrNotDeployed
	}

	b, err := s.approvals.Prepare(ctx, kind, owner, w.Address, missing)

	return b, nil, err
}

func (s *Service) newSetup(w domain.Wallet, b ports.ApprovalBatch, state domain.ApprovalSetupState) ports.StoredApprovalSetup {
	now := s.now()

	return ports.StoredApprovalSetup{
		Info: domain.ApprovalSetup{
			WalletID: w.ID, Owner: b.Owner, Kind: b.Kind, Granting: len(b.Approvals), State: state,
			CreatedAt: now, UpdatedAt: now,
		},
		Approvals: b.Approvals, Nonce: b.Nonce, Deadline: b.Deadline,
	}
}

// submitSetup submits a signed setup. A refusal fails it; when the Relayer could not be
// asked, nothing changes.
func (s *Service) submitSetup(ctx context.Context, p auth.Principal, st ports.StoredApprovalSetup, b ports.ApprovalBatch, signature string) (domain.ApprovalSetup, error) {
	txID, err := s.approvals.Submit(ctx, b, signature)
	if errors.Is(err, domain.ErrRelayerRefused) {
		s.failSetup(ctx, st, err.Error())
		return domain.ApprovalSetup{}, err
	}

	if err != nil {
		return domain.ApprovalSetup{}, err
	}

	st.Info.State, st.TransactionID, st.Info.UpdatedAt = domain.ApprovalSetupSubmitted, txID, s.now()
	if err := s.setups.SaveApprovalSetup(ctx, st); err != nil {
		return domain.ApprovalSetup{}, err
	}

	s.log.Info(ctx, "Approvals submitted", "user_id", p.User.ID, "wallet_id", st.Info.WalletID,
		"kind", st.Info.Kind, "granting", st.Info.Granting, "transaction_id", txID)

	return st.Info, nil
}

func (s *Service) failSetup(ctx context.Context, st ports.StoredApprovalSetup, reason string) {
	st.Info.State, st.Info.Error, st.Info.UpdatedAt = domain.ApprovalSetupFailed, truncate(reason, maxSessionKeyError), s.now()

	if err := s.setups.SaveApprovalSetup(ctx, st); err != nil {
		s.log.Warn(ctx, "Failed approval setup not saved", "wallet_id", st.Info.WalletID, logging.Err(err))
		return
	}

	s.log.Info(ctx, "Approval setup failed", "wallet_id", st.Info.WalletID, "reason", reason)
}

// ConfirmApprovalSetups follows the setups submitted to the Relayer: done once the chain
// confirms them, failed when their transaction fails or is not confirmed in time.
func (s *Service) ConfirmApprovalSetups(ctx context.Context) {
	if s.setups == nil {
		return
	}

	setups, err := s.setups.SubmittedApprovalSetups(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn(ctx, "Approval setups not followed", logging.Err(err))
		}

		return
	}

	for _, st := range setups {
		done, err := s.approvals.Confirmed(ctx, st.TransactionID)

		switch {
		case errors.Is(err, domain.ErrRelayerRefused):
			s.failSetup(ctx, st, err.Error())
		case err == nil && done:
			st.Info.State, st.Info.UpdatedAt = domain.ApprovalSetupDone, s.now()
			if err := s.setups.SaveApprovalSetup(ctx, st); err != nil {
				s.log.Warn(ctx, "Approval setup not saved", "wallet_id", st.Info.WalletID, logging.Err(err))
				continue
			}

			s.log.Info(ctx, "Approvals granted", "wallet_id", st.Info.WalletID, "granting", st.Info.Granting)
		case s.now().Sub(st.Info.UpdatedAt) > sessionKeyTimeout:
			s.failSetup(ctx, st, "not confirmed in 15 minutes")
		case err != nil:
			s.log.Debug(ctx, "Approval setup not followed yet", "wallet_id", st.Info.WalletID, logging.Err(err))
		}
	}
}

// setupWallet is the wallet for setting up approvals: from a browser session, while the
// instance has a Builder key.
func (s *Service) setupWallet(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.Wallet, error) {
	if !p.ViaSession() {
		return domain.Wallet{}, domain.ErrForbidden
	}

	if s.approvals == nil || !s.approvals.Available() {
		return domain.Wallet{}, domain.ErrNoBuilderKey
	}

	return s.repo.Get(ctx, p.User.ID, id)
}

func deref(st *domain.ApprovalSetup) domain.ApprovalSetup {
	if st == nil {
		return domain.ApprovalSetup{}
	}

	return *st
}
