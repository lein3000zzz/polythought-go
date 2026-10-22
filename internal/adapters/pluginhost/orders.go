package pluginhost

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// Trading is how plugins trade (D281): through the trading service Place order uses, so
// paper or live as the workflow trades, within its caps and the kill switch. Without
// Batches, several orders go one at a time; one left nil otherwise makes its calls fail.
type Trading struct {
	Orders    ports.OrderPlacer
	Batches   ports.BatchPlacer
	Canceller ports.OrderCanceller
}

// maxOrders is the most a call places: a batch's, as Polymarket takes them (D244).
const maxOrders = 15

// trades refuses a session that may not trade: without the orders permission, from a
// node its manifest does not mark trades (D309), or in a trigger, which has no run.
func (a *api) trades(s *session) error {
	switch {
	case !a.perms.orders:
		return status.Errorf(codes.PermissionDenied, "orders: its permissions do not name them (%s)", plugin.PermOrders)
	case !s.trades:
		return status.Errorf(codes.PermissionDenied, "%s does not trade: the manifest marks trades the nodes that place orders", s.typ.Name)
	case s.trigger:
		return status.Error(codes.FailedPrecondition, "a trigger trades nothing: a node's run does")
	}

	return nil
}

// PlaceOrders places orders for the workflow (D310): those that cannot be tried say why,
// the others go to the trading service, together when there are several.
func (a *api) PlaceOrders(ctx context.Context, req *pluginv1.PlaceOrdersRequest) (*pluginv1.PlaceOrdersResponse, error) {
	return call(ctx, a, req.GetSession(), opPlaceOrders, func(ctx context.Context, s *session, span trace.Span) (*pluginv1.PlaceOrdersResponse, error) {
		if err := a.trades(s); err != nil {
			return nil, err
		}

		orders := req.GetOrders()

		switch {
		case a.deps.Trading.Orders == nil:
			return nil, errNoSource
		case len(orders) == 0 || len(orders) > maxOrders:
			return nil, status.Errorf(codes.InvalidArgument, "orders: 1 to %d", maxOrders)
		}

		span.SetAttributes(attribute.Int("plugin.orders", len(orders)))

		out := &pluginv1.PlaceOrdersResponse{Results: make([]*pluginv1.OrderResult, len(orders))}

		var (
			reqs []domain.OrderRequest
			at   []int // each request's order
		)

		for i, o := range orders {
			r, err := orderRequest(s, o)
			if err != nil {
				out.Results[i] = &pluginv1.OrderResult{Error: fmt.Sprintf("order %d: %v", i, err)}
				continue
			}

			reqs, at = append(reqs, r), append(at, i)
		}

		results, err := a.place(ctx, reqs)
		if err != nil {
			return nil, a.sourceError(ctx, s, "the orders", err)
		}

		for k, r := range results {
			res := &pluginv1.OrderResult{Order: placedOf(r.Order)}
			if r.Err != nil {
				_, why := a.failure(ctx, s, fmt.Sprintf("order %d", at[k]), r.Err)
				res = &pluginv1.OrderResult{Error: why}
			}

			out.Results[at[k]] = res
		}

		a.log.Info(ctx, "Plugin placed orders", append(s.logArgs(), "orders", len(reqs))...)

		return out, nil
	})
}

// place places reqs together when there are several and a batch placer, one at a time
// otherwise.
func (a *api) place(ctx context.Context, reqs []domain.OrderRequest) ([]ports.PlaceResult, error) {
	if len(reqs) > 1 && a.deps.Trading.Batches != nil {
		return a.deps.Trading.Batches.PlaceBatch(ctx, reqs)
	}

	results := make([]ports.PlaceResult, len(reqs))
	for i, r := range reqs {
		results[i].Order, results[i].Err = a.deps.Trading.Orders.Place(ctx, r)
	}

	return results, nil
}

