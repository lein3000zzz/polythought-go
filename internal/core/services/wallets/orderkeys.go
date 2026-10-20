package wallets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

var _ ports.OrderKeys = (*Service)(nil)

// WithWalletRelease lets the service see which workflows trade live from a wallet, and
// free it from them when its owner confirms unlinking it (D218).
func WithWalletRelease(r ports.WalletReleaser) Option {
	return func(s *Service) { s.releaser = r }
}

// OrderKey is what signs a wallet's live orders (D153): its credentials, and its
// imported private key or its active session key, decrypted in memory for the trading
// service only, never for a response.
func (s *Service) OrderKey(ctx context.Context, owner domain.UserID, id domain.WalletID) (ports.OrderKey, error) {
	w, err := s.repo.Get(ctx, owner, id)
	if err != nil {
		return ports.OrderKey{}, err
	}

	if !w.HasKey && !w.HasSessionKey {
		return ports.OrderKey{}, domain.ErrWalletCannotTrade
	}

	creds, err := s.Credentials(ctx, owner, id)
	if err != nil {
		return ports.OrderKey{}, err
	}

	var key []byte

	if w.HasSessionKey {
		if s.sessionKeys == nil {
			return ports.OrderKey{}, domain.ErrWalletCannotTrade
		}

		k, err := s.sessionKey(ctx, w.ID, domain.SessionKeyActive)
		if errors.Is(err, domain.ErrNotFound) {
			return ports.OrderKey{}, domain.ErrWalletCannotTrade
		}

		if err != nil {
			return ports.OrderKey{}, err
		}

		if !k.Info.ValidUntil.After(s.now()) {
			return ports.OrderKey{}, fmt.Errorf("%w: its session key expired on %s", domain.ErrWalletCannotTrade,
				k.Info.ValidUntil.UTC().Format("2 January 2006"))
		}

		if key, err = s.box.Open(ctx, k.Key, sessionKeyAAD(w.ID, k.Info.Signer)); err != nil {
			return ports.OrderKey{}, fmt.Errorf("open session key: %w", err)
		}
	} else {
		c, err := s.repo.Credentials(ctx, w.ID)
		if err != nil {
			return ports.OrderKey{}, err
		}

		if key, err = s.box.Open(ctx, c.PrivateKey, aad(w.ID, "private_key")); err != nil {
			return ports.OrderKey{}, fmt.Errorf("open private key: %w", err)
		}
	}

	return ports.OrderKey{Wallet: w, Credentials: creds, Key: string(key)}, nil
}

// release frees a wallet from the workflows trading live from it before it is unlinked
// (D218): unconfirmed, it names them; confirmed, their resting orders are cancelled and
// they go back to paper.
func (s *Service) release(ctx context.Context, owner domain.UserID, id domain.WalletID, confirmed bool) error {
	if s.releaser == nil {
		return nil
	}

	names, err := s.releaser.TradingLive(ctx, owner, id)
	if err != nil || len(names) == 0 {
		return err
	}

	if !confirmed {
		return fmt.Errorf("%w: %s; unlinking it cancels their resting orders and puts them back on paper",
			domain.ErrWalletTradesLive, quoteNames(names))
	}

	return s.releaser.ReleaseWallet(ctx, owner, id)
}

// quoteNames lists names in a sentence: "A", "A and B", "A, B and C".
func quoteNames(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "“" + n + "”"
	}

	if len(q) == 1 {
		return q[0]
	}

	return strings.Join(q[:len(q)-1], ", ") + " and " + q[len(q)-1]
}

// SetHeartbeat sets whether Polymarket cancels the wallet's resting orders when the
// server goes quiet (D214).
func (s *Service) SetHeartbeat(ctx context.Context, p auth.Principal, id domain.WalletID, on bool) (domain.Wallet, error) {
	if err := s.repo.SetHeartbeat(ctx, p.User.ID, id, on); err != nil {
		return domain.Wallet{}, err
	}

	s.log.Info(ctx, "Wallet heartbeat set", "user_id", p.User.ID, "wallet_id", id, "on", on)

	return s.repo.Get(ctx, p.User.ID, id)
}
