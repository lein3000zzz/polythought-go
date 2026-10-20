// Package trading places workflows' orders and keeps their books. On paper (D89 to D92),
// an order fills at once against the live order book, as the CLOB would fill it, paying
// the market's taker fees, out of a paper account of the workflow's own; live (3p2), it
// goes to Polymarket's CLOB from the workflow's wallet. Every order is in the ledger,
// refused ones with why.
package trading

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

var _ ports.OrderPlacer = (*Service)(nil)

// Workflows finds a user's workflow, for showing its books to its owner only.
type Workflows interface {
	Get(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.Workflow, error)
}

type Deps struct {
	Ledger    ports.Ledger
	Books     ports.OrderBookReader
	Rules     ports.TradingRulesReader
	Markets   ports.MarketReader // a token's market, for orders that do not name it
	Workflows Workflows
	Logger    logging.Logger

	// PaperBalance is what a paper account starts with, read when one opens.
	PaperBalance func() decimal.Decimal
	Now          func() time.Time

	// Live orders (3p2); without Exchange and Keys, workflows trade on paper only.
	Trading  ports.WorkflowTradingRepository
	Exchange ports.LiveExchange
	Keys     ports.OrderKeys
	Wallets  ports.WalletDirectory   // the wallet a workflow trades from, for its owner
	Notifier ports.LiveTradeNotifier // optional: who is told of a failed live trade

	// LiveOn says whether the instance takes live orders now (D192); BuilderCode is the
	// builder code they carry, empty for none (D203).
	LiveOn      func() bool
	BuilderCode func() string

	// Users are the owners' kill switches and caps (D220, D222), Caps the instance's
	// caps (D224); without them, nothing is stopped or capped.
	Users ports.LiveUsers
	Caps  func() domain.LiveCaps

	// Notices hears what happens to orders, to tell their owners (D229); optional.
	Notices ports.OrderNotifier

	// Patience is how long a placed order is waited for, looked at every Poll (D204).
	Patience, Poll time.Duration

	// Resting orders (3p3a): optional; without Feed, paper ones rest but never fill,
	// without Stream, live ones are read every so often only.
	Feed   ports.MarketFeed
	Stream ports.LiveOrderStream
}

type Service struct {
	d Deps

	mu        sync.Mutex
	locks     map[domain.WorkflowID]*sync.Mutex
	listeners map[domain.WorkflowID]map[chan domain.OrderEvent]struct{} // Order activity (D211)
	sending   map[domain.UserID]*sending                                // live buys let through (D223)
	inFlight  map[domain.OrderID]struct{}                               // live orders on their way (D233)

	nudges chan struct{} // a live order now rests: WatchLive looks at once
}

func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}

	if d.Poll <= 0 {
		d.Poll = defaultPoll
	}

	if d.Patience <= 0 {
		d.Patience = defaultPatience
	}

	return &Service{d: d, locks: make(map[domain.WorkflowID]*sync.Mutex), nudges: make(chan struct{}, 1)}
}

// Place tries an order, in the mode the workflow trades in now, and records how it went.
// One workflow's orders go one at a time: each sees the cash and shares the one before
// left. Its owner is told how it went, as they chose (D229).
func (s *Service) Place(ctx context.Context, req domain.OrderRequest) (domain.PlacedOrder, error) {
	o, err := s.place(ctx, req)
	if err == nil {
		s.tell(placed(o, s.d.Now().UTC())...)
	}

	return o, err
}

