package polymarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.SessionKeyRelayer = (*SessionKeys)(nil)

// SessionKeys serves ports.SessionKeyRelayer: Polymarket's Relayer, with the Builder key
// builder gives while an admin has entered one (D156).
type SessionKeys struct {
	relayer    pm.RelayerClient
	builder    func() (pm.BuilderCredentials, bool)
	derivation pm.Derivation
	now        func() time.Time
}

func NewSessionKeys(relayer pm.RelayerClient, builder func() (pm.BuilderCredentials, bool)) *SessionKeys {
	return &SessionKeys{relayer: relayer, builder: builder, derivation: pm.Production, now: time.Now}
}

func (s *SessionKeys) Available() bool {
	_, ok := s.builder()
	return ok
}

func (s *SessionKeys) NewKey() (string, domain.WalletAddress, error) {
	key, signer, err := pm.NewSessionKey()
	return key, domain.WalletAddress(signer), err
}

func (s *SessionKeys) IsDepositWallet(owner, wallet domain.WalletAddress) bool {
	typ, ok := s.derivation.Classify(common.HexToAddress(string(owner)), common.HexToAddress(string(wallet)))
	return ok && typ == pm.SignatureDepositWallet
}

func (s *SessionKeys) Authorization(ctx context.Context, owner, wallet, signer domain.WalletAddress) (ports.SessionKeyBatch, error) {
	return s.batch(ctx, ports.SessionKeyBatch{Wallet: wallet, Owner: owner, Signer: signer})
}

func (s *SessionKeys) Revocation(ctx context.Context, owner, wallet, signer domain.WalletAddress) (ports.SessionKeyBatch, error) {
	return s.batch(ctx, ports.SessionKeyBatch{Wallet: wallet, Owner: owner, Signer: signer, Revoke: true})
}

func (s *SessionKeys) batch(ctx context.Context, b ports.SessionKeyBatch) (ports.SessionKeyBatch, error) {
	auth, ok := s.builder()
	if !ok {
		return ports.SessionKeyBatch{}, domain.ErrSessionKeysOff
	}

	params, err := s.relayer.ExecuteParams(ctx, auth, string(b.Owner), pm.RelayerDepositWallet)
	if err != nil {
		return ports.SessionKeyBatch{}, relayerError(err)
	}

	now := s.now().UTC().Truncate(time.Second)

	b.Nonce, b.Deadline = params.Nonce, now.Add(pm.BatchDeadline)
	if !b.Revoke {
		b.ValidUntil = now.Add(pm.SessionKeyLifetime)
	}

	return b, nil
}

// toBatch is the batch the owner signs: one call, on the wallet itself.
func toBatch(b ports.SessionKeyBatch) pm.Batch {
	call := pm.AuthorizeSessionSignerCall(string(b.Wallet), string(b.Signer), b.ValidUntil)
	if b.Revoke {
		call = pm.RevokeSessionSignerCall(string(b.Wallet), string(b.Signer))
	}

	return pm.Batch{Wallet: string(b.Wallet), Nonce: b.Nonce, Deadline: b.Deadline, Calls: []pm.DepositWalletCall{call}}
}

func (s *SessionKeys) TypedData(b ports.SessionKeyBatch) (json.RawMessage, error) {
	return toBatch(b).TypedDataJSON(pm.Polygon)
}

func (s *SessionKeys) Signer(b ports.SessionKeyBatch, signature string) (domain.WalletAddress, error) {
	signer, err := toBatch(b).Signer(pm.Polygon, signature)
	if err != nil {
		return "", fmt.Errorf("%w: %w", domain.ErrInvalidInput, err)
	}

	return domain.WalletAddress(signer), nil
}

func (s *SessionKeys) Submit(ctx context.Context, b ports.SessionKeyBatch, signature string) (string, error) {
	auth, ok := s.builder()
	if !ok {
		return "", domain.ErrSessionKeysOff
	}

	req := pm.SessionSignerRequest{
		Wallet: string(b.Wallet), SessionSigner: string(b.Signer),
		Nonce: b.Nonce, Deadline: b.Deadline.Unix(), Signature: signature,
		IdempotencyKey: idempotencyKey(b),
	}

	submit := s.relayer.RevokeSessionSigner
	if !b.Revoke {
		req.Scopes, req.ValidUntil = []string{pm.SessionScopeCLOB}, b.ValidUntil.Unix()
		submit = s.relayer.AuthorizeSessionSigner
	}

	op, err := submit(ctx, auth, req)
	if err != nil {
		return "", relayerError(err)
	}

	return op.TransactionID, nil
}

// idempotencyKey is the same for the same batch, so a batch submitted again, after an
// answer that got lost, is submitted once.
func idempotencyKey(b ports.SessionKeyBatch) string {
	data := string(b.Wallet) + "/" + string(b.Signer) + "/" + b.Nonce + "/" + strconv.FormatBool(b.Revoke)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(data)).String()
}

func (s *SessionKeys) Confirmed(ctx context.Context, transactionID string) (bool, error) {
	auth, ok := s.builder()
	if !ok {
		return false, domain.ErrSessionKeysOff
	}

	return confirmed(ctx, s.relayer, auth, transactionID)
}

// confirmed says whether a Relayer transaction is confirmed, and why it failed.
func confirmed(ctx context.Context, relayer pm.RelayerClient, auth pm.BuilderCredentials, transactionID string) (bool, error) {
	tx, err := relayer.Transaction(ctx, auth, transactionID)
	if err != nil {
		return false, relayerError(err)
	}

	if tx.Failed() {
		reason := tx.State
		if tx.Error != "" {
			reason += ": " + tx.Error
		}

		return false, fmt.Errorf("%w: the transaction failed (%s)", domain.ErrRelayerRefused, reason)
	}

	return tx.Done(), nil
}

// relayerError says what of the Relayer's answers the user can act on: a refusal, saying
// why. Anything else is the Relayer being unavailable.
func relayerError(err error) error {
	var apiErr *pm.APIError

	switch {
	case errors.Is(err, pm.ErrRelayerRefused):
		return fmt.Errorf("%w: %w", domain.ErrRelayerRefused, err)
	case errors.Is(err, pm.ErrUnauthorized):
		return fmt.Errorf("%w: it did not take the Builder key", domain.ErrRelayerRefused)
	case errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 && apiErr.StatusCode != 429:
		return fmt.Errorf("%w: %s", domain.ErrRelayerRefused, apiErr.Body)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	default:
		return fmt.Errorf("%w: %w", domain.ErrUnavailable, err)
	}
}
