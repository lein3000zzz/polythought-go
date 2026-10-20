package wallets

import (
	"context"
	"errors"
	"fmt"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.OrderKeys = (*Service)(nil)

// WithWorkflowTrading lets the service see which workflows trade from a wallet, so one a
// workflow trades live from is not unlinked.
func WithWorkflowTrading(trading ports.WorkflowTradingRepository) Option {
	return func(s *Service) { s.trading = trading }
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

// tradesLive refuses unlinking a wallet a workflow trades live from: its orders would
// have nothing to sign them.
func (s *Service) tradesLive(ctx context.Context, id domain.WalletID) error {
	if s.trading == nil {
		return nil
	}

	from, err := s.trading.TradingFrom(ctx, id)
	if err != nil {
		return err
	}

	for _, t := range from {
		if t.Mode == domain.ModeLive {
			return domain.ErrWalletTradesLive
		}
	}

	return nil
}
