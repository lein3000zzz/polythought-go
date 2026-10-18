package trading

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Summary is how a workflow's trading went (D104): its paper account, each trader it
// copies, and what it holds, marked to market.
type Summary struct {
	Account  AccountSummary
	Traders  []TraderSummary
	Holdings []HoldingSummary // open ones
}

type AccountSummary struct {
	Open        bool // false until the first order; the rest is then what it would start with
	Cash        decimal.Decimal
	StartedWith decimal.Decimal
	Worth       decimal.Decimal // cash and what its holdings are worth now
	Profit      decimal.Decimal // worth less what it started with
	StartedAt   time.Time
	ResetAt     time.Time
}

// TraderSummary is what copying one trader came to since the account (re)started; an
// empty Trader is the orders copied from no one.
type TraderSummary struct {
	Trader     domain.WalletAddress
	Label      string
	Copies     int             // orders that filled
	Volume     decimal.Decimal // the dollars they traded
	Realized   decimal.Decimal
	Unrealized decimal.Decimal
	Wins       int // closed holdings that made money
	Closed     int // holdings sold out of, or settled
}

type HoldingSummary struct {
	domain.Holding

	Question string
	Outcome  string
	Mark     *decimal.Decimal // a share's price now; nil when nobody knows
	Value    decimal.Decimal  // the shares at the mark; their cost when unknown
}

// Unrealized is what a holding would make, sold at its mark.
func (h HoldingSummary) Unrealized() decimal.Decimal {
	return h.Value.Sub(h.Cost)
}

// Summary is a workflow's results, for its owner.
func (s *Service) Summary(ctx context.Context, p auth.Principal, id domain.WorkflowID) (Summary, error) {
	if _, err := s.d.Workflows.Get(ctx, p, id); err != nil {
		return Summary{}, err
	}

	var out Summary

	a, err := s.d.Ledger.PaperAccount(ctx, id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		balance := s.d.PaperBalance()
		out.Account = AccountSummary{Cash: balance, StartedWith: balance, Worth: balance}

		return out, nil
	case err != nil:
		return Summary{}, err
	}

	holdings, err := s.d.Ledger.Holdings(ctx, id)
	if err != nil {
		return Summary{}, err
	}

	totals, err := s.d.Ledger.TraderTotals(ctx, id, domain.ModePaper, a.ResetAt) // zero when never reset: all
	if err != nil {
		return Summary{}, err
	}

	marked, err := s.mark(ctx, holdings)
	if err != nil {
		return Summary{}, err
	}

	worth := a.Cash
	for _, h := range marked {
		worth = worth.Add(h.Value)
	}

	out.Account = AccountSummary{
		Open: true, Cash: a.Cash, StartedWith: a.StartedWith, Worth: worth, Profit: worth.Sub(a.StartedWith),
		StartedAt: a.StartedAt, ResetAt: a.ResetAt,
	}
	out.Holdings = marked
	out.Traders = traderSummaries(totals, holdings, marked)

	return out, nil
}

// mark puts a price on each open paper holding (D105): the book's midpoint, or with a
// one-sided or empty book, the market's own price for the outcome.
func (s *Service) mark(ctx context.Context, holdings []domain.Holding) ([]HoldingSummary, error) {
	var (
		open    []HoldingSummary
		tokens  []domain.TokenID
		markets = map[domain.MarketID]domain.Market{}
	)

	for _, h := range holdings {
		if h.Mode != domain.ModePaper || !h.Shares.IsPositive() {
			continue
		}

		open = append(open, HoldingSummary{Holding: h, Value: h.Cost})

		if !slices.Contains(tokens, h.TokenID) {
			tokens = append(tokens, h.TokenID)
		}

		if _, ok := markets[h.MarketID]; !ok && h.MarketID != "" {
			m, err := s.d.Markets.GetMarket(ctx, h.MarketID)
			if err != nil && !errors.Is(err, domain.ErrNotFound) {
				return nil, fmt.Errorf("the market of a holding: %w", err)
			}

			markets[h.MarketID] = m
		}
	}

	if len(open) == 0 {
		return nil, nil
	}

	mids := map[domain.TokenID]decimal.Decimal{}

	books, err := s.d.Books.OrderBooks(ctx, tokens...)
	if err != nil {
		return nil, fmt.Errorf("the order books of the holdings: %w", err)
	}

	for _, b := range books {
		if mid, ok := b.Mid(); ok {
			mids[b.TokenID] = mid
		}
	}

	for i, h := range open {
		m := markets[h.MarketID]
		open[i].Question = m.Question

		price, ok := mids[h.TokenID]
		if o, found := m.Outcome(h.TokenID); found {
			open[i].Outcome = o.Label

			if !ok && o.Price.IsPositive() {
				price, ok = o.Price, true
			}
		}

		if ok {
			open[i].Mark = &price
			open[i].Value = h.Shares.Mul(price).Round(6)
		}
	}

	slices.SortFunc(open, func(a, b HoldingSummary) int {
		return cmp.Or(strings.Compare(a.TraderLabel, b.TraderLabel), strings.Compare(a.Question, b.Question))
	})

	return open, nil
}

