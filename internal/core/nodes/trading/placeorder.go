package trading

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const (
	PlaceOrderType = "trading.placeOrder"
	PortMissed     = "missed"
	PortResting    = "resting"
)

// Where Place order takes its order from (D99).
const (
	FromCopy     = "copy"
	FromSettings = "settings"
)

// How a copy is placed (D212): as Copy sizing worked it out, or resting at its price.
const CopyAsIs = "copy"

var _ engine.Node = (*PlaceOrder)(nil)

// PlaceOrder places an order for each item, on paper or live as the workflow trades
// (D191): the copy Copy sizing worked out, or one its settings make (D99), at once or
// resting on the book (D212). Filled ones, wholly or in part, go out of main; those left
// resting out of resting (D208); unfilled, refused, pending and failed ones out of
// missed, saying why (D96, D204).
type PlaceOrder struct {
	placer ports.OrderPlacer
	now    func() time.Time
}

func NewPlaceOrder(placer ports.OrderPlacer) *PlaceOrder {
	return &PlaceOrder{placer: placer, now: time.Now}
}

func (*PlaceOrder) Type() engine.NodeType {
	sides := []string{string(domain.SideBuy), string(domain.SideSell)}

	return engine.NodeType{
		Name:  PlaceOrderType,
		Label: "Place order",
		Description: "Places an order for each item, on paper or live as the workflow trades: the copy Copy sizing " +
			"worked out, or one of its own settings, at once or resting on the book. Filled orders go out of main, " +
			"those left resting out of resting, those that did not fill out of missed.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, PortResting, PortMissed},
		Items:   &engine.Items{Passed: true, Fields: orderFields()},
		Params: []engine.ParamSpec{
			{
				Name: "from", Label: "Order", Kind: engine.ParamString, Default: FromCopy, Options: []string{FromCopy, FromSettings},
				Description: "copy: the order Copy sizing worked out. settings: the order these settings make.",
			},
			{Name: "tokenId", Label: "Token", Kind: engine.ParamString, Description: "For settings: the outcome's token ID."},
			{Name: "side", Label: "Side", Kind: engine.ParamString, Options: sides, Description: "For settings."},
			{
				Name: "price", Label: "Price", Kind: engine.ParamDecimal,
				Description: "For settings: the limit, the most a buy pays per share or the least a sell takes.",
			},
			{Name: "shares", Label: "Shares", Kind: engine.ParamDecimal, Description: "For settings: how many to buy or sell."},
			{
				Name: "dollars", Label: "Dollars", Kind: engine.ParamDecimal,
				Description: "For settings: a buy of so many dollars instead of shares.",
			},
			{
				Name: "type", Label: "Type", Kind: engine.ParamString, Default: string(domain.OrderFAK),
				Options: []string{string(domain.OrderFAK), string(domain.OrderFOK), string(domain.OrderGTC), string(domain.OrderGTD)},
				Description: "For settings. FAK: what the book has within the price, the rest cancelled. FOK: all of it, " +
					"or nothing. GTC: what the book has, the rest resting at the price until filled or cancelled. GTD: " +
					"the same, until Expires after.",
			},
			{
				Name: "copyAs", Label: "Copy as", Kind: engine.ParamString, Default: CopyAsIs,
				Options: []string{CopyAsIs, string(domain.OrderGTC), string(domain.OrderGTD)},
				Description: "For copy: copy, as Copy sizing worked it out, a FAK. GTC or GTD: resting at the copy's " +
					"price instead, what fills at once included.",
			},
			{
				Name: "expiresAfter", Label: "Expires after", Kind: engine.ParamDuration, Default: "1h",
				Description: "For GTD: how long it rests, 3 minutes at the least; then what is left is cancelled.",
			},
			{
				Name: "postOnly", Label: "Post-only", Kind: engine.ParamBool, Default: false,
				Description: "For GTC and GTD: refused rather than matched at once, so it only ever rests on the book.",
			},
		},
	}
}

