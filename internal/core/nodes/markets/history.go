package markets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const PriceChangeType = "polymarket.priceChange"

var (
	_ engine.Node = (*PriceChange)(nil)

	ErrNoHistory = errors.New("no prices in the window")
)

// PriceChange compares an outcome's price now with its price a while ago. It takes
// market items, using their outcomes, or any item with a tokenId, like an order book's.
type PriceChange struct {
	history ports.PriceHistoryReader
	now     func() time.Time
}

func NewPriceChange(history ports.PriceHistoryReader) *PriceChange {
	return &PriceChange{history: history, now: time.Now}
}

// WithClock replaces time.Now, for tests.
func (n *PriceChange) WithClock(now func() time.Time) *PriceChange {
	n.now = now
	return n
}

func (*PriceChange) Type() engine.NodeType {
	return engine.NodeType{
		Name:  PriceChangeType,
		Label: "Price change",
		Description: "Compares each outcome's price now with its price a while ago: the change in " +
			"cents and in percent, for spotting markets on the move.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "window", Label: "Compare with", Kind: engine.ParamDuration, Default: "1h", Description: "How long ago, e.g. 1h or 24h."},
			{
				Name: "outcomes", Label: "Outcomes", Kind: engine.ParamString, Default: "first", Options: []string{"first", "all"},
				Description: "first: the first outcome of each market, usually Yes. all: every outcome.",
			},
		},
	}
}

func (n *PriceChange) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}
	now := n.now()

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		window, err := p.Duration("window")
		if err != nil {
			return nil, err
		}

		which, err := p.String("outcomes")
		if err != nil {
			return nil, err
		}

		refs, err := tokensOf(item)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		if which != "all" {
			refs = refs[:1]
		}

		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			j, err := n.change(ctx, item, ref, now, window)
			if err != nil {
				out.Fail(item, src, fmt.Errorf("outcome %s: %w", ref.label, err))
				continue
			}

			out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

func (n *PriceChange) change(ctx context.Context, item engine.Item, ref outcomeRef, now time.Time, window time.Duration) (map[string]any, error) {
	since := now.Add(-window)

	page, err := n.history.PriceHistory(ctx, ports.PriceHistoryFilter{TokenID: ref.tokenID, Since: since, Bucket: bucketFor(window)})
	if err != nil {
		return nil, err
	}

	points := slices.SortedFunc(slices.Values(page.Items), func(a, b domain.PricePoint) int { return a.At.Compare(b.At) })
	if len(points) == 0 {
		return nil, ErrNoHistory
	}

	then, last := points[0].Price, points[len(points)-1].Price
	change := last.Sub(then)

	j := map[string]any{
		"marketId":     firstOf(item.JSON, "marketId", "id"),
		"question":     item.JSON["question"],
		"outcome":      ref.label,
		"tokenId":      string(ref.tokenID),
		"priceThen":    then,
		"priceNow":     last,
		"change":       change,
		"changePct":    nil,
		"absChangePct": nil,
		"since":        since.UTC(),
	}

	if !then.IsZero() {
		pct := change.Div(then).Mul(decimal.NewFromInt(100)).Round(2)
		j["changePct"] = pct
		j["absChangePct"] = pct.Abs()
	}

	return j, nil
}

// bucketFor picks the price history's resolution: about thirty points over the window,
// within what the API accepts.
func bucketFor(window time.Duration) time.Duration {
	return min(max(window/30, time.Minute), 24*time.Hour)
}

// tokensOf reads the outcomes of a market item, or the single outcome of an item that
// names a tokenId.
func tokensOf(item engine.Item) ([]outcomeRef, error) {
	if refs, err := outcomesOf(item); err == nil {
		return refs, nil
	}

	token, _ := item.JSON["tokenId"].(string)
	if token == "" {
		return nil, ErrNoOutcomes
	}

	label, _ := item.JSON["outcome"].(string)

	return []outcomeRef{{tokenID: domain.TokenID(token), label: label}}, nil
}

func firstOf(j map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := j[k]; ok && v != nil {
			return v
		}
	}

	return nil
}
