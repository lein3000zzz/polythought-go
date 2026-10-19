package wallets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Session keys (D156): for a Deposit Wallet, the server makes a key, the wallet's owner
// authorises it with one signature in the browser, and the Relayer puts that on chain
// with the operator's Builder key. Once confirmed, the key makes its own API key, and both
// become the wallet's credentials: the wallet trades with it (D153). Removing it revokes
// it with one more signature (D159).

const (
	// sessionKeyTimeout is how long a submitted key may take to be confirmed and to make
	// its API key: the Relayer's transactions take minutes, its registries about a minute.
	sessionKeyTimeout = 15 * time.Minute
	// sessionKeyNotice is how long before a key expires its owner is reminded.
	sessionKeyNotice   = 14 * 24 * time.Hour
	maxSessionKeyError = 300
)

// WithSessionKeys offers session keys for Deposit Wallets (D156): kept in repo,
// authorised through relayer, and reminder tells owners when one is about to expire.
// They make their API keys as imported keys do, so they need WithKeys too.
func WithSessionKeys(repo ports.SessionKeyRepository, relayer ports.SessionKeyRelayer, reminder ports.SessionKeyReminder) Option {
	return func(s *Service) { s.sessionKeys, s.relayer, s.reminder = repo, relayer, reminder }
}

// TakesSessionKeys reports whether the instance offers session keys now: once an admin
// has entered a Builder key.
func (s *Service) TakesSessionKeys() bool {
	return s.sessionKeys != nil && s.keys != nil && s.relayer.Available()
}

// SessionKeys are a wallet's session keys: the one it trades with, and the one being
// authorised, which replaces it once confirmed. Either may be missing.
type SessionKeys struct {
	Active  *domain.SessionKey
	Pending *domain.SessionKey
}

func (s *Service) SessionKeys(ctx context.Context, p auth.Principal, id domain.WalletID) (SessionKeys, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return SessionKeys{}, err
	}

	if s.sessionKeys == nil {
		return SessionKeys{}, nil
	}

	keys, err := s.sessionKeys.SessionKeys(ctx, w.ID)
	if err != nil {
		return SessionKeys{}, err
	}

	var out SessionKeys

	for _, k := range keys {
		info := k.Info

		switch {
		case info.State == domain.SessionKeyActive && out.Active == nil:
			out.Active = &info
		case info.State != domain.SessionKeyActive && out.Pending == nil:
			out.Pending = &info
		}
	}

	return out, nil
}

// SessionKeySigning is a batch for the owner's browser wallet to sign, as
// eth_signTypedData_v4 takes it.
type SessionKeySigning struct {
	Key       domain.SessionKey
	TypedData json.RawMessage
}

// StartSessionKey makes a session key for the wallet, which must be the Deposit Wallet of
// owner, the account the browser connected; and the batch authorising it, for the owner
// to sign. A key waiting for a signature, or failed, is replaced; an active one trades
// until the new one is confirmed. From a browser session only, as keys are.
func (s *Service) StartSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID, owner string) (SessionKeySigning, error) {
	w, err := s.sessionKeyWallet(ctx, p, id)
	if err != nil {
		return SessionKeySigning{}, err
	}

	ownerAddr, err := domain.ParseAddress(owner)
	if err != nil {
		return SessionKeySigning{}, err
	}

	if !s.relayer.IsDepositWallet(ownerAddr, w.Address) {
		return SessionKeySigning{}, fmt.Errorf("%w: %s is not the Deposit Wallet of %s; proxy wallets and Safes have no session keys",
			domain.ErrNotDepositWallet, w.Address, ownerAddr)
	}

	keys, err := s.sessionKeys.SessionKeys(ctx, w.ID)
	if err != nil {
		return SessionKeySigning{}, err
	}

	for _, k := range keys {
		if k.Info.State == domain.SessionKeyAuthorizing {
			return SessionKeySigning{}, domain.ErrSessionKeyPending
		}
	}

	key, signer, err := s.relayer.NewKey()
	if err != nil {
		return SessionKeySigning{}, err
	}

	b, err := s.relayer.Authorization(ctx, ownerAddr, w.Address, signer)
	if err != nil {
		return SessionKeySigning{}, err
	}

	typed, err := s.relayer.TypedData(b)
	if err != nil {
		return SessionKeySigning{}, err
	}

	sealedKey, err := s.box.Seal(ctx, []byte(key), sessionKeyAAD(w.ID, signer))
	if err != nil {
		return SessionKeySigning{}, fmt.Errorf("seal session key: %w", err)
	}

	now := s.now()
	k := ports.SealedSessionKey{
		Info: domain.SessionKey{
			WalletID: w.ID, Signer: signer, Owner: ownerAddr, State: domain.SessionKeySigning,
			ValidUntil: b.ValidUntil, CreatedAt: now, UpdatedAt: now,
		},
		Key: sealedKey, Nonce: b.Nonce, Deadline: b.Deadline,
	}

	if err := s.sessionKeys.AddSessionKey(ctx, k); err != nil {
		return SessionKeySigning{}, err
	}

	s.log.Info(ctx, "Session key made", "user_id", p.User.ID, "wallet_id", w.ID, "signer", signer, "owner", ownerAddr)

	return SessionKeySigning{Key: k.Info, TypedData: typed}, nil
}

