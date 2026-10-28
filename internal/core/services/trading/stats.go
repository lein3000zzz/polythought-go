package trading

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// The workflows' results together, for the stats (D429).

const (
	pricesKept = 4096        // tokens' prices kept for the stats
	priceFor   = time.Minute // how long one is
)

// price is a token's mark, as the trading page puts one on a holding (D105): the book's
// midpoint, else the market's own price for the outcome; Known false when neither is.
type price struct {
	Value decimal.Decimal
	Known bool
	At    time.Time
}

// Result is how a workflow's trading went in the mode it trades in now, for the stats
// across workflows (D429): its profit, as its trading page has it, made and still held;
// its open positions; its traders' totals. No cash is read, so no wallet nor chain asked.
type Result struct {
	Mode domain.TradingMode
	// Since is what it counts from: on paper its account's start or last reset; live, the
	// zero time, its results never starting over (D430).
	Since      time.Time
	Profit     decimal.Decimal
	Realized   decimal.Decimal
	Unrealized decimal.Decimal
	Positions  int // open holdings
	Unpriced   int // of them, valued at their cost: no price known now
	Volume     decimal.Decimal
	Orders     int // that filled
	Wins       int // closed holdings that made money
	Closed     int // holdings sold out of, or settled
}

// TraderResult is what copying one trader came to across the workflows that copy them, in
// one mode (D429): the workflows' results as Result has them, by trader. An empty Trader
// is the orders copied from no one.
type TraderResult struct {
	Mode       domain.TradingMode
	Trader     domain.WalletAddress
	Label      string // as of their latest holding
	Workflows  int    // that copy them in the mode
	Profit     decimal.Decimal
	Realized   decimal.Decimal
	Unrealized decimal.Decimal
	Positions  int
	Volume     decimal.Decimal
	Orders     int
	Wins       int
	Closed     int
}

// Results are each workflow's result, and each copied trader's across them.
type Results struct {
	Workflows map[domain.WorkflowID]Result
	Traders   []TraderResult // the most profitable first; copied from no one last
}

// Results are how each of owner's workflows went that has traded in the mode it trades
// in now, one that never did having none, and what copying each trader came to across
// them, in the same modes. The ledger is asked once for all of them, each part, and the
// market once for each token, its prices kept a minute.
func (s *Service) Results(ctx context.Context, owner domain.UserID) (Results, error) {
	// Without the repository, every workflow trades on paper, as tradingOf has it.
	modes := map[domain.WorkflowID]domain.TradingMode{}

	if s.d.Trading != nil {
		setups, err := s.d.Trading.OwnerTrading(ctx, owner)
		if err != nil {
			return Results{}, err
		}

		for _, t := range setups {
			modes[t.WorkflowID] = t.Mode
		}
	}

	mode := func(id domain.WorkflowID) domain.TradingMode {
		if m := modes[id]; m != "" {
			return m
		}

		return domain.ModePaper
	}

	accounts, err := s.d.Ledger.OwnerPaperAccounts(ctx, owner)
	if err != nil {
		return Results{}, err
	}

	holdings, err := s.d.Ledger.OwnerHoldings(ctx, owner)
	if err != nil {
		return Results{}, err
	}

	totals, err := s.d.Ledger.OwnerTraderTotals(ctx, owner)
	if err != nil {
		return Results{}, err
	}

	var open []domain.Holding

	for _, h := range holdings {
		if h.Mode == mode(h.WorkflowID) && h.Shares.IsPositive() {
			open = append(open, h)
		}
	}

	prices := s.marks(ctx, open)

	out := map[domain.WorkflowID]Result{}
	paper := map[domain.WorkflowID]domain.PaperAccount{}

	for _, a := range accounts {
		if mode(a.WorkflowID) == domain.ModePaper {
			since := a.StartedAt
			if !a.ResetAt.IsZero() {
				since = a.ResetAt
			}

			out[a.WorkflowID], paper[a.WorkflowID] = Result{Mode: domain.ModePaper, Since: since}, a
		}
	}

	// result is the workflow's to add to: on paper, once it has an account; live, once
	// it holds or has traded.
	result := func(id domain.WorkflowID, m domain.TradingMode) (Result, bool) {
		if r, ok := out[id]; ok {
			return r, true
		}

		return Result{Mode: m}, m == domain.ModeLive
	}

	worth := map[domain.WorkflowID]decimal.Decimal{} // the open holdings', at their marks
	traders := newTraders()

	for _, h := range holdings {
		m := mode(h.WorkflowID)
		if h.Mode != m {
			continue
		}

		r, ok := result(h.WorkflowID, m)
		if !ok {
			continue
		}

		t := traders.of(h.WorkflowID, m, h.Trader)
		if h.TraderLabel != "" && !h.UpdatedAt.Before(t.labelled) {
			t.Label, t.labelled = h.TraderLabel, h.UpdatedAt
		}

		r.Realized = r.Realized.Add(h.Realized)
		t.Realized = t.Realized.Add(h.Realized)

		if h.Shares.IsPositive() {
			value := h.Cost
			if p := prices[h.TokenID]; p.Known {
				value = h.Shares.Mul(p.Value).Round(6)
			} else {
				r.Unpriced++
			}

			r.Unrealized = r.Unrealized.Add(value.Sub(h.Cost))
			t.Unrealized = t.Unrealized.Add(value.Sub(h.Cost))
			r.Positions++
			t.Positions++
			worth[h.WorkflowID] = worth[h.WorkflowID].Add(value)
		} else {
			r.Closed++
			t.Closed++

			if h.Realized.IsPositive() {
				r.Wins++
				t.Wins++
			}
		}

		out[h.WorkflowID] = r
	}

	for _, tt := range totals {
		m := mode(tt.WorkflowID)
		if tt.Mode != m {
			continue
		}

		r, ok := result(tt.WorkflowID, m)
		if !ok {
			continue
		}

		t := traders.of(tt.WorkflowID, m, tt.Trader)

		r.Orders += tt.Orders
		t.Orders += tt.Orders
		r.Volume = r.Volume.Add(tt.Volume)
		t.Volume = t.Volume.Add(tt.Volume)
		out[tt.WorkflowID] = r
	}

	for id, r := range out {
		r.Profit = r.Realized.Add(r.Unrealized)

		// On paper, its worth less what it started with, as its trading page has it.
		if a, ok := paper[id]; ok {
			r.Profit = a.Cash.Add(worth[id]).Sub(a.StartedWith)
		}

		out[id] = r
	}

	return Results{Workflows: out, Traders: traders.list()}, nil
}

