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

const PlaceOrdersType = "trading.placeOrders"

var _ engine.Node = (*PlaceOrders)(nil)

// PlaceOrders places a list of orders for each item, together (D244 to D248): its
// Orders table, rows typed in or the whole table an expression such as {{ basket }}.
// Each is checked on its own, those refused not stopping the rest; live, the others
// reach Polymarket together, 15 to a request. An item comes out per order, as Place
// order's do. Polymarket's batch is not atomic, and neither is this.
type PlaceOrders struct {
	placer ports.BatchPlacer
	now    func() time.Time
}

func NewPlaceOrders(placer ports.BatchPlacer) *PlaceOrders {
	return &PlaceOrders{placer: placer, now: time.Now}
}

var orderColumns = []engine.ParamSpec{
	{Name: "tokenId", Label: "Token", Kind: engine.ParamString, Required: true, Description: "The outcome's token ID."},
	{Name: "side", Label: "Side", Kind: engine.ParamString, Required: true, Options: []string{string(domain.SideBuy), string(domain.SideSell)}},
	{
		Name: "price", Label: "Price", Kind: engine.ParamDecimal, Required: true,
		Description: "The limit: the most a buy pays per share, the least a sell takes.",
	},
	{Name: "shares", Label: "Shares", Kind: engine.ParamDecimal, Description: "How many to buy or sell."},
	{Name: "dollars", Label: "Dollars", Kind: engine.ParamDecimal, Description: "A buy of so many dollars instead of shares."},
	{
		Name: "type", Label: "Type", Kind: engine.ParamString, Default: string(domain.OrderFAK),
		Options: []string{string(domain.OrderFAK), string(domain.OrderFOK), string(domain.OrderGTC), string(domain.OrderGTD)},
	},
	{Name: "marketId", Label: "Market", Kind: engine.ParamString, Description: "Its market's condition ID; looked up when empty."},
}

func (*PlaceOrders) Type() engine.NodeType {
	return engine.NodeType{
		Name:  PlaceOrdersType,
		Label: "Place orders",
		Description: "Places a list of orders for each item, together, on paper or live as the workflow trades: live, " +
			"they reach Polymarket in one request, 15 at a time. Each is checked on its own; an item comes out per " +
			"order, filled ones out of main, resting ones out of resting, the rest out of missed.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort, PortResting, PortMissed},
		Items:   &engine.Items{Passed: true, Fields: batchOrderFields()},
		Params: []engine.ParamSpec{
			{
				Name: "orders", Label: "Orders", Kind: engine.ParamTable, Required: true, Columns: orderColumns,
				Description: "A row per order; or the whole table an expression giving a list of them, like " +
					"{{ basket }}, each with tokenId, side, price, shares or dollars, and type.",
			},
			{
				Name: "expiresAfter", Label: "Expires after", Kind: engine.ParamDuration, Default: "1h",
				Description: "For GTD rows: how long they rest, 3 minutes at the least; then what is left is cancelled.",
			},
			{
				Name: "postOnly", Label: "Post-only", Kind: engine.ParamBool, Default: false,
				Description: "For GTC and GTD rows: refused rather than matched at once, so they only ever rest.",
			},
		},
	}
}

func batchOrderFields() []engine.Field {
	f := orderFields()
	f[0].Description = "The order of the list this item is for, and how it went."
	f[0].Fields = append([]engine.Field{
		shape.Number("index", "Its place in the list, from 0."),
		shape.Text("tokenId", "Its outcome's token ID."),
		shape.OneOf("side", "buy or sell.", string(domain.SideBuy), string(domain.SideSell)),
	}, f[0].Fields...)

	return f
}

var errNoOrders = errors.New("no orders: add rows to Orders, or give it a list")

func (n *PlaceOrders) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		rows, err := p.Table("orders", orderColumns)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		if len(rows) == 0 {
			out.Fail(item, src, errNoOrders)
			continue
		}

		var (
			reqs []domain.OrderRequest
			at   []int // each request's row
		)

		for r, row := range rows {
			req, err := n.request(rc, p, item.JSON, row)
			if err != nil {
				out.Fail(n.result(item, r, row, nil), src, fmt.Errorf("order %d: %w", r, err))
				continue
			}

			reqs, at = append(reqs, req), append(at, r)
		}

		if len(reqs) == 0 {
			continue
		}

		results, err := n.placer.PlaceBatch(ctx, reqs)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		for k, res := range results {
			r := at[k]

			if res.Err != nil {
				out.Fail(n.result(item, r, rows[r], nil), src, fmt.Errorf("order %d: %w", r, res.Err))
				continue
			}

			port := PortMissed

			switch res.Order.Status {
			case domain.OrderFilled, domain.OrderPartial:
				port = engine.DefaultPort
			case domain.OrderOpen:
				port = PortResting
			}

			out.Add(port, engine.Item{JSON: n.result(item, r, rows[r], &res.Order).JSON, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

// result is the item for row r of item's list: its fields, and the order as it went.
func (n *PlaceOrders) result(item engine.Item, r int, row engine.Params, o *domain.PlacedOrder) engine.Item {
	j := maps.Clone(item.JSON)

	order := map[string]any{"index": r}
	if o != nil {
		order = orderItem(*o)
		order["index"] = r
	}

	token, _ := row.String("tokenId")
	side, _ := row.String("side")
	order["tokenId"], order["side"] = token, side
	j["order"] = order

	return engine.Item{JSON: j}
}

// request is a row's order, the item's trader its copier when there is one (D92).
func (n *PlaceOrders) request(rc engine.RunContext, p engine.Params, j map[string]any, row engine.Params) (domain.OrderRequest, error) {
	req := domain.OrderRequest{WorkflowID: rc.WorkflowID, OwnerID: rc.OwnerID, ExecutionID: rc.ExecutionID, NodeID: rc.NodeID}

	if who, ok := j["trader"].(map[string]any); ok {
		w := engine.Params(who)
		if address, err := w.String("address"); err == nil {
			if a, err := domain.ParseAddress(address); err == nil {
				req.Trader = a
				req.TraderLabel, _ = w.String("label")
			}
		}
	}

	side, _ := row.String("side")
	token, _ := row.String("tokenId")
	market, _ := row.String("marketId")
	typ, _ := row.String("type")

	req.Side, req.TokenID, req.MarketID, req.Type = domain.Side(side), domain.TokenID(token), domain.MarketID(market), domain.OrderType(typ)

	var err error
	if req.Price, err = row.Decimal("price"); err != nil {
		return req, fmt.Errorf("its price: %w", err)
	}

	for name, to := range map[string]*decimal.Decimal{"shares": &req.Shares, "dollars": &req.Dollars} {
		if row[name] == nil {
			continue
		}

		if *to, err = row.Decimal(name); err != nil {
			return req, fmt.Errorf("its %s: %w", name, err)
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
