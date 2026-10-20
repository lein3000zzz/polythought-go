package ports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// OrderPlacer places a workflow's orders in its mode.
type OrderPlacer interface {
	// Place tries an order and records it in the ledger: filled, partly filled, unfilled
	// or refused, with why. The error is for an order that could not be tried at all.
	Place(ctx context.Context, req domain.OrderRequest) (domain.PlacedOrder, error)
}

// TradingRulesReader says what a market's orders must keep to and what its fills cost.
type TradingRulesReader interface {
	TradingRules(ctx context.Context, market domain.MarketID) (domain.TradingRules, error)
}

// Ledger keeps the orders workflows placed, their paper accounts and their holdings.
type Ledger interface {
	// PaperAccount is ErrNotFound until the workflow's first paper order.
	PaperAccount(ctx context.Context, workflow domain.WorkflowID) (domain.PaperAccount, error)
	// OpenPaperAccount opens a, unless the workflow has one already: the account it has.
	OpenPaperAccount(ctx context.Context, a domain.PaperAccount) (domain.PaperAccount, error)

	// Holding is ErrNotFound for a position never held.
	Holding(ctx context.Context, key domain.HoldingKey) (domain.Holding, error)
	Holdings(ctx context.Context, workflow domain.WorkflowID) ([]domain.Holding, error)

	// OpenHoldings are the positions in a mode with shares in them, every workflow's.
	OpenHoldings(ctx context.Context, mode domain.TradingMode) ([]domain.Holding, error)
	// TraderTotals are a workflow's filled orders in a mode since then, by trader;
	// settlements are not orders.
	TraderTotals(ctx context.Context, workflow domain.WorkflowID, mode domain.TradingMode, since time.Time) ([]domain.TraderTotal, error)
	// ResetPaperAccount starts a's workflow's account over as a has it, its paper
	// holdings emptied; its orders stay (D107).
	ResetPaperAccount(ctx context.Context, a domain.PaperAccount) error

	// Spent is what a workflow's buys in a mode spent since then, fees included.
	Spent(ctx context.Context, workflow domain.WorkflowID, mode domain.TradingMode, since time.Time) (decimal.Decimal, error)

	// Record adds an order with its fills and, with a change, what it did: in one
	// transaction, so the books always add up.
	Record(ctx context.Context, o domain.PlacedOrder, change *LedgerChange) error
	// Orders lists a workflow's orders, the newest first.
	Orders(ctx context.Context, workflow domain.WorkflowID, f PageFilter) (Page[domain.PlacedOrder], error)

	// OrdersOf are the orders of a holding, the oldest first: what it is made of.
	OrdersOf(ctx context.Context, key domain.HoldingKey) ([]domain.PlacedOrder, error)
	// Following are the live orders since then still pending, or with fills whose
	// trades are not final: what the follower looks at (D193, D204).
	Following(ctx context.Context, since time.Time) ([]domain.PlacedOrder, error)
	// Update replaces a recorded live order's status, reason and fills and, with a
	// holding, sets it as given, in one transaction.
	Update(ctx context.Context, o domain.PlacedOrder, holding *domain.Holding) error
}

// LedgerChange is what a filled order does to the books.
type LedgerChange struct {
	Cash    decimal.Decimal // added to the paper account; a buy's is negative
	Holding domain.Holding  // as the order leaves it
}
