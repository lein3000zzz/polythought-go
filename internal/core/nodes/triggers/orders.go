package triggers

import (
	"context"
	"slices"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const OrderActivityType = "trigger.orderActivity"

var _ engine.Trigger = (*OrderActivity)(nil)

var orderEvents = []string{
	string(domain.OrderEventFilled), string(domain.OrderEventCanceled),
	string(domain.OrderEventExpired), string(domain.OrderEventFailed),
}

// OrderActivity starts the workflow on what happens to its own orders after the runs
// that placed them (D211, D215): a resting order filling, its rest cancelled or expired,
// a live trade failing on chain; paper and live alike (D216), an item each.
type OrderActivity struct {
	events ports.OrderEvents
}

func NewOrderActivity(events ports.OrderEvents) *OrderActivity {
	return &OrderActivity{events: events}
}

func (*OrderActivity) Type() engine.NodeType {
	return engine.NodeType{
		Name:  OrderActivityType,
		Label: "Order activity",
		Description: "Starts the workflow on what happens to its own orders after the runs that placed them: a " +
			"resting order filling, what is left of it cancelled or expired, a live trade failing on chain. An item " +
			"each, paper and live.",
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Own(orderEventFields()...),
		Params: []engine.ParamSpec{{
			Name: "events", Label: "Events", Kind: engine.ParamList, Default: slices.Clone(anyOf(orderEvents)), Options: orderEvents,
			Description: "filled: a resting order filled, in part or wholly. canceled: what was left of it was " +
				"cancelled, by Cancel orders, its Trading view, Polymarket or its market resolving. expired: a GTD's " +
				"time came. failed: a live trade failed on chain and the books undid it.",
		}},
	}
}

func anyOf(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}

	return out
}

func orderEventFields() []engine.Field {
	fill := []engine.Field{
		shape.Decimal("price", "Its price per share."),
		shape.Decimal("shares", "Its shares."),
		shape.Decimal("dollars", "Shares times price."),
		shape.Decimal("fee", "Its fee, in dollars; a maker pays none."),
	}

	return []engine.Field{
		shape.OneOf("event", "What happened: filled, canceled, expired or failed.", orderEvents...),
		shape.OneOf("mode", "paper or live.", string(domain.ModePaper), string(domain.ModeLive)),
		{
			Name: "order", Kind: engine.FieldObject, Description: "The order, as it is now.",
			Fields: []engine.Field{
				shape.Text("id", "Its ID in the workflow's orders: Cancel orders cancels it by it."),
				shape.Text("status", "open, filled, partial, canceled, expired or failed."),
				shape.OneOf("side", "buy or sell.", string(domain.SideBuy), string(domain.SideSell)),
				shape.Text("type", "FAK, FOK, GTC or GTD."),
				shape.Text("tokenId", "The outcome's token ID."),
				shape.Nullable(shape.Text("marketId", "The market's condition ID.")),
				shape.Decimal("price", "Its limit."),
				shape.Decimal("shares", "Shares asked for; zero for a buy of dollars."),
				shape.Decimal("filled", "Shares it filled so far."),
				shape.Decimal("dollars", "Dollars they came to."),
				shape.Decimal("fees", "Fees paid, in dollars."),
				shape.Nullable(shape.Text("reason", "How it stands, or why it ended.")),
				shape.Nullable(shape.Text("trader", "The wallet whose trade it copied, if any.")),
			},
		},
		shape.ListOf("fills", "For filled and failed: the fills it is about.", fill...),
		shape.Time("at", "When it was seen."),
	}
}

func (o *OrderActivity) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	wanted, err := tc.Params.Strings("events")
	if err != nil {
		return err
	}

	for e := range o.events.OrderEvents(ctx, tc.WorkflowID) {
		if !slices.Contains(wanted, string(e.Kind)) {
			continue
		}

		_ = emit(ctx, engine.Ports{engine.DefaultPort: {{JSON: orderEventItem(e)}}})
	}

	return nil
}

func orderEventItem(e domain.OrderEvent) map[string]any {
	o := e.Order
	shares, dollars, fees := o.Totals()

	text := func(s string) any {
		if s == "" {
			return nil
		}

		return s
	}

	fills := make([]any, len(e.Fills))
	for i, f := range e.Fills {
		fills[i] = map[string]any{"price": f.Price, "shares": f.Shares, "dollars": f.Dollars, "fee": f.Fee}
	}

	return map[string]any{
		"event": string(e.Kind),
		"mode":  string(o.Mode),
		"order": map[string]any{
			"id": string(o.ID), "status": string(o.Status), "side": string(o.Side), "type": string(o.Type),
			"tokenId": string(o.TokenID), "marketId": text(string(o.MarketID)), "price": o.Price, "shares": o.Shares,
			"filled": shares, "dollars": dollars, "fees": fees, "reason": text(o.Reason), "trader": text(string(o.Trader)),
		},
		"fills": fills,
		"at":    e.At,
	}
}
