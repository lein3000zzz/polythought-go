package trading

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Safety (3q1): a user's kill switch (D220, D221) and an admin's caps on their live buys
// (D222 to D224), kept here, where every live order goes, whatever the nodes before it
// did.

const capDay = 24 * time.Hour

// Why a stopped order was cancelled.
const (
	whyKillSwitch = "cancelled: the kill switch is on"
	whyLiveOff    = "cancelled: an admin turned live trading off"
)

// KillSwitch is how a user's kill switch stands after it was flipped.
type KillSwitch struct {
	On    bool
	Since time.Time
	// Cancelled are the resting orders it cancelled as it went on; Left those that still
	// rest, Polymarket not cancelling them.
	Cancelled, Left int
}

// sending is what a user's live buys let through may spend before they are in the books.
type sending struct {
	mu      sync.Mutex
	dollars decimal.Decimal
}

func (s *Service) sendingOf(u domain.UserID) *sending {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sending == nil {
		s.sending = map[domain.UserID]*sending{}
	}

	e, ok := s.sending[u]
	if !ok {
		e = &sending{}
		s.sending[u] = e
	}

	return e
}

// SetKillSwitch turns the user's kill switch on or off (D220): on, their workflows' live
// orders are refused and theirs resting on the book cancelled (D221); off, they trade as
// before.
func (s *Service) SetKillSwitch(ctx context.Context, p auth.Principal, on bool) (KillSwitch, error) {
	if on {
		return s.StopLive(ctx, p.User.ID, whyKillSwitch)
	}

	if s.d.Users == nil {
		return KillSwitch{}, nil
	}

	if err := s.d.Users.SetLiveStopped(ctx, p.User.ID, time.Time{}); err != nil {
		return KillSwitch{}, err
	}

	s.d.Logger.Info(ctx, "Kill switch off", "user_id", p.User.ID)

	return KillSwitch{}, nil
}

// StopLive turns the user's kill switch on, if it is not on already, and cancels their
// orders resting on the book: their own doing, or an admin disabling them (D226).
func (s *Service) StopLive(ctx context.Context, owner domain.UserID, why string) (KillSwitch, error) {
	if s.d.Users == nil {
		return KillSwitch{}, errors.New("no kill switch on this server")
	}

	u, err := s.d.Users.ByID(ctx, owner)
	if err != nil {
		return KillSwitch{}, err
	}

	ks := KillSwitch{On: true, Since: u.LiveStoppedAt}

	if !u.LiveStopped() {
		ks.Since = s.d.Now().UTC()

		if err := s.d.Users.SetLiveStopped(ctx, owner, ks.Since); err != nil {
			return KillSwitch{}, err
		}
	}

	open, err := s.ownerResting(ctx, owner)
	if err != nil {
		return ks, err
	}

	ks.Cancelled, ks.Left, err = s.cancelEach(ctx, open, why)

	s.d.Logger.Warn(ctx, "Kill switch on", "user_id", owner, "cancelled", ks.Cancelled, "left", ks.Left, logging.Err(err))

	return ks, err
}

// CancelAllLive cancels every live order resting on the book, every user's: an admin
// turned live trading off (D226).
func (s *Service) CancelAllLive(ctx context.Context) (int, error) {
	open, err := s.d.Ledger.OpenOrders(ctx, domain.ModeLive)
	if err != nil {
		return 0, err
	}

	cancelled, left, err := s.cancelEach(ctx, open, whyLiveOff)

	s.d.Logger.Warn(ctx, "Live trading off: resting live orders cancelled", "cancelled", cancelled, "left", left, logging.Err(err))

	return left, err
}

func (s *Service) ownerResting(ctx context.Context, owner domain.UserID) ([]domain.PlacedOrder, error) {
	unsettled, err := s.d.Ledger.OwnerUnsettled(ctx, owner, domain.ModeLive)
	if err != nil {
		return nil, err
	}

	var open []domain.PlacedOrder

	for _, o := range unsettled {
		if o.Status == domain.OrderOpen {
			open = append(open, o)
		}
	}

	return open, nil
}

// cancelEach cancels the live orders, saying how many went and how many still rest.
func (s *Service) cancelEach(ctx context.Context, open []domain.PlacedOrder, why string) (cancelled, left int, err error) {
	var errs []error

	for _, o := range open {
		got, _, err := s.cancelLive(ctx, o, why)
		if err != nil {
			errs = append(errs, fmt.Errorf("order %s: %w", o.ID, err))
		}

		switch {
		case err != nil || got.Status == domain.OrderOpen:
			left++
		case got.Status == domain.OrderCanceled:
			cancelled++
		}
	}

	return cancelled, left, errors.Join(errs...)
}

