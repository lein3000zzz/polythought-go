package markets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const TradesType = "polymarket.trades"

// maxTrades is the most trades one market may return per run.
const maxTrades = 500

var (
	_ engine.Node = (*Trades)(nil)

	ErrNoMarket = errors.New("item has no market id")
)

// Trades lists the recent trades of each input item's market: a market item's id, or
// the marketId of any other item.
type Trades struct {
	trades ports.TradeReader
	now    func() time.Time
}

func NewTrades(trades ports.TradeReader) *Trades {
	return &Trades{trades: trades, now: time.Now}
}

// WithClock replaces time.Now, for tests.
func (n *Trades) WithClock(now func() time.Time) *Trades {
	n.now = now
	return n
}

func (*Trades) Type() engine.NodeType {
	return engine.NodeType{
		Name:  TradesType,
		Label: "Recent trades",
		Description: "Lists the recent trades of each market, one item per trade, with its value in " +
			"dollars: for spotting large buyers and sellers.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Own(tradeFields()...),
		Params: []engine.ParamSpec{
			{Name: "since", Label: "Look back", Kind: engine.ParamDuration, Default: "1h", Description: "How far back, e.g. 15m or 1h."},
			{Name: "limit", Label: "Limit", Kind: engine.ParamNumber, Default: 50, Description: "Most trades per market, up to 500."},
		},
	}
}

func (n *Trades) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}
	now := n.now()

	for i, item := range in[engine.DefaultPort] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		since, err := p.Duration("since")
		if err != nil {
			return nil, err
		}

		limit, err := p.Int("limit")
		if err != nil {
			return nil, err
		}

		if limit < 1 || limit > maxTrades {
			return nil, fmt.Errorf("%w %q: %d is not between 1 and %d", engine.ErrInvalidParam, "limit", limit, maxTrades)
		}

		market, _ := firstOf(item.JSON, "marketId", "id").(string)
		if market == "" {
			out.Fail(item, src, ErrNoMarket)
			continue
		}

		page, err := n.trades.Trades(ctx, ports.TradeFilter{
			MarketIDs: []domain.MarketID{domain.MarketID(market)},
			Since:     now.Add(-since),
			Limit:     limit,
		})
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		for _, t := range page.Items {
			out.Add(engine.DefaultPort, engine.Item{JSON: tradeItem(item, t), Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

func tradeItem(market engine.Item, t domain.Trade) map[string]any {
	return map[string]any{
		"marketId": string(t.MarketID),
		"question": market.JSON["question"],
		"outcome":  t.Outcome,
		"tokenId":  string(t.TokenID),
		"side":     string(t.Side),
		"price":    t.Price,
		"size":     t.Size,
		"value":    t.Price.Mul(t.Size),
		"wallet":   string(t.Wallet),
		"at":       t.At,
		"txHash":   t.TxHash,
	}
}

func tradeFields() []engine.Field {
	return []engine.Field{
		shape.Text("marketId", "The market's condition ID."),
		shape.Nullable(shape.Text("question", "The market's question, from the item received.")),
		shape.Text("outcome", "The outcome's label, like Yes."),
		shape.Text("tokenId", "The outcome's token ID."),
		shape.OneOf("side", "Whether the taker bought or sold.", string(domain.SideBuy), string(domain.SideSell)),
		shape.Decimal("price", "Per share."),
		shape.Decimal("size", "In shares."),
		shape.Decimal("value", "price times size, in dollars."),
		shape.Text("wallet", "The trader's wallet address."),
		shape.Time("at", "When it happened."),
		shape.Text("txHash", "The transaction that settled it."),
	}
}
