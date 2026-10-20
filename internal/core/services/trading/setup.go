package trading

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Setup is how a workflow trades, for its owner (D191): paper or live, from which
// wallet, and whether it could go live.
type Setup struct {
	Mode      domain.TradingMode
	Wallet    *domain.Wallet // nil: none chosen, or it is gone
	HoldsLive bool           // it holds live shares, so its wallet stays (D202)
	// Resting is how many of its live orders rest on the book: switching it off asks
	// whether to cancel them (D217); going back to paper cancels them (D213).
	Resting int

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
	if err != nil {
		return Setup{}, err
	}

	out.HoldsLive = holds

	resting, err := s.d.Ledger.WorkflowOpenOrders(ctx, t.WorkflowID, domain.ModeLive)
	out.Resting = len(resting)

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

	if err := s.setSetup(ctx, p, id, c); err != nil {
		return Setup{}, err
	}

	// Back on paper, it leaves no live orders behind (D213); what Polymarket would not
	// cancel still rests, and Setup says so.
	if c.Mode == domain.ModePaper {
		if _, err := s.CancelResting(ctx, id, "cancelled: the workflow went back to paper"); err != nil {
			s.d.Logger.Warn(ctx, "Live resting orders not cancelled going back to paper", "workflow_id", id, logging.Err(err))
		}
	}

	t, err := s.tradingOf(ctx, id)
	if err != nil {
		return Setup{}, err
	}

	return s.setupOf(ctx, p.User.ID, t)
}

func (s *Service) setSetup(ctx context.Context, p auth.Principal, id domain.WorkflowID, c SetupChange) error {
	// No order of the workflow goes while it changes.
	unlock := s.lock(id)
	defer unlock()

	current, err := s.tradingOf(ctx, id)
	if err != nil {
		return err
	}

	var w domain.Wallet

	if c.WalletID != "" {
		if w, err = s.d.Wallets.Wallet(ctx, p.User.ID, c.WalletID); err != nil {
			return err
		}
	}

	if c.WalletID != current.WalletID {
		holds, err := s.holdsLive(ctx, id)
		if err != nil {
			return err
		}

		if holds {
			return domain.ErrHoldsLive
		}
	}

	if c.Mode == domain.ModeLive {
		switch ok, _ := s.liveAvailable(); {
		case !ok:
			return domain.ErrLiveTradingOff
		case c.WalletID == "":
			return fmt.Errorf("%w: choose a wallet to trade from", domain.ErrInvalidInput)
		case !w.HasKey && !w.HasSessionKey:
			return domain.ErrWalletCannotTrade
		case !c.Confirmed && (current.Mode != domain.ModeLive || current.WalletID != c.WalletID):
			return domain.ErrNotConfirmed
		}
	}

	t := domain.WorkflowTrading{WorkflowID: id, Mode: c.Mode, WalletID: c.WalletID, UpdatedAt: s.d.Now().UTC()}
	if err := s.d.Trading.SetTrading(ctx, t); err != nil {
		return err
	}

	s.d.Logger.Info(ctx, "Workflow's trading changed", "workflow_id", id, "user_id", p.User.ID,
		"mode", t.Mode, "wallet_id", t.WalletID, "was", current.Mode)

	return nil
}

var _ ports.WalletReleaser = (*Service)(nil)

// TradingLive names the owner's workflows that trade live from a wallet (D218).
func (s *Service) TradingLive(ctx context.Context, owner domain.UserID, wallet domain.WalletID) ([]string, error) {
	live, err := s.liveFrom(ctx, wallet)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(live))

	for _, t := range live {
		w, err := s.d.Workflows.Get(ctx, auth.Principal{User: domain.User{ID: owner}}, t.WorkflowID)
		if err != nil {
			return nil, err
		}

		names = append(names, w.Name)
	}

	return names, nil
}

// ReleaseWallet frees a wallet its owner unlinks (D218): the workflows trading live from
// it have their resting orders cancelled and go back to paper, their live books kept.
func (s *Service) ReleaseWallet(ctx context.Context, owner domain.UserID, wallet domain.WalletID) error {
	live, err := s.liveFrom(ctx, wallet)
	if err != nil {
		return err
	}

	for _, t := range live {
		if _, err := s.CancelResting(ctx, t.WorkflowID, "cancelled: its wallet was unlinked"); err != nil {
			s.d.Logger.Warn(ctx, "Live resting orders not cancelled as their wallet goes", "workflow_id", t.WorkflowID, logging.Err(err))
		}

		unlock := s.lock(t.WorkflowID)
		err := s.d.Trading.SetTrading(ctx, domain.WorkflowTrading{WorkflowID: t.WorkflowID, Mode: domain.ModePaper, UpdatedAt: s.d.Now().UTC()})
		unlock()

		if err != nil {
			return err
		}

		s.d.Logger.Info(ctx, "Workflow back to paper: its wallet was unlinked", "workflow_id", t.WorkflowID, "user_id", owner)
	}

	return nil
}

// liveFrom are the workflows trading live from a wallet.
func (s *Service) liveFrom(ctx context.Context, wallet domain.WalletID) ([]domain.WorkflowTrading, error) {
	if s.d.Trading == nil {
		return nil, nil
	}

	from, err := s.d.Trading.TradingFrom(ctx, wallet)
	if err != nil {
		return nil, err
	}

	return slices.DeleteFunc(from, func(t domain.WorkflowTrading) bool { return t.Mode != domain.ModeLive }), nil
}
