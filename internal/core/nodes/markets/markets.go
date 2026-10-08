package markets

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const MarketsType = "polymarket.markets"

var _ engine.Node = (*Markets)(nil)

// Markets lists markets once per input item and emits one item per market.
type Markets struct {
	reader ports.MarketReader
}

func NewMarkets(reader ports.MarketReader) *Markets {
	return &Markets{reader: reader}
}

func (*Markets) Type() engine.NodeType {
	return engine.NodeType{
		Name:    MarketsType,
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "limit", Kind: engine.ParamNumber, Default: 20},
			{Name: "closed", Kind: engine.ParamBool, Default: false},
			{Name: "slugs", Kind: engine.ParamList},
			{Name: "tagIds", Kind: engine.ParamList},
		},
	}
}

// Run fails an input item, not the node, when its lookup fails.
func (n *Markets) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		filter, err := n.filter(rc, src)
		if err != nil {
			return nil, err // bad config fails the node for every item alike
		}

		page, err := n.reader.ListMarkets(ctx, filter)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		for _, m := range page.Items {
			out.Add(engine.DefaultPort, engine.Item{JSON: marketItem(m), Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

func (n *Markets) filter(rc engine.RunContext, src engine.ItemRef) (ports.MarketFilter, error) {
	p, err := rc.Params.Resolve(src)
	if err != nil {
		return ports.MarketFilter{}, err
	}

	limit, err := p.Int("limit")
	if err != nil {
		return ports.MarketFilter{}, err
	}

	closed, err := p.Bool("closed")
	if err != nil {
		return ports.MarketFilter{}, err
	}

	slugs, err := optionalStrings(p, "slugs")
	if err != nil {
		return ports.MarketFilter{}, err
	}

	tags, err := optionalStrings(p, "tagIds")
	if err != nil {
		return ports.MarketFilter{}, err
	}

	return ports.MarketFilter{Limit: limit, Closed: &closed, Slugs: slugs, TagIDs: tags}, nil
}
