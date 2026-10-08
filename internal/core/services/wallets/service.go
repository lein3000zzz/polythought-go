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

type Service struct {
	repo       ports.WalletRepository
	box        ports.SecretBox
	log        logging.Logger
	now        func() time.Time
	maxWallets int
}

type Option func(*Service)

// WithClock replaces the clock, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(repo ports.WalletRepository, box ports.SecretBox, log logging.Logger, opts ...Option) *Service {
	s := &Service{
		repo:       repo,
		box:        box,
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

// Unlink removes a wallet together with its credentials.
func (s *Service) Unlink(ctx context.Context, p auth.Principal, id domain.WalletID) error {
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

// SetCredentials stores a wallet's L2 API credentials, replacing any previous ones.
// Like API tokens, this is only allowed from a browser session.
func (s *Service) SetCredentials(ctx context.Context, p auth.Principal, id domain.WalletID, in CredentialsInput) (domain.CredentialsInfo, error) {
	if !p.ViaSession() {
		return domain.CredentialsInfo{}, domain.ErrForbidden
	}

	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	creds, err := validateCredentials(w, in)
	if err != nil {
		return domain.CredentialsInfo{}, err
	}

	now := s.now()
	created := now

	if old, err := s.repo.Credentials(ctx, w.ID); err == nil {
		created = old.Info.CreatedAt
	}

	sealed := ports.SealedCredentials{
		Info: domain.CredentialsInfo{
			WalletID:      w.ID,
			APIKeyHint:    hint(creds.APIKey),
			Signer:        creds.Signer,
			SignatureType: creds.SignatureType,
			CreatedAt:     created,
			UpdatedAt:     now,
		},
	}

	for _, f := range []struct {
		name  string
		value string
		dst   *[]byte
	}{
		{"api_key", creds.APIKey, &sealed.APIKey},
		{"secret", creds.Secret, &sealed.Secret},
		{"passphrase", creds.Passphrase, &sealed.Passphrase},
	} {
		*f.dst, err = s.box.Seal(ctx, []byte(f.value), aad(w.ID, f.name))
		if err != nil {
			return domain.CredentialsInfo{}, fmt.Errorf("seal %s: %w", f.name, err)
		}
	}

	if err := s.repo.SaveCredentials(ctx, sealed); err != nil {
		return domain.CredentialsInfo{}, err
	}

	s.log.Info(ctx, "Polymarket credentials saved", "user_id", p.User.ID, "wallet_id", w.ID, "signature_type", creds.SignatureType)

	return sealed.Info, nil
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

func (s *Service) RemoveCredentials(ctx context.Context, p auth.Principal, id domain.WalletID) error {
	w, err := s.repo.Get(ctx, p.User.ID, id)
	if err != nil {
		return err
	}

	if err := s.repo.DeleteCredentials(ctx, w.ID); err != nil {
		return err
	}

	s.log.Info(ctx, "Polymarket credentials removed", "user_id", p.User.ID, "wallet_id", w.ID)

	return nil
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

	out := domain.PolymarketCredentials{Signer: c.Info.Signer, SignatureType: c.Info.SignatureType}

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
