// Package wallets links Polymarket accounts to users and keeps their L2 API credentials
// encrypted at rest.
package wallets

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

const (
	maxLabel          = 64
	maxSecretField    = 256
	defaultMaxWallets = 20
)

var _ ports.WalletDirectory = (*Service)(nil)

type Service struct {
	repo      ports.WalletRepository
	box       ports.SecretBox
	verifier  ports.CredentialsVerifier
	keys      ports.TradingKeys // optional: without it, no key is taken
	keyImport func() bool       // whether the instance takes keys now (D21)

	// Optional: without them, no session key is offered (D156).
	sessionKeys ports.SessionKeyRepository
	relayer     ports.SessionKeyRelayer
	reminder    ports.SessionKeyReminder

	// Optional: without them, no approvals are read or set up (D160).
	setups    ports.ApprovalSetupRepository
	approvals ports.TradingApprovals

	// Optional: without it, a wallet is unlinked whatever trades from it.
	trading ports.WorkflowTradingRepository

	log        logging.Logger
	now        func() time.Time
	maxWallets int
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithKeys takes private keys while allowed says so (D21), reading them with keys.
func WithKeys(keys ports.TradingKeys, allowed func() bool) Option {
	return func(s *Service) { s.keys, s.keyImport = keys, allowed }
}

// TakesKeys reports whether the instance takes private keys now.
func (s *Service) TakesKeys() bool {
	return s.keys != nil && s.keyImport != nil && s.keyImport()
}

func New(repo ports.WalletRepository, box ports.SecretBox, verifier ports.CredentialsVerifier, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		repo:       repo,
		box:        box,
		verifier:   verifier,
		log:        log.With("component", "wallets"),
		now:        func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		maxWallets: defaultMaxWallets,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Link adds a Polymarket account by its address. No secret is involved: positions and
// activity of any address are public.
func (s *Service) Link(ctx context.Context, p auth.Principal, address, label string) (domain.Wallet, error) {
	addr, err := domain.ParseAddress(address)
	if err != nil {
		return domain.Wallet{}, err
	}

	label = strings.TrimSpace(label)
	if len(label) > maxLabel {
		return domain.Wallet{}, fmt.Errorf("%w: label is longer than %d characters", domain.ErrInvalidInput, maxLabel)
	}

	n, err := s.repo.CountForUser(ctx, p.User.ID)
	if err != nil {
		return domain.Wallet{}, err
	}

	if n >= s.maxWallets {
		return domain.Wallet{}, fmt.Errorf("%w: at most %d wallets per user", domain.ErrInvalidInput, s.maxWallets)
	}

	w := domain.Wallet{
		ID:        domain.WalletID(uuid.Must(uuid.NewV7()).String()),
		UserID:    p.User.ID,
		Address:   addr,
		Label:     label,
		CreatedAt: s.now(),
	}

	if err := s.repo.Create(ctx, w); err != nil {
		return domain.Wallet{}, err
	}

	s.log.Info(ctx, "Wallet linked", "user_id", p.User.ID, "wallet_id", w.ID, "address", addr)

	return w, nil
}

func (s *Service) List(ctx context.Context, p auth.Principal) ([]domain.Wallet, error) {
	return s.repo.ListForUser(ctx, p.User.ID)
}

// Unlink removes a wallet together with its credentials, unless a workflow trades live
// from it.
func (s *Service) Unlink(ctx context.Context, p auth.Principal, id domain.WalletID) error {
	if _, err := s.repo.Get(ctx, p.User.ID, id); err != nil {
		return err
	}

	if err := s.tradesLive(ctx, id); err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, p.User.ID, id); err != nil {
		return err
	}

	s.log.Info(ctx, "Wallet unlinked", "user_id", p.User.ID, "wallet_id", id)

	return nil
}

type CredentialsInput struct {
	APIKey     string
	Secret     string
	Passphrase string

	// Signer defaults to the wallet address, which is right for EOA and deposit-wallet
	// accounts. Proxy and safe accounts must give the EOA that owns the API key.
	Signer        string
	SignatureType domain.SignatureType
}

// SetCredentials checks a wallet's L2 API credentials with Polymarket, then stores them,
// replacing any previous ones. Like API tokens, this is only allowed from a browser session.
// Credentials Polymarket refuses, or cannot be asked about, are not stored.
func (s *Service) SetCredentials(ctx context.Context, p auth.Principal, id domain.WalletID, in CredentialsInput) (domain.CredentialsInfo, error) {
	if !p.ViaSession() {
		return domain.CredentialsInfo{}, domain.ErrForbidden
	}

	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	if w.HasSessionKey {
		return domain.CredentialsInfo{}, domain.ErrSessionKeyInUse
	}

	creds, err := validateCredentials(w, in)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	if err := s.verifier.VerifyCredentials(ctx, creds); err != nil {
		s.log.Info(ctx, "Polymarket credentials not accepted", "user_id", p.User.ID, "wallet_id", w.ID, logging.Err(err))
		return domain.CredentialsInfo{}, err
	}

	info, err := s.save(ctx, w, creds, "") // pasted: no key, a key kept before goes
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	s.log.Info(ctx, "Polymarket credentials saved", "user_id", p.User.ID, "wallet_id", w.ID, "signature_type", creds.SignatureType)

	return info, nil
}

// save seals and keeps a wallet's credentials, and its private key when there is one,
// replacing what it had.
func (s *Service) save(ctx context.Context, w domain.Wallet, creds domain.PolymarketCredentials, key string) (domain.CredentialsInfo, error) {
	sealed, err := s.seal(ctx, w.ID, creds, key)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	if err := s.repo.SaveCredentials(ctx, sealed); err != nil {
		return domain.CredentialsInfo{}, err
	}

	return sealed.Info, nil
}

// seal seals a wallet's credentials, and its private key when there is one, to replace
// what it had.
func (s *Service) seal(ctx context.Context, id domain.WalletID, creds domain.PolymarketCredentials, key string) (ports.SealedCredentials, error) {
	now := s.now()
	created := now

	if old, err := s.repo.Credentials(ctx, id); err == nil {
		created = old.Info.CreatedAt
	}

	sealed := ports.SealedCredentials{
		Info: domain.CredentialsInfo{
			WalletID:      id,
			APIKeyHint:    hint(creds.APIKey),
			Signer:        creds.Signer,
			SignatureType: creds.SignatureType,
			HasKey:        key != "",
			SessionKey:    creds.SessionKey,
			CreatedAt:     created,
			UpdatedAt:     now,
		},
	}

	fields := []struct {
		name  string
		value string
		dst   *[]byte
	}{
		{"api_key", creds.APIKey, &sealed.APIKey},
		{"secret", creds.Secret, &sealed.Secret},
		{"passphrase", creds.Passphrase, &sealed.Passphrase},
	}

	if key != "" {
		fields = append(fields, struct {
			name  string
			value string
			dst   *[]byte
		}{"private_key", key, &sealed.PrivateKey})
	}

	var err error

	for _, f := range fields {
		*f.dst, err = s.box.Seal(ctx, []byte(f.value), aad(id, f.name))
		if err != nil {
			return ports.SealedCredentials{}, fmt.Errorf("seal %s: %w", f.name, err)
		}
	}

	return sealed, nil
}

// ImportKey keeps a wallet's private key, so it can trade (D153): only while the instance
// takes keys (D21), from a browser session, and only a key that controls the wallet
// (D155). Its API credentials are made from it (D154), replacing any pasted ones. The key
// is never shown again.
func (s *Service) ImportKey(ctx context.Context, p auth.Principal, id domain.WalletID, key string) (domain.CredentialsInfo, error) {
	if !p.ViaSession() {
		return domain.CredentialsInfo{}, domain.ErrForbidden
	}

	if !s.TakesKeys() {
		return domain.CredentialsInfo{}, domain.ErrKeyImportOff
	}

	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	if w.HasSessionKey {
		return domain.CredentialsInfo{}, domain.ErrSessionKeyInUse
	}

	key = strings.TrimSpace(key)

	signer, typ, err := s.keys.Account(key, w.Address)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	creds, err := s.keys.APICredentials(ctx, key)
	if err != nil {
		s.log.Info(ctx, "Polymarket API key not made", "user_id", p.User.ID, "wallet_id", w.ID, logging.Err(err))
		return domain.CredentialsInfo{}, err
	}

	creds.Signer, creds.SignatureType = signer, typ

	info, err := s.save(ctx, w, creds, key)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	s.log.Info(ctx, "Polymarket key imported", "user_id", p.User.ID, "wallet_id", w.ID, "signer", signer, "signature_type", typ)

	return info, nil
}

// CredentialsInfo describes a wallet's stored credentials without decrypting them.
func (s *Service) CredentialsInfo(ctx context.Context, p auth.Principal, id domain.WalletID) (domain.CredentialsInfo, error) {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	c, err := s.repo.Credentials(ctx, w.ID)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	return c.Info, nil
}

// RemoveCredentials removes a wallet's credentials, and its key; not a session key's,
// which removing the session key does (D159).
func (s *Service) RemoveCredentials(ctx context.Context, p auth.Principal, id domain.WalletID) error {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return err
	}

	if w.HasSessionKey {
		return domain.ErrSessionKeyInUse
	}

	if err := s.repo.DeleteCredentials(ctx, w.ID); err != nil {
		return err
	}

	s.log.Info(ctx, "Polymarket credentials removed", "user_id", p.User.ID, "wallet_id", w.ID)

	return nil
}