// SignSessionKey submits the owner's signature of the batch StartSessionKey gave. The key
// is active once the chain confirms it, which ConfirmSessionKeys follows. A refusal fails
// the key; when the Relayer could not be asked, the same signature may be sent again
// until the batch's deadline.
func (s *Service) SignSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID, signature string) (domain.SessionKey, error) {
	w, err := s.sessionKeyWallet(ctx, p, id)
	if err != nil {
		return domain.SessionKey{}, err
	}

	k, err := s.sessionKey(ctx, w.ID, domain.SessionKeySigning)
	if err != nil {
		return domain.SessionKey{}, err
	}

	b := ports.SessionKeyBatch{
		Wallet: w.Address, Owner: k.Info.Owner, Signer: k.Info.Signer,
		ValidUntil: k.Info.ValidUntil, Nonce: k.Nonce, Deadline: k.Deadline,
	}

	signature = strings.TrimSpace(signature)
	if err := s.checkSignature(b, signature); err != nil {
		return domain.SessionKey{}, err
	}

	txID, err := s.relayer.Submit(ctx, b, signature)
	if errors.Is(err, domain.ErrRelayerRefused) {
		s.failSessionKey(ctx, k, err.Error())
		return domain.SessionKey{}, err
	}

	if err != nil {
		return domain.SessionKey{}, err
	}

	k.Info.State, k.TransactionID, k.Info.UpdatedAt = domain.SessionKeyAuthorizing, txID, s.now()
	if err := s.sessionKeys.SaveSessionKey(ctx, k); err != nil {
		return domain.SessionKey{}, err
	}

	s.log.Info(ctx, "Session key submitted", "user_id", p.User.ID, "wallet_id", w.ID, "signer", k.Info.Signer, "transaction_id", txID)

	return k.Info, nil
}

// ConfirmSessionKeys follows the session keys submitted to the Relayer: a key confirmed on
// chain makes its API key and becomes its wallet's credentials; one whose transaction
// failed, or that is not done in time, fails.
func (s *Service) ConfirmSessionKeys(ctx context.Context) {
	if s.sessionKeys == nil {
		return
	}

	keys, err := s.sessionKeys.AuthorizingSessionKeys(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn(ctx, "Session keys not followed", logging.Err(err))
		}

		return
	}

	for _, k := range keys {
		s.confirmSessionKey(ctx, k)
	}
}

func (s *Service) confirmSessionKey(ctx context.Context, k ports.SealedSessionKey) {
	late := s.now().Sub(k.Info.UpdatedAt) > sessionKeyTimeout

	done, err := s.relayer.Confirmed(ctx, k.TransactionID)

	switch {
	case errors.Is(err, domain.ErrRelayerRefused):
		s.failSessionKey(ctx, k, err.Error())
		return
	case err != nil || !done:
		if late {
			s.failSessionKey(ctx, k, "not confirmed in 15 minutes")
		} else if err != nil {
			s.log.Debug(ctx, "Session key not followed yet", "wallet_id", k.Info.WalletID, logging.Err(err))
		}

		return
	}

	err = s.activateSessionKey(ctx, k)

	switch {
	case err == nil, errors.Is(err, domain.ErrNotFound): // removed meanwhile
	case late:
		s.failSessionKey(ctx, k, "authorised, but Polymarket gave it no API key: "+err.Error())
	default:
		// Polymarket's registries may take a minute to know the key.
		s.log.Debug(ctx, "Session key confirmed, its API key not made yet", "wallet_id", k.Info.WalletID, logging.Err(err))
	}
}

