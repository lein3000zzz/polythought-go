package trading

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Setup is how a workflow trades, for its owner (D191): paper or live, from which
// wallet, and whether it could go live.
type Setup struct {
	Mode      domain.TradingMode
	Wallet    *domain.Wallet // nil: none chosen, or it is gone
	HoldsLive bool           // it holds live shares, so its wallet stays (D202)

	// LiveAvailable says whether the instance places live orders now; Why, why not.
	LiveAvailable bool
	Why           string
}

// SetupChange is how a workflow is to trade.
type SetupChange struct {
	Mode     domain.TradingMode
	WalletID domain.WalletID // empty: none

	// Confirmed is the owner's yes to orders spending real money (D201): going live, or
	// live from another wallet, needs it.
	Confirmed bool
}

// Setup is how a workflow trades now.
func (s *Service) Setup(ctx context.Context, p auth.Principal, id domain.WorkflowID) (Setup, error) {
	if _, err := s.d.Workflows.Get(ctx, p, id); err != nil {
		return Setup{}, err
	}

	t, err := s.tradingOf(ctx, id)
	if err != nil {
		return Setup{}, err
	}

	return s.setupOf(ctx, p.User.ID, t)
}

func (s *Service) setupOf(ctx context.Context, owner domain.UserID, t domain.WorkflowTrading) (Setup, error) {
	out := Setup{Mode: t.Mode}
	out.LiveAvailable, out.Why = s.liveAvailable()

	if t.WalletID != "" && s.d.Wallets != nil {
		w, err := s.d.Wallets.Wallet(ctx, owner, t.WalletID)

		switch {
		case err == nil:
			out.Wallet = &w
		case !errors.Is(err, domain.ErrNotFound):
			return Setup{}, err
		}
	}

	holds, err := s.holdsLive(ctx, t.WorkflowID)
	out.HoldsLive = holds

	return out, err
}

// WalletCash is the pUSD of one of the owner's wallets on the CLOB (D198), for a wallet
// that can trade: what going live with it spends (D201).
func (s *Service) WalletCash(ctx context.Context, p auth.Principal, wallet domain.WalletID) (decimal.Decimal, error) {
	if s.d.Exchange == nil || s.d.Keys == nil {
		return decimal.Zero, domain.ErrLiveTradingOff
	}

	k, err := s.d.Keys.OrderKey(ctx, p.User.ID, wallet)
	if err != nil {
		return decimal.Zero, err
	}

	return s.d.Exchange.Cash(ctx, k)
}

// SetSetup changes how a workflow trades. Live needs the instance to place live orders
// (D192), a wallet of the owner's that can trade, and the owner's confirmation (D201);
// the wallet changes only while the workflow holds nothing live (D202).
func (s *Service) SetSetup(ctx context.Context, p auth.Principal, id domain.WorkflowID, c SetupChange) (Setup, error) {
	if _, err := s.d.Workflows.Get(ctx, p, id); err != nil {
		return Setup{}, err
	}

	if c.Mode != domain.ModePaper && c.Mode != domain.ModeLive {
		return Setup{}, fmt.Errorf("%w: a mode is paper or live", domain.ErrInvalidInput)
	}

	if s.d.Trading == nil || s.d.Wallets == nil {
		return Setup{}, domain.ErrLiveTradingOff
	}

	// No order of the workflow goes while it changes.
	unlock := s.lock(id)
	defer unlock()

	current, err := s.tradingOf(ctx, id)
	if err != nil {
		return Setup{}, err
	}

	var w domain.Wallet

	if c.WalletID != "" {
		if w, err = s.d.Wallets.Wallet(ctx, p.User.ID, c.WalletID); err != nil {
			return Setup{}, err
		}
	}

	if c.WalletID != current.WalletID {
		holds, err := s.holdsLive(ctx, id)
		if err != nil {
			return Setup{}, err
		}

		if holds {
			return Setup{}, domain.ErrHoldsLive
		}
	}

	if c.Mode == domain.ModeLive {
		switch ok, _ := s.liveAvailable(); {
		case !ok:
			return Setup{}, domain.ErrLiveTradingOff
		case c.WalletID == "":
			return Setup{}, fmt.Errorf("%w: choose a wallet to trade from", domain.ErrInvalidInput)
		case !w.HasKey && !w.HasSessionKey:
			return Setup{}, domain.ErrWalletCannotTrade
		case !c.Confirmed && (current.Mode != domain.ModeLive || current.WalletID != c.WalletID):
			return Setup{}, domain.ErrNotConfirmed
		}
	}

	t := domain.WorkflowTrading{WorkflowID: id, Mode: c.Mode, WalletID: c.WalletID, UpdatedAt: s.d.Now().UTC()}
	if err := s.d.Trading.SetTrading(ctx, t); err != nil {
		return Setup{}, err
	}

	s.d.Logger.Info(ctx, "Workflow's trading changed", "workflow_id", id, "user_id", p.User.ID,
		"mode", t.Mode, "wallet_id", t.WalletID, "was", current.Mode)

	return s.setupOf(ctx, p.User.ID, t)
}