// Wallet looks up one of owner's wallets, for the engine.
func (s *Service) Wallet(ctx context.Context, owner domain.UserID, id domain.WalletID) (domain.Wallet, error) {
	return s.repo.Get(ctx, owner, id)
}

// Credentials decrypts a wallet's credentials for signing requests on the owner's
// behalf. It is for the engine, never for API responses.
func (s *Service) Credentials(ctx context.Context, owner domain.UserID, id domain.WalletID) (domain.PolymarketCredentials, error) {
	w, err := s.repo.Get(ctx, owner, id)
	if err != nil {
		return domain.PolymarketCredentials{}, err
	}

	c, err := s.repo.Credentials(ctx, w.ID)
	if err != nil {
		return domain.PolymarketCredentials{}, err
	}

	out := domain.PolymarketCredentials{Signer: c.Info.Signer, SignatureType: c.Info.SignatureType, SessionKey: c.Info.SessionKey}

	for _, f := range []struct {
		name   string
		sealed []byte
		dst    *string
	}{
		{"api_key", c.APIKey, &out.APIKey},
		{"secret", c.Secret, &out.Secret},
		{"passphrase", c.Passphrase, &out.Passphrase},
	} {
		plain, err := s.box.Open(ctx, f.sealed, aad(w.ID, f.name))
		if err != nil {
			return domain.PolymarketCredentials{}, fmt.Errorf("open %s: %w", f.name, err)
		}

		*f.dst = string(plain)
	}

	return out, nil
}

