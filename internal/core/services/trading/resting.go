package trading

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Resting orders (3p3a, D205): GTC and GTD orders that wait on the book. On paper they
// fill as the live market trades through their price (D209), their buys holding their
// cash meanwhile (D210); live, Polymarket's user channel says when they change (D195)
// and the CLOB is read again.

// minGTD is the least a GTD may wait: the CLOB takes none expiring within 3 minutes.
const minGTD = 3 * time.Minute

// feedChunk is how many tokens one feed subscription holds.
const feedChunk = 50

// remaining is what of an order is still to fill.
func remaining(o domain.PlacedOrder) decimal.Decimal {
	shares, _, _ := o.Totals()

	return decimal.Max(o.Shares.Sub(shares), decimal.Zero)
}

// committed is what a workflow's resting orders in a mode hold back (D210): the cash its
// buys may spend, and per holding the shares its sells may sell.
func (s *Service) committed(ctx context.Context, w domain.WorkflowID, mode domain.TradingMode) (decimal.Decimal, map[domain.HoldingKey]decimal.Decimal, error) {
	open, err := s.d.Ledger.WorkflowOpenOrders(ctx, w, mode)
	if err != nil {
		return decimal.Zero, nil, err
	}

	cash, shares := decimal.Zero, map[domain.HoldingKey]decimal.Decimal{}

	for _, o := range open {
		left := remaining(o)

		if o.Side == domain.SideBuy {
			cash = cash.Add(left.Mul(o.Price))
			continue
		}

		key := domain.HoldingKey{WorkflowID: o.WorkflowID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}
		shares[key] = shares[key].Add(left)
	}

	return cash, shares, nil
}

// restingSells says, when there are, what resting sells hold back.
func restingSells(selling decimal.Decimal, o domain.PlacedOrder) string {
	held := copied(o)
	if selling.IsPositive() {
		held += fmt.Sprintf(", %s more held back by resting sells", selling)
	}

	return held
}

// asResting makes a resting buy of dollars a buy of shares at its limit, as resting
// orders are in shares, and holds a GTD to the CLOB's least wait.
func (s *Service) asResting(o domain.PlacedOrder) (domain.PlacedOrder, string) {
	if !o.Type.Rests() {
		return o, ""
	}

	if o.Dollars.IsPositive() {
		o.Shares, o.Dollars = o.Dollars.Div(o.Price).RoundDown(shareDecimals), decimal.Zero
	}

	switch {
	case o.Type == domain.OrderGTC:
		o.Expires = time.Time{}
	case o.Expires.IsZero():
		return o, "a GTD order needs a time it expires at"
	case o.Expires.Before(s.d.Now().Add(minGTD)):
		return o, "a GTD order expires 3 minutes from now at the earliest, as Polymarket takes it"
	}

	return o, ""
}