func (s *Service) place(ctx context.Context, req domain.OrderRequest) (domain.PlacedOrder, error) {
	unlock := s.lock(req.WorkflowID)
	defer unlock()

	t, err := s.tradingOf(ctx, req.WorkflowID)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	req.Mode = t.Mode

	req, reason := request(req)
	o := domain.PlacedOrder{OrderRequest: req, ID: domain.OrderID(uuid.Must(uuid.NewV7()).String()), CreatedAt: s.d.Now().UTC()}

	if reason == "" {
		o, reason = s.asResting(o)
	}

	if reason != "" {
		return s.refuse(ctx, o, reason)
	}

	if o.MarketID == "" {
		market, err := s.marketOf(ctx, o.TokenID)
		if errors.Is(err, domain.ErrNotFound) {
			return s.refuse(ctx, o, "no market trades this token")
		}

		if err != nil {
			return domain.PlacedOrder{}, err
		}

		o.MarketID = market
	}

	rules, err := s.d.Rules.TradingRules(ctx, o.MarketID)
	if err != nil {
		return domain.PlacedOrder{}, fmt.Errorf("the market's rules: %w", err)
	}

	if reason := keepsRules(o.OrderRequest, rules); reason != "" {
		return s.refuse(ctx, o, reason)
	}

	if o.Mode == domain.ModeLive {
		return s.placeLive(ctx, o, rules, t)
	}

	key := domain.HoldingKey{WorkflowID: o.WorkflowID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}

	held, err := s.d.Ledger.Holding(ctx, key)
	if errors.Is(err, domain.ErrNotFound) {
		held = domain.Holding{WorkflowID: o.WorkflowID, Mode: o.Mode, Trader: o.Trader, TokenID: o.TokenID}
	} else if err != nil {
		return domain.PlacedOrder{}, err
	}

	held.OwnerID = o.OwnerID

	if o.Side == domain.SideSell && o.Shares.GreaterThan(held.Shares) {
		return s.refuse(ctx, o, fmt.Sprintf("not enough shares: %s to sell, %s held%s", o.Shares, held.Shares, copied(o)))
	}

	account, err := s.account(ctx, o)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	if o.Type.Rests() {
		return s.placePaperResting(ctx, o, rules, held, account)
	}

	// Resting orders hold back cash and shares (D210).
	holding, selling, err := s.committed(ctx, o.WorkflowID, o.Mode)
	if err != nil {
		return domain.PlacedOrder{}, err
	}

	if free := held.Shares.Sub(selling[held.Key()]); o.Side == domain.SideSell && o.Shares.GreaterThan(free) {
		return s.refuse(ctx, o, fmt.Sprintf("not enough shares: %s to sell, %s held%s", o.Shares, free, restingSells(selling[held.Key()], o)))
	}

	// As the CLOB checks a balance: against all the order may spend, before it matches.
	free := account.Cash.Sub(holding)
	if need := o.Dollars.Add(o.Shares.Mul(o.Price)); o.Side == domain.SideBuy && need.GreaterThan(free) {
		left := "left"
		if holding.IsPositive() {
			left = "free, the rest held by resting orders"
		}

		return s.refuse(ctx, o, fmt.Sprintf("not enough paper cash: $%s for it, $%s %s",
			need.StringFixed(2), free.StringFixed(2), left))
	}

	books, err := s.d.Books.OrderBooks(ctx, o.TokenID)
	if err != nil {
		return domain.PlacedOrder{}, fmt.Errorf("the order book: %w", err)
	}

	var book domain.OrderBook
	if len(books) > 0 {
		book = books[0]
	}

	o.Fills, o.Status, o.Reason = fill(o.OrderRequest, book, rules.Fees, o.CreatedAt)
	if len(o.Fills) == 0 {
		return o, s.d.Ledger.Record(ctx, o, nil)
	}

	cash, after := apply(o, held)
	if cash.Add(account.Cash).IsNegative() { // the fees on top
		return s.refuse(ctx, o, fmt.Sprintf("not enough paper cash: $%s for it, $%s left",
			cash.Neg().StringFixed(2), account.Cash.StringFixed(2)))
	}

	err = s.d.Ledger.Record(ctx, o, &ports.LedgerChange{Cash: cash, Holding: after})
	if errors.Is(err, domain.ErrNotEnoughCash) || errors.Is(err, domain.ErrNotEnoughShares) {
		return s.refuse(ctx, o, "the paper account changed under it") // only another process could
	}

	if err != nil {
		return domain.PlacedOrder{}, err
	}

	s.d.Logger.Info(ctx, "Paper order filled", "workflow_id", o.WorkflowID, "order_id", o.ID, "side", o.Side,
		"status", o.Status, "token_id", o.TokenID, "cash", cash)

	return o, nil
}

func (s *Service) refuse(ctx context.Context, o domain.PlacedOrder, reason string) (domain.PlacedOrder, error) {
	o.Status, o.Reason, o.Fills = domain.OrderRefused, reason, nil

	if err := s.d.Ledger.Record(ctx, o, nil); err != nil {
		return domain.PlacedOrder{}, err
	}

	return o, nil
}

// account is the workflow's paper account, opened with the starting balance by its
// first order that keeps the market's rules (D89).
func (s *Service) account(ctx context.Context, o domain.PlacedOrder) (domain.PaperAccount, error) {
	a, err := s.d.Ledger.PaperAccount(ctx, o.WorkflowID)
	if !errors.Is(err, domain.ErrNotFound) {
		return a, err
	}

	balance := s.d.PaperBalance()

	return s.d.Ledger.OpenPaperAccount(ctx, domain.PaperAccount{
		WorkflowID: o.WorkflowID, OwnerID: o.OwnerID, Cash: balance, StartedWith: balance, StartedAt: o.CreatedAt,
	})
}

func (s *Service) marketOf(ctx context.Context, token domain.TokenID) (domain.MarketID, error) {
	page, err := s.d.Markets.ListMarkets(ctx, ports.MarketFilter{TokenIDs: []domain.TokenID{token}, Limit: 1})
	if err != nil {
		return "", err
	}

	if len(page.Items) == 0 {
		return "", domain.ErrNotFound
	}

	return page.Items[0].ID, nil
}

func (s *Service) lock(w domain.WorkflowID) func() {
	s.mu.Lock()
	l, ok := s.locks[w]
	if !ok {
		l = &sync.Mutex{}
		s.locks[w] = l
	}
	s.mu.Unlock()

	l.Lock()

	return l.Unlock
}

func copied(o domain.PlacedOrder) string {
	if o.TraderLabel == "" {
		return ""
	}

	return " copying " + o.TraderLabel // each trader's copies are held apart (D92)
}

// PaperAccount is a workflow's paper account, if it has one yet, and what one starts with.
func (s *Service) PaperAccount(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.PaperAccount, bool, decimal.Decimal, error) {
	if _, err := s.d.Workflows.Get(ctx, p, id); err != nil {
		return domain.PaperAccount{}, false, decimal.Zero, err
	}

	a, err := s.d.Ledger.PaperAccount(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.PaperAccount{}, false, s.d.PaperBalance(), nil
	}

	return a, err == nil, s.d.PaperBalance(), err
}

// Orders lists a workflow's orders, the newest first, for its owner.
func (s *Service) Orders(ctx context.Context, p auth.Principal, id domain.WorkflowID, f ports.PageFilter) (ports.Page[domain.PlacedOrder], error) {
	if _, err := s.d.Workflows.Get(ctx, p, id); err != nil {
		return ports.Page[domain.PlacedOrder]{}, err
	}

	return s.d.Ledger.Orders(ctx, id, f)
}

// Holdings lists a workflow's positions, for its owner.
func (s *Service) Holdings(ctx context.Context, p auth.Principal, id domain.WorkflowID) ([]domain.Holding, error) {
	if _, err := s.d.Workflows.Get(ctx, p, id); err != nil {
		return nil, err
	}

	return s.d.Ledger.Holdings(ctx, id)
}
