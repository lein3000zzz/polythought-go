package markets

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const OrderBooksType = "polymarket.orderbook"

var _ engine.Node = (*OrderBooks)(nil)

// OrderBooks takes market items and emits one item per outcome with its book summary.
// All books are fetched in one batch.
type OrderBooks struct {
	reader ports.OrderBookReader
}

func NewOrderBooks(reader ports.OrderBookReader) *OrderBooks {
	return &OrderBooks{reader: reader}
}

func (*OrderBooks) Type() engine.NodeType {
	return engine.NodeType{
		Name:        OrderBooksType,
		Label:       "Order books",
		Description: "Adds the current order book of each outcome to market items: best prices, spread and depth.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "depth", Label: "Depth", Description: "Price levels summed into bidDepth and askDepth.", Kind: engine.ParamNumber, Default: 5},
		},
	}
}

type pending struct {
	item     engine.Item
	src      engine.ItemRef
	depth    int
	outcomes []outcomeRef
}

func (n *OrderBooks) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	var (
		work   []pending
		tokens []domain.TokenID
		seen   = make(map[domain.TokenID]bool)
	)

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		depth, err := p.Int("depth")
		if err != nil {
			return nil, err
		}

		outcomes, err := outcomesOf(item)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		for _, o := range outcomes {
			if !seen[o.tokenID] {
				seen[o.tokenID] = true
				tokens = append(tokens, o.tokenID)
			}
		}

		work = append(work, pending{item: item, src: src, depth: depth, outcomes: outcomes})
	}

	if len(work) == 0 {
		return out, nil
	}

	books, err := n.reader.OrderBooks(ctx, tokens...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}

		for _, w := range work {
			out.Fail(w.item, w.src, err)
		}

		return out, nil
	}

	byToken := make(map[domain.TokenID]domain.OrderBook, len(books))
	for _, b := range books {
		byToken[b.TokenID] = b
	}

	for _, w := range work {
		for _, o := range w.outcomes {
			book, ok := byToken[o.tokenID]
			if !ok {
				out.Fail(w.item, w.src, fmt.Errorf("no order book for token %s", o.tokenID))
				continue
			}

			out.Add(engine.DefaultPort, engine.Item{JSON: bookItem(w.item, o, book, w.depth), Source: []engine.ItemRef{w.src}})
		}
	}

	return out, nil
}

// bookItem summarises one outcome's book. Mid and spread are nil for a one-sided book.
func bookItem(market engine.Item, o outcomeRef, b domain.OrderBook, depth int) map[string]any {
	j := map[string]any{
		"marketId": market.JSON["id"],
		"question": market.JSON["question"],
		"outcome":  o.label,
		"tokenId":  string(o.tokenID),
		"bestBid":  nil,
		"bestAsk":  nil,
		"mid":      nil,
		"spread":   nil,
		"bidDepth": sumSize(b.Bids, depth),
		"askDepth": sumSize(b.Asks, depth),
		"at":       b.At,
	}

	if bid, ok := b.BestBid(); ok {
		j["bestBid"] = bid.Price
	}

	if ask, ok := b.BestAsk(); ok {
		j["bestAsk"] = ask.Price
	}

	if mid, ok := b.Mid(); ok {
		j["mid"] = mid
	}

	if spread, ok := b.Spread(); ok {
		j["spread"] = spread
	}

	return j
}

func sumSize(levels []domain.Level, depth int) decimal.Decimal {
	sum := decimal.Zero
	for _, l := range levels[:min(depth, len(levels))] {
		sum = sum.Add(l.Size)
	}

	return sum
}