// aad binds each sealed field to its wallet and name, so a ciphertext copied to another
// wallet or swapped with another field fails to decrypt.
func aad(id domain.WalletID, field string) []byte {
	return []byte("polymarket-credentials/" + string(id) + "/" + field)
}

func validateCredentials(w domain.Wallet, in CredentialsInput) (domain.PolymarketCredentials, error) {
	invalid := func(format string, args ...any) (domain.PolymarketCredentials, error) {
		return domain.PolymarketCredentials{}, fmt.Errorf("%w: "+format, append([]any{domain.ErrInvalidInput}, args...)...)
	}

	apiKey := strings.TrimSpace(in.APIKey)
	if _, err := uuid.Parse(apiKey); err != nil {
		return invalid("api key must be a UUID")
	}

	secret := strings.TrimSpace(in.Secret)
	if secret == "" || len(secret) > maxSecretField {
		return invalid("secret is required")
	}

	// The CLOB signs with base64url-decoded secrets; reject what could never work.
	if _, err := base64.URLEncoding.DecodeString(secret); err != nil {
		if _, err := base64.RawURLEncoding.DecodeString(secret); err != nil {
			return invalid("secret must be base64url")
		}
	}

	passphrase := strings.TrimSpace(in.Passphrase)
	if passphrase == "" || len(passphrase) > maxSecretField {
		return invalid("passphrase is required")
	}

	if !in.SignatureType.Valid() {
		return invalid("signature type must be 0 to 3")
	}

	signer := w.Address
	if strings.TrimSpace(in.Signer) != "" {
		addr, err := domain.ParseAddress(in.Signer)
		if err != nil {
			return domain.PolymarketCredentials{}, err
		}

		signer = addr
	}

	return domain.PolymarketCredentials{
		APIKey:        apiKey,
		Secret:        secret,
		Passphrase:    passphrase,
		Signer:        signer,
		SignatureType: in.SignatureType,
	}, nil
}

// hint keeps the first and last four characters of an API key for display.
func hint(apiKey string) string {
	if len(apiKey) <= 8 {
		return "…"
	}

	return apiKey[:4] + "…" + apiKey[len(apiKey)-4:]
}