func orderFields() []engine.Field {
	return []engine.Field{{
		Name: "order", Kind: engine.FieldObject, Description: "The order placed, and how it went.",
		Fields: []engine.Field{
			shape.Text("id", "Its ID in the workflow's orders."),
			shape.OneOf("mode", "paper: simulated against the live book. live: on Polymarket, from the workflow's wallet.",
				string(domain.ModePaper), string(domain.ModeLive)),
			shape.OneOf("status", "filled, partial (the rest cancelled), open (resting on the book, filled in part or "+
				"not yet), unfilled or refused; live, also pending (sent, its outcome not known yet: followed) or failed "+
				"(matched, but its trades failed on chain).",
				string(domain.OrderFilled), string(domain.OrderPartial), string(domain.OrderOpen), string(domain.OrderUnfilled),
				string(domain.OrderRefused), string(domain.OrderPending), string(domain.OrderFailed)),
			shape.Nullable(shape.Text("reason", "Why it was refused or did not fill wholly.")),
			shape.Decimal("shares", "Shares it got or sold."),
			shape.Decimal("dollars", "Dollars they came to."),
			shape.Decimal("fees", "Fees paid, in dollars."),
			shape.Nullable(shape.Decimal("price", "The average price per share; null when nothing filled.")),
		},
	}}
}

func (n *PlaceOrder) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		req, err := n.request(rc, p, item.JSON)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		placed, err := n.placer.Place(ctx, req)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		j := maps.Clone(item.JSON)
		j["order"] = orderItem(placed)

		port := PortMissed

		switch placed.Status {
		case domain.OrderFilled, domain.OrderPartial:
			port = engine.DefaultPort
		case domain.OrderOpen:
			port = PortResting
		}

		out.Add(port, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// request is the item's order: its copy, or the node's settings, with the trader it
// copies when there is one, so the copy is held as theirs (D92).
func (n *PlaceOrder) request(rc engine.RunContext, p engine.Params, j map[string]any) (domain.OrderRequest, error) {
	// The mode is the workflow's when the order goes: the trading service's to say.
	req := domain.OrderRequest{WorkflowID: rc.WorkflowID, OwnerID: rc.OwnerID, ExecutionID: rc.ExecutionID, NodeID: rc.NodeID, By: rc.By}

	if who, ok := j["trader"].(map[string]any); ok {
		row := engine.Params(who)
		if address, err := row.String("address"); err == nil {
			if w, err := domain.ParseAddress(address); err == nil {
				req.Trader = w
				req.TraderLabel, _ = row.String("label")
			}
		}
	}

	from, err := p.String("from")
	if err != nil {
		return req, err
	}

	order := p
	if from == FromCopy {
		copied, ok := j["copy"].(map[string]any)
		if !ok {
			return req, errors.New("no copy on the item: put Copy sizing before it, or set Order to settings")
		}

		order = engine.Params(copied)
	}

	side, _ := order.String("side")
	token, _ := order.String("tokenId")
	market, _ := order.String("marketId")
	typ, _ := order.String("type")

	req.Side, req.TokenID, req.MarketID, req.Type = domain.Side(side), domain.TokenID(token), domain.MarketID(market), domain.OrderType(typ)

	if req.Price, err = order.Decimal("price"); err != nil {
		return req, fmt.Errorf("the order's price: %w", err)
	}

	for name, to := range map[string]*decimal.Decimal{"shares": &req.Shares, "dollars": &req.Dollars} {
		if order[name] == nil {
			continue
		}

		if *to, err = order.Decimal(name); err != nil {
			return req, fmt.Errorf("the order's %s: %w", name, err)
		}
	}

	if from == FromCopy {
		as, err := p.String("copyAs")
		if err != nil {
			return req, err
		}

		if as != CopyAsIs {
			req.Type = domain.OrderType(as)
		}
	}

	if !req.Type.Rests() {
		return req, nil
	}

	if req.PostOnly, err = p.Bool("postOnly"); err != nil {
		return req, err
	}

	if req.Type == domain.OrderGTD {
		after, err := p.Duration("expiresAfter")
		if err != nil {
			return req, err
		}

		req.Expires = n.now().Add(after).UTC()
	}

	return req, nil
}

func orderItem(o domain.PlacedOrder) map[string]any {
	shares, dollars, fees := o.Totals()

	var reason, price any
	if o.Reason != "" {
		reason = o.Reason
	}

	if shares.IsPositive() {
		price = dollars.DivRound(shares, 6)
	}

	return map[string]any{
		"id": string(o.ID), "mode": string(o.Mode), "status": string(o.Status), "reason": reason,
		"shares": shares, "dollars": dollars, "fees": fees, "price": price,
	}
}
