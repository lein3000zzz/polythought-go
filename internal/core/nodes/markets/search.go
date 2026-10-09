package markets

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const SearchType = "polymarket.search"

var _ engine.Node = (*Search)(nil)

// Search finds markets by words in their event's title, once per input item, and emits
// one item per market, shaped like the Markets node's.
type Search struct {
	events ports.EventReader
}

func NewSearch(events ports.EventReader) *Search {
	return &Search{events: events}
}

func (*Search) Type() engine.NodeType {
	return engine.NodeType{
		Name:        SearchType,
		Label:       "Search markets",
		Description: "Finds markets by words in their title, e.g. bitcoin or election, one item per market.",
		Inputs:      []string{engine.DefaultPort},
		Outputs:     []string{engine.DefaultPort},
		Params: []engine.ParamSpec{
			{Name: "query", Label: "Search for", Kind: engine.ParamString, Required: true, Description: "Words in the event title."},
			{Name: "limit", Label: "Limit", Kind: engine.ParamNumber, Default: 10, Description: "Most markets to return."},
			{Name: "closed", Label: "Closed markets", Kind: engine.ParamBool, Default: false, Description: "Search closed markets instead of open ones."},
		},
	}
}

// Run fails an input item, not the node, when its search fails.
func (n *Search) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	for i, item := range in[engine.DefaultPort] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		query, err := p.String("query")
		if err != nil {
			return nil, err
		}

		limit, err := p.Int("limit")
		if err != nil {
			return nil, err
		}

		closed, err := p.Bool("closed")
		if err != nil {
			return nil, err
		}

		page, err := n.events.ListEvents(ctx, ports.EventFilter{Title: query, Limit: limit, Closed: &closed})
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		found := 0

	events:
		for _, e := range page.Items {
			for _, m := range e.Markets {
				if found == limit {
					break events
				}

				// An open event can hold markets that already closed, like past dates.
				if !closed && m.Status != domain.MarketOpen {
					continue
				}

				j := marketItem(m)
				j["event"] = e.Title
				j["eventSlug"] = e.Slug

				out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
				found++
			}
		}
	}

	return out, nil
}
