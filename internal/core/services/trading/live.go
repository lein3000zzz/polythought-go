package trading

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Live orders (3p2): placed from the workflow's wallet through Polymarket's CLOB, FOK or
// FAK. A fill counts at match and is followed until its trade is final; a failed one is
// undone in the books and the owner told (D193). A delayed order, or one sent without an
// answer, is waited for (D204). The wallet's own pUSD is the cash (D198): the ledger
// keeps live holdings only.

var cent = decimal.New(1, -centDecimals)

const (
	defaultPatience = 30 * time.Second // what Place order waits for an order to come to something (D204)
	defaultPoll     = time.Second

	// followFor is how long a live order is followed at most; lostAfter, how long one
	// sent without an answer may stay unfound before it counts as never placed.
	followFor = 24 * time.Hour
	lostAfter = 10 * time.Minute
)

// tradingOf is how a workflow trades now: on paper unless it said otherwise (D22).
func (s *Service) tradingOf(ctx context.Context, w domain.WorkflowID) (domain.WorkflowTrading, error) {
	paper := domain.WorkflowTrading{WorkflowID: w, Mode: domain.ModePaper}
	if s.d.Trading == nil {
		return paper, nil
	}

	t, err := s.d.Trading.Trading(ctx, w)
	if errors.Is(err, domain.ErrNotFound) {
		return paper, nil
	}

	return t, err
}

// Mode is how a workflow trades now, for the trading nodes.
func (s *Service) Mode(ctx context.Context, w domain.WorkflowID) (domain.TradingMode, error) {
	t, err := s.tradingOf(ctx, w)

	return t.Mode, err
}

// liveAvailable says whether the instance places live orders now (D192), and why not.
func (s *Service) liveAvailable() (bool, string) {
	switch {
	case s.d.Exchange == nil || s.d.Keys == nil:
		return false, "this server cannot place live orders"
	case s.d.LiveOn == nil || !s.d.LiveOn():
		return false, "live trading is off on this server: an admin turns it on (Config, Trading, Live trading)"
	}

	return true, ""
}

func (s *Service) builderCode() string {
	if s.d.BuilderCode == nil {
		return ""
	}

	return s.d.BuilderCode()
}

// placeLive places o from the workflow's wallet, waits for what it came to (D204), and
// records it. The error is for an order not sent at all, which is not recorded.
func (s *Service) placeLive(ctx context.Context, o domain.PlacedOrder, rules domain.TradingRules, t domain.WorkflowTrading) (domain.PlacedOrder, error) {
	if ok, why := s.liveAvailable(); !ok {
		return s.refuse(ctx, o, why)
	}

	if t.WalletID == "" {
		return s.refuse(ctx, o, "the workflow has no wallet to trade from: choose one in its Trading view")
	}

	o.WalletID = t.WalletID

	held, err := s.holding(ctx, o)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	if o.Side == domain.SideSell {
		_, selling, err := s.committed(ctx, o.WorkflowID, o.Mode)
		if err != nil {
			return domain.PlacedOrder{}, err
		}

		if free := held.Shares.Sub(selling[held.Key()]); o.Shares.GreaterThan(free) {
			return s.refuse(ctx, o, fmt.Sprintf("not enough shares: %s to sell, %s held%s", o.Shares, free, restingSells(selling[held.Key()], o)))
		}
	}

	k, err := s.d.Keys.OrderKey(ctx, o.OwnerID, t.WalletID)
	if errors.Is(err, domain.ErrWalletCannotTrade) {
		return s.refuse(ctx, o, err.Error())
	}

	if errors.Is(err, domain.ErrNotFound) {
		return s.refuse(ctx, o, "the workflow's wallet is gone: choose another in its Trading view")
	}

	if err != nil {
		return domain.PlacedOrder{}, err
	}

	placed, err := s.d.Exchange.Place(ctx, k, ports.LiveOrder{
		TokenID: o.TokenID, Side: o.Side, Type: o.Type, Price: o.Price, Shares: o.Shares, Dollars: o.Dollars,
		TickSize: rules.TickSize, NegRisk: rules.NegRisk, Builder: s.builderCode(),
		PostOnly: o.PostOnly, Expires: o.Expires,
	})
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	o.ClobID = placed.ID

	switch {
	case placed.State == ports.LiveRefused:
		return s.refuse(ctx, o, placed.Reason)
	case placed.State == ports.LiveDone && len(placed.TradeIDs) == 0 && !o.Type.Rests():
		o.Status, o.Reason = domain.OrderUnfilled, placed.Reason

		return o, s.d.Ledger.Record(ctx, o, nil)
	}

	o.Status = domain.OrderPending

	settled, err := s.await(ctx, k, o, rules, placed)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	if settled.Status == domain.OrderPending {
		settled.Reason = "sent: what it came to is not known yet, and is followed"
	}

	var change *ports.LedgerChange
	if len(settled.Fills) > 0 {
		_, after := apply(settled, held)
		change = &ports.LedgerChange{Holding: after}
	}

	if err := s.d.Ledger.Record(ctx, settled, change); err != nil {
		// On Polymarket, yet not in the books: all there is to find it again.
		s.d.Logger.Error(ctx, "Live order placed but not recorded", "workflow_id", o.WorkflowID, "order_id", o.ID,
			"clob_id", o.ClobID, "wallet_id", o.WalletID, "side", o.Side, "token_id", o.TokenID,
			"status", settled.Status, logging.Err(err))

		return domain.PlacedOrder{}, err
	}

	s.d.Logger.Info(ctx, "Live order placed", "workflow_id", o.WorkflowID, "order_id", o.ID, "clob_id", o.ClobID,
		"wallet_id", o.WalletID, "side", o.Side, "status", settled.Status, "token_id", o.TokenID)

	return settled, nil
}