// orderRequest is a plugin's order as the trading service takes it, the run's: its form
// checked here, the rest by the service, which records why it refuses one.
func orderRequest(s *session, o *pluginv1.OrderRequest) (domain.OrderRequest, error) {
	r := domain.OrderRequest{
		WorkflowID: s.rc.WorkflowID, OwnerID: s.rc.OwnerID, ExecutionID: s.rc.ExecutionID, NodeID: s.rc.NodeID,
		TokenID: domain.TokenID(strings.TrimSpace(o.GetTokenId())), MarketID: domain.MarketID(strings.TrimSpace(o.GetMarketId())),
		Side: domain.Side(o.GetSide()), Type: domain.OrderType(o.GetType()),
	}

	switch {
	case r.TokenID == "":
		return r, fmt.Errorf("a token ID: name the outcome")
	case r.Side != domain.SideBuy && r.Side != domain.SideSell:
		return r, fmt.Errorf("side %q: buy or sell", o.GetSide())
	case r.Type != domain.OrderFOK && r.Type != domain.OrderFAK && r.Type != domain.OrderGTC && r.Type != domain.OrderGTD:
		return r, fmt.Errorf("type %q: FOK, FAK, GTC or GTD", o.GetType())
	}

	var err error

	if r.Price, err = decimal.NewFromString(o.GetPrice()); err != nil {
		return r, fmt.Errorf("the price %q is no decimal", o.GetPrice())
	}

	for _, amount := range []struct {
		name, text string
		to         *decimal.Decimal
	}{{"shares", o.GetShares(), &r.Shares}, {"dollars", o.GetDollars(), &r.Dollars}} {
		if amount.text == "" {
			continue
		}

		if *amount.to, err = decimal.NewFromString(amount.text); err != nil {
			return r, fmt.Errorf("the %s %q are no decimal", amount.name, amount.text)
		}
	}

	if t := o.GetTrader(); t != "" {
		if r.Trader, err = domain.ParseAddress(t); err != nil {
			return r, fmt.Errorf("the trader: %w", err)
		}

		r.TraderLabel = o.GetTraderLabel()
	}

	if !r.Type.Rests() {
		return r, nil
	}

	r.PostOnly = o.GetPostOnly()

	if r.Type == domain.OrderGTD {
		if r.Expires, err = time.Parse(time.RFC3339Nano, o.GetExpires()); err != nil {
			return r, fmt.Errorf("a GTD order expires: a time, RFC 3339")
		}
	}

	return r, nil
}

// CancelOrders cancels the workflow's own resting orders, as Cancel orders does (D310).
func (a *api) CancelOrders(ctx context.Context, req *pluginv1.CancelOrdersRequest) (*pluginv1.CancelOrdersResponse, error) {
	return call(ctx, a, req.GetSession(), opCancelOrders, func(ctx context.Context, s *session, _ trace.Span) (*pluginv1.CancelOrdersResponse, error) {
		if err := a.trades(s); err != nil {
			return nil, err
		}

		c := ports.CancelRequest{
			WorkflowID: s.rc.WorkflowID, OwnerID: s.rc.OwnerID, OrderID: domain.OrderID(req.GetOrderId()),
			MarketID: domain.MarketID(req.GetMarketId()), TokenID: domain.TokenID(req.GetTokenId()), All: req.GetAll(),
		}

		switch {
		case a.deps.Trading.Canceller == nil:
			return nil, errNoSource
		case c.OrderID == "" && c.MarketID == "" && c.TokenID == "" && !c.All:
			return nil, status.Error(codes.InvalidArgument, "which orders: one by its ID, a market's, an outcome's, or all")
		}

		results, err := a.deps.Trading.Canceller.CancelOrders(ctx, c)
		if err != nil {
			return nil, a.sourceError(ctx, s, "cancel orders", err)
		}

		out := &pluginv1.CancelOrdersResponse{Results: make([]*pluginv1.CancelResult, len(results))}
		for i, r := range results {
			out.Results[i] = &pluginv1.CancelResult{Order: placedOf(r.Order), Reason: r.Reason}
		}

		a.log.Info(ctx, "Plugin cancelled orders", append(s.logArgs(), "orders", len(results))...)

		return out, nil
	})
}

// placedOf is an order as a plugin sees it: what was asked, and how far it filled.
func placedOf(o domain.PlacedOrder) *pluginv1.PlacedOrder {
	shares, dollars, fees := o.Totals()

	out := &pluginv1.PlacedOrder{
		Id: string(o.ID), Mode: string(o.Mode), Status: string(o.Status), Reason: o.Reason, TokenId: string(o.TokenID),
		Side: string(o.Side), Type: string(o.Type), Shares: text(shares), Dollars: text(dollars), Fees: text(fees),
	}

	if shares.IsPositive() {
		out.Price = text(dollars.DivRound(shares, 6))
	}

	return out
}
