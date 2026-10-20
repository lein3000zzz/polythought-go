package trading

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const CancelOrdersType = "trading.cancelOrders"

// Which of the workflow's resting orders Cancel orders cancels (D207).
const (
	CancelTheOrder = "order"
	CancelAll      = "all"
	CancelMarket   = "market"
	CancelOutcome  = "outcome"
)

var _ engine.Node = (*CancelOrders)(nil)

// CancelOrders cancels the workflow's own orders resting on the book (D207), for each
// item: the item's order, all of them, or those of a market or an outcome. Orders placed
// by hand, or by other workflows on the same wallet, stay. Items with something cancelled
// go out of main, the others out of missed, saying why.
type CancelOrders struct {
	canceller ports.OrderCanceller
}

func NewCancelOrders(canceller ports.OrderCanceller) *CancelOrders {
	return &CancelOrders{canceller: canceller}
}

func (*CancelOrders) Type() engine.NodeType {
	return engine.NodeType{
		Name:  CancelOrdersType,
		Label: "Cancel orders",
		Description: "Cancels the workflow's own orders resting on the book, for each item: the item's order, all of " +
			"them, or those of a market or an outcome. What they filled before stays. Items with something cancelled go out " +
			"of main, the others out of missed.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, PortMissed},
		Items:   &engine.Items{Passed: true, Fields: cancelFields()},
		Params: []engine.ParamSpec{
			{
				Name: "which", Label: "Cancel", Kind: engine.ParamString, Default: CancelTheOrder,
				Options: []string{CancelTheOrder, CancelAll, CancelMarket, CancelOutcome},
				Description: "order: the item's order, as Place order or Order activity put it on the item. all: every " +
					"order of the workflow resting on the book. market or outcome: those in it.",
			},
			{Name: "marketId", Label: "Market", Kind: engine.ParamString, Description: "For market: its condition ID."},
			{Name: "tokenId", Label: "Token", Kind: engine.ParamString, Description: "For outcome: its token ID."},
		},
	}
}

func cancelFields() []engine.Field {
	return []engine.Field{{
		Name: "cancel", Kind: engine.FieldObject, Description: "What it cancelled, and what not.",
		Fields: []engine.Field{
			shape.ListOf("cancelled", "The orders cancelled, as they are now: what they filled before is theirs.",
				shape.Text("id", "The order's ID in the workflow's orders."),
				shape.OneOf("mode", "paper or live.", string(domain.ModePaper), string(domain.ModeLive)),
				shape.Text("status", "canceled, or filled when it filled before it could be."),
				shape.Decimal("shares", "Shares it filled before."),
			),
			shape.ListOf("notCancelled", "The orders not cancelled, and why: matched already, say.",
				shape.Text("id", "The order's ID."),
				shape.Text("reason", "Why it was not cancelled."),
			),
		},
	}}
}

var errNoOrder = errors.New("no order on the item: put Place order or Order activity before it, or cancel all, a market or an outcome")

func (n *CancelOrders) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		req, err := cancelRequest(rc, p, item.JSON)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		results, err := n.canceller.CancelOrders(ctx, req)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		cancelled, notCancelled := []any{}, []any{}

		for _, r := range results {
			if r.Reason != "" {
				notCancelled = append(notCancelled, map[string]any{"id": string(r.Order.ID), "reason": r.Reason})
				continue
			}

			shares, _, _ := r.Order.Totals()
			cancelled = append(cancelled, map[string]any{
				"id": string(r.Order.ID), "mode": string(r.Order.Mode), "status": string(r.Order.Status), "shares": shares,
			})
		}

		port := engine.DefaultPort
		if len(cancelled) == 0 {
			port = PortMissed

			if len(notCancelled) == 0 {
				notCancelled = append(notCancelled, map[string]any{"id": "", "reason": "nothing of it rests on the book"})
			}
		}

		j := maps.Clone(item.JSON)
		j["cancel"] = map[string]any{"cancelled": cancelled, "notCancelled": notCancelled}

		out.Add(port, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

func cancelRequest(rc engine.RunContext, p engine.Params, j map[string]any) (ports.CancelRequest, error) {
	req := ports.CancelRequest{WorkflowID: rc.WorkflowID, OwnerID: rc.OwnerID}

	which, err := p.String("which")
	if err != nil {
		return req, err
	}

	switch which {
	case CancelTheOrder:
		order, ok := j["order"].(map[string]any)
		if !ok {
			return req, errNoOrder
		}

		id, err := engine.Params(order).String("id")
		if err != nil || id == "" {
			return req, errNoOrder
		}

		req.OrderID = domain.OrderID(id)
	case CancelAll:
		req.All = true
	case CancelMarket:
		market, err := p.String("marketId")
		if err != nil || market == "" {
			return req, errors.New("cancelling a market's orders needs the market")
		}

		req.MarketID = domain.MarketID(market)
	case CancelOutcome:
		token, err := p.String("tokenId")
		if err != nil || token == "" {
			return req, errors.New("cancelling an outcome's orders needs its token")
		}

		req.TokenID = domain.TokenID(token)
	default:
		return req, fmt.Errorf("%q is not what to cancel: order, all, market or outcome", which)
	}

	return req, nil
}