// await waits up to the patience for a live order to come to something (D204): its
// fills once its trades are listed. Undecided, it stays pending.
func (s *Service) await(ctx context.Context, k ports.OrderKey, o domain.PlacedOrder, rules domain.TradingRules, placed ports.LivePlacement) (domain.PlacedOrder, error) {
	state, trades := placed.State, placed.TradeIDs

	for attempt := 0; ; attempt++ {
		if decided, ok := s.decide(ctx, k, o, rules, state, trades); ok {
			return decided, nil
		}

		if attempt >= int(s.d.Patience/s.d.Poll) {
			return o, nil
		}

		// Cancelled while waiting, it is recorded pending, and followed.
		if sleep(ctx, s.d.Poll) != nil {
			return o, nil
		}

		st, err := s.d.Exchange.Order(ctx, k, o.ClobID)
		if err != nil {
			s.d.Logger.Warn(ctx, "Live order not looked up", "order_id", o.ID, logging.Err(err))
			continue
		}

		state, trades = st.State, st.TradeIDs
	}
}

// decide is the order as Polymarket has it in state, with the trades ids: its fills, at
// their fees, and how it stands; ok is false while that is not known yet: delayed, not
// found, or a trade not listed yet.
func (s *Service) decide(ctx context.Context, k ports.OrderKey, o domain.PlacedOrder, rules domain.TradingRules, state ports.LiveState, ids []string) (domain.PlacedOrder, bool) {
	switch {
	case state == ports.LiveMatched || state == ports.LiveDone:
	case state == ports.LiveResting && o.Type.Rests():
	default:
		return o, false
	}

	o.Fills = nil

	if len(ids) > 0 {
		trades, err := s.d.Exchange.Trades(ctx, k, o.ClobID, o.TokenID, ids)
		if err != nil {
			s.d.Logger.Warn(ctx, "Live order's trades not read", "order_id", o.ID, logging.Err(err))
			return o, false
		}

		if len(trades) < len(ids) {
			return o, false
		}

		for _, t := range trades {
			for _, f := range t.Fills {
				f.Fee, f.Status, f.TradeID = fee(f.Shares, f.Price, rules.Fees), t.Status, t.ID
				if t.Maker {
					f.Fee = decimal.Zero // makers pay none (D90)
				}

				o.Fills = append(o.Fills, f)
			}
		}
	}

	o.Status, o.Reason = s.liveOutcome(o, state)

	return o, true
}

// liveOutcome is how a live order stands, its fills known: FAK and FOK as they filled;
// a resting order on the book, filled, or cancelled or expired with what it filled.
func (s *Service) liveOutcome(o domain.PlacedOrder, state ports.LiveState) (domain.OrderStatus, string) {
	if len(o.Fills) > 0 && !slices.ContainsFunc(o.Fills, func(f domain.OrderFill) bool { return f.Status != domain.FillFailed }) {
		return domain.OrderFailed, "it matched, but its trades failed on chain: undone"
	}

	if !o.Type.Rests() {
		if len(o.Fills) == 0 {
			return domain.OrderUnfilled, "the book could not fill it within its price"
		}

		return liveStatus(o)
	}

	shares, _, _ := o.Totals()

	switch {
	case state == ports.LiveMatched || shares.GreaterThanOrEqual(o.Shares):
		return domain.OrderFilled, ""
	case state == ports.LiveResting:
		return domain.OrderOpen, resting(o)
	case o.Type == domain.OrderGTD && !s.d.Now().Before(o.Expires):
		return domain.OrderExpired, "expired, " + filledOf(o)
	default:
		return domain.OrderCanceled, "cancelled on Polymarket, " + filledOf(o)
	}
}

// resting says where an order rests and what of it filled.
func resting(o domain.PlacedOrder) string {
	return fmt.Sprintf("rests on the book at %s, %s", o.Price, filledOf(o))
}

