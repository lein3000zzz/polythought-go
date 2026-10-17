package ports

import (
	"context"

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

	// Record adds an order with its fills and, with a change, what it did: in one
	// transaction, so the books always add up.
	Record(ctx context.Context, o domain.PlacedOrder, change *LedgerChange) error
	// Orders lists a workflow's orders, the newest first.
	Orders(ctx context.Context, workflow domain.WorkflowID, f PageFilter) (Page[domain.PlacedOrder], error)
}

// LedgerChange is what a filled order does to the books.
type LedgerChange struct {
	Cash    decimal.Decimal // added to the paper account; a buy's is negative
	Holding domain.Holding  // as the order leaves it
}