// traderSummaries puts each trader's copies together: their orders, and their holdings,
// open and closed, since the account (re)started.
func traderSummaries(totals []domain.TraderTotal, holdings []domain.Holding, open []HoldingSummary) []TraderSummary {
	by := map[domain.WalletAddress]*TraderSummary{}
	get := func(w domain.WalletAddress) *TraderSummary {
		if t, ok := by[w]; ok {
			return t
		}

		t := &TraderSummary{Trader: w}
		by[w] = t

		return t
	}

	for _, t := range totals {
		s := get(t.Trader)
		s.Copies, s.Volume = t.Orders, t.Volume
	}

	for _, h := range holdings {
		if h.Mode != domain.ModePaper {
			continue
		}

		s := get(h.Trader)
		s.Realized = s.Realized.Add(h.Realized)

		if s.Label == "" {
			s.Label = h.TraderLabel
		}

		if !h.Shares.IsPositive() {
			s.Closed++

			if h.Realized.IsPositive() {
				s.Wins++
			}
		}
	}

	for _, h := range open {
		s := get(h.Trader)
		s.Unrealized = s.Unrealized.Add(h.Unrealized())
	}

	out := make([]TraderSummary, 0, len(by))
	for _, t := range by {
		out = append(out, *t)
	}

	// Copied from no one last.
	slices.SortFunc(out, func(a, b TraderSummary) int {
		if (a.Trader == "") != (b.Trader == "") {
			return cmp.Compare(boolInt(a.Trader == ""), boolInt(b.Trader == ""))
		}

		return cmp.Or(strings.Compare(a.Label, b.Label), strings.Compare(string(a.Trader), string(b.Trader)))
	})

	return out
}

func boolInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

// Reset starts a workflow's paper account over (D107): cash back to the Paper balance,
// holdings emptied, the orders before kept.
func (s *Service) Reset(ctx context.Context, p auth.Principal, id domain.WorkflowID) (domain.PaperAccount, error) {
	w, err := s.d.Workflows.Get(ctx, p, id)
	if err != nil {
		return domain.PaperAccount{}, err
	}

	unlock := s.lock(id)
	defer unlock()

	now, balance := s.d.Now().UTC(), s.d.PaperBalance()

	if _, err := s.d.Ledger.PaperAccount(ctx, id); errors.Is(err, domain.ErrNotFound) {
		return s.d.Ledger.OpenPaperAccount(ctx, domain.PaperAccount{
			WorkflowID: id, OwnerID: w.OwnerID, Cash: balance, StartedWith: balance, StartedAt: now,
		})
	} else if err != nil {
		return domain.PaperAccount{}, err
	}

	a := domain.PaperAccount{WorkflowID: id, OwnerID: w.OwnerID, Cash: balance, StartedWith: balance, StartedAt: now, ResetAt: now}
	if err := s.d.Ledger.ResetPaperAccount(ctx, a); err != nil {
		return domain.PaperAccount{}, err
	}

	s.d.Logger.Info(ctx, "Paper account reset", "workflow_id", id, "balance", balance)

	return a, nil
}

// SettleEvery checks for resolved markets now and then until ctx is done (D106).
func (s *Service) SettleEvery(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		if err := s.Settle(ctx); err != nil && ctx.Err() == nil {
			s.d.Logger.Warn(ctx, "Paper holdings not settled", logging.Err(err))
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Settle closes paper holdings in resolved markets at what a share pays (D106): a sell
// at the outcome's resolved price, no fee, in the ledger as an order of its own.
func (s *Service) Settle(ctx context.Context) error {
	open, err := s.d.Ledger.OpenHoldings(ctx, domain.ModePaper)
	if err != nil {
		return err
	}

	markets := map[domain.MarketID]*domain.Market{}

	var errs []error

	for _, h := range open {
		m, seen := markets[h.MarketID]
		if !seen {
			got, err := s.d.Markets.GetMarket(ctx, h.MarketID)
			if err != nil {
				errs = append(errs, fmt.Errorf("market %s: %w", h.MarketID, err))
			} else {
				m = &got
			}

			markets[h.MarketID] = m
		}

		if m == nil || m.Status != domain.MarketResolved {
			continue
		}

		o, ok := m.Outcome(h.TokenID)
		if !ok {
			errs = append(errs, fmt.Errorf("market %s has no outcome %s", h.MarketID, h.TokenID))
			continue
		}

		if err := s.settle(ctx, h.Key(), o, m.Question); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (s *Service) settle(ctx context.Context, key domain.HoldingKey, o domain.Outcome, question string) error {
	unlock := s.lock(key.WorkflowID)
	defer unlock()

	h, err := s.d.Ledger.Holding(ctx, key) // as it is now: an order may have sold it meanwhile
	if err != nil || !h.Shares.IsPositive() {
		return err
	}

	a, err := s.d.Ledger.PaperAccount(ctx, key.WorkflowID)
	if err != nil {
		return err
	}

	reason := fmt.Sprintf("%s resolved: %s pays $%s a share", question, o.Label, o.Price.StringFixed(2))
	order := domain.PlacedOrder{
		OrderRequest: domain.OrderRequest{
			WorkflowID: key.WorkflowID, OwnerID: a.OwnerID, Mode: domain.ModePaper, Trader: h.Trader,
			TraderLabel: h.TraderLabel, TokenID: h.TokenID, MarketID: h.MarketID, Side: domain.SideSell,
			Type: domain.OrderSettle, Price: o.Price, Shares: h.Shares,
		},
		ID: domain.OrderID(uuid.Must(uuid.NewV7()).String()), Status: domain.OrderFilled, Reason: reason,
		Fills:     []domain.OrderFill{{Price: o.Price, Shares: h.Shares, Dollars: h.Shares.Mul(o.Price), Fee: decimal.Zero}},
		CreatedAt: s.d.Now().UTC(),
	}

	cash, after := apply(order, h)
	if err := s.d.Ledger.Record(ctx, order, &ports.LedgerChange{Cash: cash, Holding: after}); err != nil {
		return err
	}

	s.d.Logger.Info(ctx, "Paper holding settled", "workflow_id", key.WorkflowID, "token_id", h.TokenID,
		"shares", h.Shares, "price", o.Price)

	return nil
}