// placePaperResting places a paper GTC or GTD (D206): what crosses the book fills at once
// as a FAK would, paying the taker fee; the rest rests at its price, its cash held
// (D210). A post-only order that would cross is refused, as the CLOB refuses it.
func (s *Service) placePaperResting(ctx context.Context, o domain.PlacedOrder, rules domain.TradingRules, held domain.Holding, account domain.PaperAccount) (domain.PlacedOrder, error) {
	holding, selling, err := s.committed(ctx, o.WorkflowID, o.Mode)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	if free := held.Shares.Sub(selling[held.Key()]); o.Side == domain.SideSell && o.Shares.GreaterThan(free) {
		return s.refuse(ctx, o, fmt.Sprintf("not enough shares: %s to sell, %s held%s", o.Shares, free, restingSells(selling[held.Key()], o)))
	}

	books, err := s.d.Books.OrderBooks(ctx, o.TokenID)
	if err != nil {
		return domain.PlacedOrder{}, fmt.Errorf("the order book: %w", err)
	}

	var book domain.OrderBook
	if len(books) > 0 {
		book = books[0]
	}

	fills, _, _ := fill(o.OrderRequest, book, rules.Fees, o.CreatedAt)

	if o.PostOnly && len(fills) > 0 {
		return s.refuse(ctx, o, "post-only: it would match the book at once")
	}

	o.Fills = fills
	left := remaining(o)

	// As the CLOB checks a balance: what crosses now, and all the rest may spend.
	free := account.Cash.Sub(holding)
	if _, spent, fees := o.Totals(); o.Side == domain.SideBuy && spent.Add(fees).Add(left.Mul(o.Price)).GreaterThan(free) {
		return s.refuse(ctx, o, fmt.Sprintf("not enough paper cash: $%s for it, $%s free",
			spent.Add(fees).Add(left.Mul(o.Price)).StringFixed(2), free.StringFixed(2)))
	}

	o.Status, o.Reason = domain.OrderOpen, resting(o)
	if left.IsZero() {
		o.Status, o.Reason = domain.OrderFilled, ""
	}

	var change *ports.LedgerChange
	if len(o.Fills) > 0 {
		cash, after := apply(o, held)
		change = &ports.LedgerChange{Cash: cash, Holding: after}
	}

	if err := s.d.Ledger.Record(ctx, o, change); err != nil {
		if errors.Is(err, domain.ErrNotEnoughCash) || errors.Is(err, domain.ErrNotEnoughShares) {
			return s.refuse(ctx, o, "the paper account changed under it")
		}

		return domain.PlacedOrder{}, err
	}

	s.d.Logger.Info(ctx, "Paper order resting", "workflow_id", o.WorkflowID, "order_id", o.ID, "status", o.Status,
		"token_id", o.TokenID, "price", o.Price, "left", left)

	return o, nil
}

// RestEvery keeps the paper orders resting on the book until ctx is done: filled as the
// live market trades through their price (D209), expired at their time.
func (s *Service) RestEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	var (
		subs    []ports.FeedSubscription
		tokens  []domain.TokenID
		updates = make(chan domain.MarketUpdate, 256)
	)

	closeAll := func() {
		for _, sub := range subs {
			sub.Close()
		}

		subs, tokens = nil, nil
	}
	defer closeAll()

	for {
		want, err := s.keepResting(ctx)
		if err != nil && ctx.Err() == nil {
			s.d.Logger.Warn(ctx, "Paper resting orders not kept", logging.Err(err))
		}

		if s.d.Feed != nil && err == nil && !slices.Equal(want, tokens) {
			closeAll()

			if subs, err = s.watchTokens(ctx, want, updates); err != nil {
				s.d.Logger.Warn(ctx, "Paper resting orders not watched", logging.Err(err))
				closeAll()
			} else {
				tokens = want
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case u := <-updates:
			if trade, ok := u.(domain.TradeUpdate); ok {
				if err := s.paperTraded(ctx, trade.Trade); err != nil && ctx.Err() == nil {
					s.d.Logger.Warn(ctx, "Paper resting orders not filled", "token_id", trade.TokenID, logging.Err(err))
				}
			}
		}
	}
}