// traderKey is a trader copied in a mode.
type traderKey struct {
	mode   domain.TradingMode
	trader domain.WalletAddress
}

// tradersSum adds up each trader's results across the workflows copying them.
type tradersSum struct {
	by        map[traderKey]*traderSum
	workflows map[traderKey]map[domain.WorkflowID]bool
}

type traderSum struct {
	TraderResult

	labelled time.Time // when Label was the holding's
}

func newTraders() *tradersSum {
	return &tradersSum{by: map[traderKey]*traderSum{}, workflows: map[traderKey]map[domain.WorkflowID]bool{}}
}

// of is trader's sum in mode, counting the workflow as one copying them.
func (ts *tradersSum) of(w domain.WorkflowID, mode domain.TradingMode, trader domain.WalletAddress) *traderSum {
	k := traderKey{mode, trader}

	t, ok := ts.by[k]
	if !ok {
		t = &traderSum{TraderResult: TraderResult{Mode: mode, Trader: trader}}
		ts.by[k], ts.workflows[k] = t, map[domain.WorkflowID]bool{}
	}

	ts.workflows[k][w] = true

	return t
}

// list is the sums, the most profitable first, copied from no one last.
func (ts *tradersSum) list() []TraderResult {
	out := make([]TraderResult, 0, len(ts.by))

	for k, t := range ts.by {
		r := t.TraderResult
		r.Workflows = len(ts.workflows[k])
		r.Profit = r.Realized.Add(r.Unrealized)
		out = append(out, r)
	}

	slices.SortFunc(out, func(a, b TraderResult) int {
		if (a.Trader == "") != (b.Trader == "") {
			return cmp.Compare(boolInt(a.Trader == ""), boolInt(b.Trader == ""))
		}

		return cmp.Or(b.Profit.Cmp(a.Profit), strings.Compare(string(a.Mode), string(b.Mode)), strings.Compare(string(a.Trader), string(b.Trader)))
	})

	return out
}

// marks prices the holdings' tokens as mark does, kept a minute: the books' midpoints,
// and for those with none, their markets' prices. The market failing leaves prices
// unknown, those holdings at their cost, rather than no stats; nothing is kept then.
func (s *Service) marks(ctx context.Context, open []domain.Holding) map[domain.TokenID]price {
	now := s.d.Now()
	out := map[domain.TokenID]price{}
	marketOf := map[domain.TokenID]domain.MarketID{}

	var missing []domain.TokenID

	for _, h := range open {
		if _, seen := marketOf[h.TokenID]; seen {
			continue
		}

		marketOf[h.TokenID] = h.MarketID

		if p, ok := s.prices.Get(h.TokenID); ok && now.Sub(p.At) < priceFor {
			out[h.TokenID] = p
			continue
		}

		missing = append(missing, h.TokenID)
	}

	if len(missing) == 0 {
		return out
	}

	books, err := s.d.Books.OrderBooks(ctx, missing...)
	if err != nil {
		s.d.Logger.Warn(ctx, "The stats' holdings not priced: valued at their cost", logging.Err(err))
		return out
	}

	for _, b := range books {
		if mid, ok := b.Mid(); ok {
			out[b.TokenID] = price{Value: mid, Known: true, At: now}
			s.prices.Put(b.TokenID, out[b.TokenID])
		}
	}

	markets := map[domain.MarketID]domain.Market{}

	for _, token := range missing {
		if _, ok := out[token]; ok {
			continue
		}

		p := price{At: now}

		if id := marketOf[token]; id != "" {
			m, ok := markets[id]
			if !ok {
				got, err := s.d.Markets.GetMarket(ctx, id)
				if err != nil && !errors.Is(err, domain.ErrNotFound) {
					s.d.Logger.Warn(ctx, "A holding's market not read: valued at its cost", "market_id", id, logging.Err(err))
					continue
				}

				m, markets[id] = got, got
			}

			if o, found := m.Outcome(token); found && o.Price.IsPositive() {
				p.Value, p.Known = o.Price, true
			}
		}

		out[token] = p
		s.prices.Put(token, p)
	}

	return out
}