// stopped cancels what of open should not rest any more (D220, D226): every order with
// live trading off, and those of users whose kill switch is on. It catches the orders
// sent while either was turned on, which came back resting after it cancelled the rest.
func (s *Service) stopped(ctx context.Context, open []domain.PlacedOrder) {
	if s.d.Users == nil && s.liveOn() {
		return
	}

	why := map[domain.UserID]string{}

	for _, o := range open {
		w, ok := why[o.OwnerID]

		if !ok {
			switch {
			case !s.liveOn():
				w = whyLiveOff
			case s.d.Users != nil:
				u, err := s.d.Users.ByID(ctx, o.OwnerID)
				if err != nil {
					s.d.Logger.Warn(ctx, "Kill switch not read", "user_id", o.OwnerID, logging.Err(err))
				} else if u.LiveStopped() {
					w = whyKillSwitch
				}
			}

			why[o.OwnerID] = w
		}

		if w == "" {
			continue
		}

		if _, _, err := s.cancelLive(ctx, o, w); err != nil && ctx.Err() == nil {
			s.d.Logger.Warn(ctx, "Stopped live order not cancelled", "order_id", o.ID, logging.Err(err))
		}
	}
}

func (s *Service) liveOn() bool {
	return s.d.LiveOn != nil && s.d.LiveOn()
}

// admit lets a live order go, or says why not: its owner's kill switch (D220) and, for a
// buy, the admin's caps (D223). What a buy it lets go may spend counts until release,
// called once the order is in the books, so two runs at once cannot both pass the day
// cap.
func (s *Service) admit(ctx context.Context, o domain.PlacedOrder) (release func(), why string, err error) {
	release = func() {}

	var u domain.User

	if s.d.Users != nil {
		if u, err = s.d.Users.ByID(ctx, o.OwnerID); err != nil {
			return release, "", err
		}
	}

	if u.LiveStopped() {
		return release, "your kill switch is on: live trading is stopped until you resume it", nil
	}

	if o.Side != domain.SideBuy { // a sell lowers what is at risk (D101)
		return release, "", nil
	}

	var instance domain.LiveCaps
	if s.d.Caps != nil {
		instance = s.d.Caps()
	}

	caps := u.LiveCaps.Over(instance)
	value := o.Dollars.Add(o.Shares.Mul(o.Price))

	if limit, ok := capOf(caps.PerOrder); ok && value.GreaterThan(limit) {
		return release, fmt.Sprintf("$%s is over the $%s a live order may spend: an admin's cap", value.StringFixed(2), limit), nil
	}

	limit, ok := capOf(caps.PerDay)
	if !ok {
		return release, "", nil
	}

	e := s.sendingOf(o.OwnerID)

	e.mu.Lock()
	defer e.mu.Unlock()

	used, err := s.usedToday(ctx, o.OwnerID)
	if err != nil {
		return release, "", err
	}

	if used = used.Add(e.dollars); used.Add(value).GreaterThan(limit) {
		return release, fmt.Sprintf("$%s would go over the $%s a day your live buys may spend and hold, an admin's cap: "+
			"$%s spent or held in the last 24 hours, $%s left", value.StringFixed(2), limit, used.StringFixed(2),
			decimal.Max(limit.Sub(used), decimal.Zero).StringFixed(2)), nil
	}

	e.dollars = e.dollars.Add(value)

	return func() {
		e.mu.Lock()
		e.dollars = e.dollars.Sub(value)
		e.mu.Unlock()
	}, "", nil
}

// capOf is a cap in dollars, if there is one.
func capOf(c *int) (decimal.Decimal, bool) {
	if c == nil || *c <= 0 {
		return decimal.Zero, false
	}

	return decimal.NewFromInt(int64(*c)), true
}

// usedToday is what counts against a user's day cap (D223): what their live buys spent
// in the last 24 hours, and what those resting or pending may still spend.
func (s *Service) usedToday(ctx context.Context, owner domain.UserID) (decimal.Decimal, error) {
	spent, err := s.d.Ledger.OwnerSpent(ctx, owner, domain.ModeLive, s.d.Now().Add(-capDay))
	if err != nil {
		return decimal.Zero, err
	}

	unsettled, err := s.d.Ledger.OwnerUnsettled(ctx, owner, domain.ModeLive)
	if err != nil {
		return decimal.Zero, err
	}

	for _, o := range unsettled {
		if o.Side == domain.SideBuy {
			spent = spent.Add(unspent(o))
		}
	}

	return spent, nil
}

// unspent is what a buy may still spend: a buy of dollars what is left of them, one of
// shares those left at its limit.
func unspent(o domain.PlacedOrder) decimal.Decimal {
	if o.Dollars.IsPositive() {
		_, dollars, _ := o.Totals()

		return decimal.Max(o.Dollars.Sub(dollars), decimal.Zero)
	}

	return remaining(o).Mul(o.Price)
}

// Resting is the cash a workflow's resting buys hold now, by market, in the mode it
// trades in: what the Risk guard's caps count beside what it spent and holds.
func (s *Service) Resting(ctx context.Context, w domain.WorkflowID) (map[domain.MarketID]decimal.Decimal, error) {
	mode, err := s.Mode(ctx, w)
	if err != nil {
		return nil, err
	}

	open, err := s.d.Ledger.WorkflowOpenOrders(ctx, w, mode)
	if err != nil {
		return nil, err
	}

	out := map[domain.MarketID]decimal.Decimal{}

	for _, o := range open {
		if o.Side == domain.SideBuy {
			out[o.MarketID] = out[o.MarketID].Add(remaining(o).Mul(o.Price))
		}
	}

	return out, nil
}