// activateSessionKey makes a confirmed key's API key, and makes both the wallet's
// credentials, the key moving there from the session key.
func (s *Service) activateSessionKey(ctx context.Context, k ports.SealedSessionKey) error {
	key, err := s.box.Open(ctx, k.Key, sessionKeyAAD(k.Info.WalletID, k.Info.Signer))
	if err != nil {
		return fmt.Errorf("open session key: %w", err)
	}

	creds, err := s.keys.APICredentials(ctx, string(key))
	if err != nil {
		return err
	}

	creds.Signer, creds.SignatureType, creds.SessionKey = k.Info.Signer, domain.SignatureDepositWallet, true

	sealed, err := s.seal(ctx, k.Info.WalletID, creds, string(key))
	if err != nil {
		return err
	}

	k.Info.State, k.Info.Error, k.Info.UpdatedAt, k.Key = domain.SessionKeyActive, "", s.now(), nil
	if err := s.sessionKeys.ActivateSessionKey(ctx, k, sealed); err != nil {
		return err
	}

	s.log.Info(ctx, "Session key active", "wallet_id", k.Info.WalletID, "signer", k.Info.Signer, "valid_until", k.Info.ValidUntil)

	return nil
}

func (s *Service) failSessionKey(ctx context.Context, k ports.SealedSessionKey, reason string) {
	k.Info.State, k.Info.Error, k.Info.UpdatedAt = domain.SessionKeyFailed, truncate(reason, maxSessionKeyError), s.now()

	if err := s.sessionKeys.SaveSessionKey(ctx, k); err != nil && !errors.Is(err, domain.ErrNotFound) {
		s.log.Warn(ctx, "Failed session key not saved", "wallet_id", k.Info.WalletID, logging.Err(err))
		return
	}

	s.log.Info(ctx, "Session key failed", "wallet_id", k.Info.WalletID, "signer", k.Info.Signer, "reason", reason)
}

// StartRevocation gives the batch revoking the wallet's active session key, for its owner
// to sign (D159).
func (s *Service) StartRevocation(ctx context.Context, p auth.Principal, id domain.WalletID) (SessionKeySigning, error) {
	w, err := s.sessionKeyWallet(ctx, p, id)
	if err != nil {
		return SessionKeySigning{}, err
	}

	k, err := s.sessionKey(ctx, w.ID, domain.SessionKeyActive)
	if err != nil {
		return SessionKeySigning{}, err
	}

	b, err := s.relayer.Revocation(ctx, k.Info.Owner, w.Address, k.Info.Signer)
	if err != nil {
		return SessionKeySigning{}, err
	}

	typed, err := s.relayer.TypedData(b)
	if err != nil {
		return SessionKeySigning{}, err
	}

	k.Nonce, k.Deadline, k.Info.UpdatedAt = b.Nonce, b.Deadline, s.now()
	if err := s.sessionKeys.SaveSessionKey(ctx, k); err != nil {
		return SessionKeySigning{}, err
	}

	return SessionKeySigning{Key: k.Info, TypedData: typed}, nil
}

// Revocation is how removing a session key with a signature went: the key is removed
// either way; Revoked unless the Relayer did not take it, Problem saying why.
type Revocation struct {
	Revoked bool
	Problem string
}

// RevokeSessionKey submits the owner's signature of the batch StartRevocation gave, and
// removes the wallet's session keys, and the credentials the active one made, whatever
// the Relayer answers (D159). A signature that is not the owner's, or late, removes
// nothing.
func (s *Service) RevokeSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID, signature string) (Revocation, error) {
	w, err := s.sessionKeyWallet(ctx, p, id)
	if err != nil {
		return Revocation{}, err
	}

	k, err := s.sessionKey(ctx, w.ID, domain.SessionKeyActive)
	if err != nil {
		return Revocation{}, err
	}

	b := ports.SessionKeyBatch{
		Wallet: w.Address, Owner: k.Info.Owner, Signer: k.Info.Signer, Revoke: true, Nonce: k.Nonce, Deadline: k.Deadline,
	}

	signature = strings.TrimSpace(signature)
	if err := s.checkSignature(b, signature); err != nil {
		return Revocation{}, err
	}

	out := Revocation{Revoked: true}

	if _, err := s.relayer.Submit(ctx, b, signature); err != nil {
		if ctx.Err() != nil {
			return Revocation{}, err
		}

		out = Revocation{Problem: err.Error()}
		s.log.Warn(ctx, "Session key not revoked", "wallet_id", w.ID, "signer", k.Info.Signer, logging.Err(err))
	}

	if err := s.sessionKeys.DeleteSessionKeys(ctx, w.ID); err != nil {
		return Revocation{}, err
	}

	s.log.Info(ctx, "Session key removed", "user_id", p.User.ID, "wallet_id", w.ID, "signer", k.Info.Signer, "revoked", out.Revoked)

	return out, nil
}