func filledOf(o domain.PlacedOrder) string {
	shares, _, _ := o.Totals()

	return fmt.Sprintf("%s of %s shares filled", shares, o.Shares)
}

// liveStatus is how a matched FAK or FOK went: filled as asked, or in part.
func liveStatus(o domain.PlacedOrder) (domain.OrderStatus, string) {
	shares, dollars, _ := o.Totals()

	switch {
	case o.Type == domain.OrderFOK:
		return domain.OrderFilled, ""
	case o.Shares.IsPositive() && shares.GreaterThanOrEqual(o.Shares):
		return domain.OrderFilled, ""
	case o.Dollars.IsPositive() && dollars.GreaterThanOrEqual(o.Dollars.Sub(cent)):
		return domain.OrderFilled, ""
	default:
		return domain.OrderPartial, "the book filled part of it within its price; the rest was cancelled"
	}
}

func (s *Service) holding(ctx context.Context, o domain.PlacedOrder) (domain.Holding, error) {
	key := domain.HoldingKey{WorkflowID: o.WorkflowID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}

	h, err := s.d.Ledger.Holding(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Holding{WorkflowID: o.WorkflowID, OwnerID: o.OwnerID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}, nil
	}

	return h, err
}

// FollowEvery follows the live orders still settling now and then until ctx is done.
func (s *Service) FollowEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		if err := s.Follow(ctx); err != nil && ctx.Err() == nil {
			s.d.Logger.Warn(ctx, "Live orders not followed", logging.Err(err))
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Follow looks at the live orders still settling (D193, D204): pending ones, until they
// come to something, and matched ones, until their trades are final.
func (s *Service) Follow(ctx context.Context) error {
	if s.d.Exchange == nil || s.d.Keys == nil {
		return nil
	}

	orders, err := s.d.Ledger.Following(ctx, s.d.Now().Add(-followFor))
	if err != nil {
		return err
	}

	var errs []error

	for _, o := range orders {
		if err := s.follow(ctx, o); err != nil {
			errs = append(errs, fmt.Errorf("order %s: %w", o.ID, err))
		}
	}

	return errors.Join(errs...)
}

func (s *Service) follow(ctx context.Context, o domain.PlacedOrder) error {
	unlock := s.lock(o.WorkflowID)
	defer unlock()

	k, err := s.d.Keys.OrderKey(ctx, o.OwnerID, o.WalletID)
	if err != nil {
		return fmt.Errorf("its wallet's key: %w", err)
	}

	rules, err := s.d.Rules.TradingRules(ctx, o.MarketID)
	if err != nil {
		return fmt.Errorf("the market's rules: %w", err)
	}

	before := o

	if o.Status == domain.OrderPending || o.Status == domain.OrderOpen {
		st, err := s.d.Exchange.Order(ctx, k, o.ClobID)
		if err != nil {
			return err
		}

		decided, ok := s.decide(ctx, k, o, rules, st.State, st.TradeIDs)

		switch {
		case ok:
			o = decided
		case st.State == ports.LiveNotFound && o.Status == domain.OrderPending && s.d.Now().Sub(o.CreatedAt) >= lostAfter:
			o.Status, o.Reason = domain.OrderRefused, "it never reached Polymarket's book"
		default:
			return nil // still delayed, or not known yet
		}
	} else if o, err = s.followTrades(ctx, k, o); err != nil {
		return err
	}

	if unchanged(before, o) {
		return nil
	}

	h, err := s.replay(ctx, o)
	if err != nil {
		return err
	}

	if err := s.d.Ledger.Update(ctx, o, &ports.LedgerChange{Holding: h}); err != nil {
		return err
	}

	s.d.Logger.Info(ctx, "Live order followed", "workflow_id", o.WorkflowID, "order_id", o.ID, "status", o.Status)

	if failedNow(before, o) {
		s.d.Logger.Warn(ctx, "Live trade failed: undone in the books", "workflow_id", o.WorkflowID, "order_id", o.ID)

		if s.d.Notifier != nil {
			if err := s.d.Notifier.LiveTradeFailed(ctx, o.OwnerID, o); err != nil {
				s.d.Logger.Warn(ctx, "Owner not told of a failed live trade", "order_id", o.ID, logging.Err(err))
			}
		}
	}

	return nil
}

// followTrades reads again the trades of o not final yet.
func (s *Service) followTrades(ctx context.Context, k ports.OrderKey, o domain.PlacedOrder) (domain.PlacedOrder, error) {
	var ids []string

	for _, f := range o.Fills {
		if !f.Status.Final() && !slices.Contains(ids, f.TradeID) {
			ids = append(ids, f.TradeID)
		}
	}

	trades, err := s.d.Exchange.Trades(ctx, k, o.ClobID, o.TokenID, ids)
	if err != nil {
		return o, err
	}

	status := map[string]domain.FillStatus{}
	for _, t := range trades {
		status[t.ID] = t.Status
	}

	o.Fills = slices.Clone(o.Fills)

	for i, f := range o.Fills {
		if st, ok := status[f.TradeID]; ok && !f.Status.Final() {
			o.Fills[i].Status = st
		}
	}

	// Ended with all its trades failed, it failed; one resting still rests.
	allFailed := !slices.ContainsFunc(o.Fills, func(f domain.OrderFill) bool { return f.Status != domain.FillFailed })
	if allFailed && o.Status != domain.OrderOpen && o.Status != domain.OrderPending {
		o.Status, o.Reason = domain.OrderFailed, "it matched, but its trades failed on chain: undone"
	}

	return o, nil
}

// replay is o's holding made again from all its orders, o as it is now: a failed trade
// taken out of it exactly, whatever came after.
func (s *Service) replay(ctx context.Context, o domain.PlacedOrder) (domain.Holding, error) {
	key := domain.HoldingKey{WorkflowID: o.WorkflowID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}

	orders, err := s.d.Ledger.OrdersOf(ctx, key)
	if err != nil {
		return domain.Holding{}, err
	}

	h := domain.Holding{WorkflowID: o.WorkflowID, OwnerID: o.OwnerID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID, MarketID: o.MarketID}

	for _, past := range orders {
		if past.ID == o.ID {
			past = o
		}

		if len(past.Fills) > 0 {
			_, h = apply(past, h)
		}
	}

	h.UpdatedAt = s.d.Now().UTC()

	return h, nil
}

func fillStatuses(o domain.PlacedOrder) []domain.FillStatus {
	out := make([]domain.FillStatus, len(o.Fills))
	for i, f := range o.Fills {
		out[i] = f.Status
	}

	return out
}

// unchanged is whether following an order changed nothing of it.
func unchanged(before, after domain.PlacedOrder) bool {
	return before.Status == after.Status && before.Reason == after.Reason &&
		slices.Equal(fillStatuses(before), fillStatuses(after))
}

// failedNow is whether a trade of o failed since before.
func failedNow(before, after domain.PlacedOrder) bool {
	failed := func(o domain.PlacedOrder) int {
		n := 0
		for _, f := range o.Fills {
			if f.Status == domain.FillFailed {
				n++
			}
		}

		return n
	}

	return failed(after) > failed(before)
}

// Worth is what a workflow has to size copies by (D97, D198): on paper, its paper
// account's cash and what its holdings cost, or before it opens what it starts with;
// live, its wallet's pUSD and what its live holdings cost.
func (s *Service) Worth(ctx context.Context, owner domain.UserID, w domain.WorkflowID) (decimal.Decimal, error) {
	t, err := s.tradingOf(ctx, w)
	if err != nil {
		return decimal.Zero, err
	}

	var cash decimal.Decimal

	if t.Mode == domain.ModeLive {
		if cash, err = s.liveCash(ctx, owner, t); err != nil {
			return decimal.Zero, err
		}
	} else {
		a, err := s.d.Ledger.PaperAccount(ctx, w)
		if errors.Is(err, domain.ErrNotFound) {
			return s.d.PaperBalance(), nil
		}

		if err != nil {
			return decimal.Zero, err
		}

		cash = a.Cash
	}

	holdings, err := s.d.Ledger.Holdings(ctx, w)
	if err != nil {
		return decimal.Zero, err
	}

	worth := cash
	for _, h := range holdings {
		if h.Mode == t.Mode {
			worth = worth.Add(h.Cost)
		}
	}

	return worth, nil
}

// liveCash is the pUSD of the workflow's wallet on the CLOB.
func (s *Service) liveCash(ctx context.Context, owner domain.UserID, t domain.WorkflowTrading) (decimal.Decimal, error) {
	if ok, why := s.liveAvailable(); !ok {
		return decimal.Zero, errors.New(why)
	}

	if t.WalletID == "" {
		return decimal.Zero, errors.New("the workflow has no wallet to trade from")
	}

	k, err := s.d.Keys.OrderKey(ctx, owner, t.WalletID)
	if err != nil {
		return decimal.Zero, err
	}

	return s.d.Exchange.Cash(ctx, k)
}

// holdsLive is whether a workflow holds live shares (D202).
func (s *Service) holdsLive(ctx context.Context, w domain.WorkflowID) (bool, error) {
	holdings, err := s.d.Ledger.Holdings(ctx, w)
	if err != nil {
		return false, err
	}

	return slices.ContainsFunc(holdings, func(h domain.Holding) bool {
		return h.Mode == domain.ModeLive && h.Shares.IsPositive()
	}), nil
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