// watchTokens subscribes the market feed to tokens, in chunks, all into updates.
func (s *Service) watchTokens(ctx context.Context, tokens []domain.TokenID, updates chan<- domain.MarketUpdate) ([]ports.FeedSubscription, error) {
	var subs []ports.FeedSubscription

	for chunk := range slices.Chunk(tokens, feedChunk) {
		sub, err := s.d.Feed.Subscribe(ctx, chunk)
		if err != nil {
			return subs, err
		}

		subs = append(subs, sub)

		go func() {
			for u := range sub.Updates() {
				select {
				case updates <- u:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	return subs, nil
}

// keepResting expires the paper GTD orders whose time came, and says the tokens of those
// still resting.
func (s *Service) keepResting(ctx context.Context) ([]domain.TokenID, error) {
	open, err := s.d.Ledger.OpenOrders(ctx, domain.ModePaper)
	if err != nil {
		return nil, err
	}

	var tokens []domain.TokenID

	for _, o := range open {
		if o.Type == domain.OrderGTD && !s.d.Now().Before(o.Expires) {
			if err := s.endPaper(ctx, o.ID, domain.OrderExpired, "expired"); err != nil {
				return nil, err
			}

			continue
		}

		if !slices.Contains(tokens, o.TokenID) {
			tokens = append(tokens, o.TokenID)
		}
	}

	slices.Sort(tokens)

	return tokens, nil
}

// paperTraded fills the paper orders a live trade went through (D209): a resting buy
// above its price, a resting sell below, the most eager first, as much as it traded.
func (s *Service) paperTraded(ctx context.Context, t domain.Trade) error {
	open, err := s.d.Ledger.OpenOrders(ctx, domain.ModePaper)
	if err != nil {
		return err
	}

	var through []domain.PlacedOrder

	for _, o := range open {
		if o.TokenID != t.TokenID {
			continue
		}

		if (o.Side == domain.SideBuy && t.Price.LessThan(o.Price)) || (o.Side == domain.SideSell && t.Price.GreaterThan(o.Price)) {
			through = append(through, o)
		}
	}

	slices.SortStableFunc(through, func(a, b domain.PlacedOrder) int {
		if a.Side == domain.SideBuy {
			return b.Price.Cmp(a.Price)
		}

		return a.Price.Cmp(b.Price)
	})

	left := t.Size

	var errs []error

	for _, o := range through {
		if !left.IsPositive() {
			break
		}

		took, err := s.paperFill(ctx, o.ID, left)
		if err != nil {
			errs = append(errs, err)
		}

		left = left.Sub(took)
	}

	return errors.Join(errs...)
}

// paperFill fills up to most shares of a resting paper order at its own price, as a
// maker: no fee (D209). It says what it took.
func (s *Service) paperFill(ctx context.Context, id domain.OrderID, most decimal.Decimal) (decimal.Decimal, error) {
	o, err := s.d.Ledger.Order(ctx, id)
	if err != nil {
		return decimal.Zero, err
	}

	unlock := s.lock(o.WorkflowID)
	defer unlock()

	if o, err = s.d.Ledger.Order(ctx, id); err != nil || o.Status != domain.OrderOpen { // as it is now
		return decimal.Zero, err
	}

	take := decimal.Min(remaining(o), most).RoundDown(shareDecimals)
	if !take.IsPositive() {
		return decimal.Zero, nil
	}

	held, err := s.holding(ctx, o)
	if err != nil {
		return decimal.Zero, err
	}

	part := o
	part.Fills = []domain.OrderFill{{Price: o.Price, Shares: take, Dollars: take.Mul(o.Price), Fee: decimal.Zero, At: s.d.Now().UTC()}}
	cash, after := apply(part, held)

	o.Fills = append(slices.Clone(o.Fills), part.Fills[0])
	o.Status, o.Reason = domain.OrderOpen, resting(o)

	if remaining(o).IsZero() {
		o.Status, o.Reason = domain.OrderFilled, ""
	}

	err = s.d.Ledger.Update(ctx, o, &ports.LedgerChange{Cash: cash, Holding: after})
	if errors.Is(err, domain.ErrNotEnoughCash) || errors.Is(err, domain.ErrNotEnoughShares) {
		return decimal.Zero, s.endPaperLocked(ctx, o.ID, domain.OrderCanceled, "cancelled: the paper account could no longer pay for it")
	}

	if err != nil {
		return decimal.Zero, err
	}

	s.publish(ctx, domain.OrderEvent{Kind: domain.OrderEventFilled, Order: o, Fills: part.Fills, At: s.d.Now().UTC()})

	s.d.Logger.Info(ctx, "Paper resting order filled", "workflow_id", o.WorkflowID, "order_id", o.ID,
		"shares", take, "price", o.Price, "status", o.Status)

	return take, nil
}

// endPaper ends a resting paper order, keeping what it filled; its held cash and shares
// are free again, as they are worked out from the orders still open.
func (s *Service) endPaper(ctx context.Context, id domain.OrderID, status domain.OrderStatus, why string) error {
	o, err := s.d.Ledger.Order(ctx, id)
	if err != nil {
		return err
	}

	unlock := s.lock(o.WorkflowID)
	defer unlock()

	return s.endPaperLocked(ctx, id, status, why)
}

func (s *Service) endPaperLocked(ctx context.Context, id domain.OrderID, status domain.OrderStatus, why string) error {
	o, err := s.d.Ledger.Order(ctx, id)
	if err != nil || o.Status != domain.OrderOpen {
		return err
	}

	before := o
	o.Status, o.Reason = status, why+", "+filledOf(o)

	if err := s.d.Ledger.Update(ctx, o, nil); err != nil {
		return err
	}

	s.publish(ctx, s.changes(before, o)...)

	s.d.Logger.Info(ctx, "Paper resting order ended", "workflow_id", o.WorkflowID, "order_id", o.ID, "status", status)

	return nil
}

// WatchLive follows the live orders resting on the book (3p3a) until ctx is done: as the
// user channel of their wallet says they changed (D195), each connection reading them
// all again, and every so often anyway.
func (s *Service) WatchLive(ctx context.Context, every time.Duration) {
	if s.d.Exchange == nil || s.d.Keys == nil {
		return
	}

	t := time.NewTicker(every)
	defer t.Stop()

	type change struct {
		wallet domain.WalletID
		event  ports.LiveOrderEvent
	}

	var (
		changes = make(chan change, 64)
		streams = map[domain.WalletID]context.CancelFunc{}
		beats   = map[domain.WalletID]context.CancelFunc{}
	)

	defer func() {
		for _, stop := range streams {
			stop()
		}

		for _, stop := range beats {
			stop()
		}
	}()

	watch := func(open []domain.PlacedOrder) {
		wanted := map[domain.WalletID]domain.UserID{}
		for _, o := range open {
			wanted[o.WalletID] = o.OwnerID
		}

		// A wallet with nothing resting is no longer watched; its heartbeats stop, and
		// Polymarket's switch has nothing to cancel.
		for _, running := range []map[domain.WalletID]context.CancelFunc{streams, beats} {
			for w, stop := range running {
				if _, ok := wanted[w]; !ok {
					stop()
					delete(running, w)
				}
			}
		}

		for w, owner := range wanted {
			_, streaming := streams[w]
			_, beating := beats[w]

			if streaming && beating {
				continue
			}

			k, err := s.d.Keys.AccessKey(ctx, owner, w) // no signing key: none is needed (R16)
			if err != nil {
				s.d.Logger.Warn(ctx, "Live orders of a wallet not watched", "wallet_id", w, logging.Err(err))
				continue
			}

			if !streaming && s.d.Stream != nil {
				wctx, stop := context.WithCancel(ctx)
				streams[w] = stop

				go func() {
					for e := range s.d.Stream.Watch(wctx, k) {
						select {
						case changes <- change{wallet: w, event: e}:
						case <-wctx.Done():
							return
						}
					}
				}()
			}

			// Once beating, it beats while orders rest, the setting off or not: stopping
			// would have Polymarket cancel them (D214).
			if !beating && k.Wallet.Heartbeat {
				bctx, stop := context.WithCancel(ctx)
				beats[w] = stop

				go s.beat(bctx, k)
			}
		}
	}

	// follow reads again the open orders of the wallet, of those named, or all.
	follow := func(wallet domain.WalletID, named []string) {
		open, err := s.d.Ledger.OpenOrders(ctx, domain.ModeLive)
		if err != nil {
			s.d.Logger.Warn(ctx, "Live resting orders not followed", logging.Err(err))
			return
		}

		watch(open)

		if wallet == "" {
			s.stopped(ctx, open)
		}

		for _, o := range open {
			if (wallet != "" && o.WalletID != wallet) || (named != nil && !slices.Contains(named, o.ClobID)) {
				continue
			}

			if err := s.follow(ctx, o); err != nil && ctx.Err() == nil {
				s.d.Logger.Warn(ctx, "Live resting order not followed", "order_id", o.ID, logging.Err(err))
			}
		}
	}

	for {
		follow("", nil)

		for wait := true; wait; {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				wait = false
			case <-s.nudges:
				wait = false
			case c := <-changes:
				if c.event.Gap {
					follow(c.wallet, nil)
				} else {
					follow(c.wallet, c.event.Orders)
				}
			}
		}
	}
}

// heartbeatEvery is how often a wallet's heartbeat goes: Polymarket cancels its orders
// once none came for 10 s.
const heartbeatEvery = 5 * time.Second

// beat sends the wallet's heartbeats until ctx is done (D214).
func (s *Service) beat(ctx context.Context, k ports.OrderKey) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()

	last := ""

	for {
		next, err := s.d.Exchange.Heartbeat(ctx, k, last)

		switch {
		case err == nil:
			if last == "" {
				s.d.Logger.Info(ctx, "Heartbeats started: Polymarket cancels the wallet's resting orders if they stop",
					"wallet_id", k.Wallet.ID)
			}

			last = next
		case ctx.Err() == nil:
			s.d.Logger.Warn(ctx, "Heartbeat not sent", "wallet_id", k.Wallet.ID, logging.Err(err))
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Cancel cancels what is left of one of a workflow's resting orders, for its owner: on
// paper at once; live, Polymarket cancels it, and what it filled is read first.
func (s *Service) Cancel(ctx context.Context, p auth.Principal, w domain.WorkflowID, id domain.OrderID) (domain.PlacedOrder, error) {
	if _, err := s.d.Workflows.Get(ctx, p, w); err != nil {
		return domain.PlacedOrder{}, err
	}

	o, err := s.d.Ledger.Order(ctx, id)
	if err != nil || o.WorkflowID != w {
		return domain.PlacedOrder{}, cmp.Or(err, domain.ErrNotFound)
	}

	if o.Status != domain.OrderOpen {
		return domain.PlacedOrder{}, domain.ErrNotOpen
	}

	if err := p.Need(o.Mode.Scope()); err != nil {
		return domain.PlacedOrder{}, err
	}

	if o.Mode == domain.ModePaper {
		if err := s.endPaper(ctx, id, domain.OrderCanceled, "cancelled by its owner"); err != nil {
			return domain.PlacedOrder{}, err
		}

		return s.d.Ledger.Order(ctx, id)
	}

	got, _, err := s.cancelLive(ctx, o, "cancelled by its owner")

	return got, err
}

// cancelLive has Polymarket cancel what is left of a live resting order, then reads it
// again, what it filled before included: the order as it is now, and why Polymarket did
// not cancel it, when it did not.
func (s *Service) cancelLive(ctx context.Context, o domain.PlacedOrder, why string) (domain.PlacedOrder, string, error) {
	if s.d.Exchange == nil || s.d.Keys == nil {
		return domain.PlacedOrder{}, "", domain.ErrLiveTradingOff
	}

	k, err := s.d.Keys.AccessKey(ctx, o.OwnerID, o.WalletID)
	if err != nil {
		return domain.PlacedOrder{}, "", err
	}

	c, err := s.d.Exchange.Cancel(ctx, k, o.ClobID)
	if err != nil {
		return domain.PlacedOrder{}, "", err
	}

	if !c.Canceled {
		s.d.Logger.Info(ctx, "Live order not cancelled", "order_id", o.ID, "reason", c.Reason)
	}

	if err := s.follow(ctx, o); err != nil {
		return domain.PlacedOrder{}, "", err
	}

	unlock := s.lock(o.WorkflowID)
	defer unlock()

	got, err := s.d.Ledger.Order(ctx, o.ID)
	if err != nil {
		return domain.PlacedOrder{}, "", err
	}

	// Cancelled as asked, it says why; still open, the CLOB's registry lags.
	if c.Canceled && (got.Status == domain.OrderOpen || got.Status == domain.OrderCanceled) {
		before := got
		got.Status, got.Reason = domain.OrderCanceled, why+", "+filledOf(got)

		if !unchanged(before, got) {
			if err := s.d.Ledger.Update(ctx, got, nil); err != nil {
				return domain.PlacedOrder{}, "", err
			}

			s.publish(ctx, s.changes(before, got)...)
		}
	}

	if c.Canceled {
		s.d.Logger.Info(ctx, "Live order cancelled", "workflow_id", o.WorkflowID, "order_id", o.ID, "status", got.Status)

		return got, "", nil
	}

	return got, c.Reason, nil
}

// CancelOrders cancels the workflow's own resting orders a request names (D207), on paper
// and live: how each went.
func (s *Service) CancelOrders(ctx context.Context, c ports.CancelRequest) ([]ports.CancelResult, error) {
	var open []domain.PlacedOrder

	for _, mode := range []domain.TradingMode{domain.ModePaper, domain.ModeLive} {
		got, err := s.d.Ledger.WorkflowOpenOrders(ctx, c.WorkflowID, mode)
		if err != nil {
			return nil, err
		}

		open = append(open, got...)
	}

	if c.OrderID != "" && !slices.ContainsFunc(open, func(o domain.PlacedOrder) bool { return o.ID == c.OrderID }) {
		o, err := s.d.Ledger.Order(ctx, c.OrderID)
		if errors.Is(err, domain.ErrNotFound) || (err == nil && o.WorkflowID != c.WorkflowID) {
			return []ports.CancelResult{{Order: domain.PlacedOrder{ID: c.OrderID}, Reason: "the workflow has no such order"}}, nil
		}

		if err != nil {
			return nil, err
		}

		return []ports.CancelResult{{Order: o, Reason: "it no longer rests on the book: " + string(o.Status)}}, nil
	}

	var out []ports.CancelResult

	for _, o := range open {
		switch {
		case c.OrderID != "" && o.ID != c.OrderID,
			c.MarketID != "" && o.MarketID != c.MarketID,
			c.TokenID != "" && o.TokenID != c.TokenID,
			c.OrderID == "" && c.MarketID == "" && c.TokenID == "" && !c.All:
			continue
		}

		r, err := s.cancelOne(ctx, o, "cancelled by Cancel orders")
		if err != nil {
			return out, err
		}

		out = append(out, r)
	}

	return out, nil
}

func (s *Service) cancelOne(ctx context.Context, o domain.PlacedOrder, why string) (ports.CancelResult, error) {
	if o.Mode == domain.ModePaper {
		if err := s.endPaper(ctx, o.ID, domain.OrderCanceled, why); err != nil {
			return ports.CancelResult{}, err
		}

		got, err := s.d.Ledger.Order(ctx, o.ID)

		return ports.CancelResult{Order: got}, err
	}

	got, reason, err := s.cancelLive(ctx, o, why)

	return ports.CancelResult{Order: got, Reason: reason}, err
}

// CancelResting cancels a workflow's live resting orders (D213, D217): when it goes back
// to paper, is deleted, is switched off and its owner asks, or its wallet is unlinked.
// It says how many still rest: those Polymarket would not or could not cancel.
func (s *Service) CancelResting(ctx context.Context, w domain.WorkflowID, why string) (int, error) {
	open, err := s.d.Ledger.WorkflowOpenOrders(ctx, w, domain.ModeLive)
	if err != nil {
		return 0, err
	}

	_, left, err := s.cancelEach(ctx, open, why)
	if left > 0 {
		s.d.Logger.Warn(ctx, "Live resting orders not all cancelled", "workflow_id", w, "left", left, logging.Err(err))
	}

	return left, err
}

func (s *Service) nudge() {
	select {
	case s.nudges <- struct{}{}:
	default:
	}
}

// endPaperOrders ends a workflow's resting paper orders, as when its account starts over.
func (s *Service) endPaperOrders(ctx context.Context, w domain.WorkflowID, why string) error {
	open, err := s.d.Ledger.WorkflowOpenOrders(ctx, w, domain.ModePaper)
	if err != nil {
		return err
	}

	for _, o := range open {
		if err := s.endPaperLocked(ctx, o.ID, domain.OrderCanceled, why); err != nil {
			return err
		}
	}

	return nil
}