// RemoveSessionKey removes the wallet's session keys, and the credentials the active one
// made, without revoking it: when its owner did not sign the revocation (D159). With the
// key gone from here, nobody can use it; it expires on chain.
func (s *Service) RemoveSessionKey(ctx context.Context, p auth.Principal, id domain.WalletID) error {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return err
	}

	if s.sessionKeys == nil {
		return domain.ErrNotFound
	}

	if err := s.sessionKeys.DeleteSessionKeys(ctx, w.ID); err != nil {
		return err
	}

	s.log.Info(ctx, "Session key removed", "user_id", p.User.ID, "wallet_id", w.ID, "revoked", false)

	return nil
}

// RemindSessionKeys tells owners of session keys that expire within 14 days, once a key.
func (s *Service) RemindSessionKeys(ctx context.Context) {
	if s.sessionKeys == nil || s.reminder == nil {
		return
	}

	keys, err := s.sessionKeys.ExpiringSessionKeys(ctx, s.now().Add(sessionKeyNotice))
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn(ctx, "Expiring session keys not read", logging.Err(err))
		}

		return
	}

	for _, e := range keys {
		if err := s.reminder.RemindSessionKey(ctx, e.Wallet.UserID, e.Wallet, e.Key.ValidUntil); err != nil {
			s.log.Warn(ctx, "Session key reminder not sent", "wallet_id", e.Wallet.ID, logging.Err(err))
			continue
		}

		if err := s.sessionKeys.MarkReminded(ctx, e.Wallet.ID, e.Key.Signer, s.now()); err != nil && !errors.Is(err, domain.ErrNotFound) {
			s.log.Warn(ctx, "Session key reminder not recorded", "wallet_id", e.Wallet.ID, logging.Err(err))
		}
	}
}

// FollowEvery follows what was submitted to the Relayer, session keys and approval setups,
// every follow, and reminds of expiring session keys every remind, until ctx is done.
func (s *Service) FollowEvery(ctx context.Context, follow, remind time.Duration) {
	if s.sessionKeys == nil && s.setups == nil {
		return
	}

	f, r := time.NewTicker(follow), time.NewTicker(remind)
	defer f.Stop()
	defer r.Stop()

	s.RemindSessionKeys(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-f.C:
			s.ConfirmSessionKeys(ctx)
			s.ConfirmApprovalSetups(ctx)
		case <-r.C:
			s.RemindSessionKeys(ctx)
		}
	}
}

// sessionKeyWallet is the wallet for a session key's authorisation or revocation: from a
// browser session, while the instance offers session keys.
func (s *Service) sessionKeyWallet(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.Wallet, error) {
	if !p.ViaSession() {
		return domain.Wallet{}, domain.ErrForbidden
	}

	if !s.TakesSessionKeys() {
		return domain.Wallet{}, domain.ErrSessionKeysOff
	}

	return s.repo.Get(ctx, p.User.ID, id)
}

// sessionKey is the wallet's newest key in state.
func (s *Service) sessionKey(ctx context.Context, wallet domain.WalletID, state domain.SessionKeyState) (ports.SealedSessionKey, error) {
	keys, err := s.sessionKeys.SessionKeys(ctx, wallet)
	if err != nil {
		return ports.SealedSessionKey{}, err
	}

	for _, k := range keys {
		if k.Info.State == state {
			return k, nil
		}
	}

	if state == domain.SessionKeyActive {
		return ports.SealedSessionKey{}, fmt.Errorf("%w: the wallet has no active session key", domain.ErrNotFound)
	}

	return ports.SealedSessionKey{}, fmt.Errorf("%w: no session key waits for a signature: start again", domain.ErrNotFound)
}

// checkSignature checks that the batch's owner signed it, before its deadline.
func (s *Service) checkSignature(b ports.SessionKeyBatch, signature string) error {
	if s.now().After(b.Deadline) {
		return domain.ErrSignatureExpired
	}

	signer, err := s.relayer.Signer(b, signature)
	if err != nil {
		return err
	}

	if !strings.EqualFold(string(signer), string(b.Owner)) {
		return fmt.Errorf("%w: it is %s's, and the wallet's owner is %s", domain.ErrWrongSigner, signer, b.Owner)
	}

	return nil
}

// sessionKeyAAD binds a sealed session key to its wallet and address.
func sessionKeyAAD(wallet domain.WalletID, signer domain.WalletAddress) []byte {
	return []byte("session-keys/" + string(wallet) + "/" + string(signer))
}

func truncate(s string, most int) string {
	if utf8.RuneCountInString(s) <= most {
		return s
	}

	return string([]rune(s)[:most-1]) + "…"
}
