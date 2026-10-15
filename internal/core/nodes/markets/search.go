package markets

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
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
		Items:       shape.Own(FoundFields()...),
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

		found, err := SearchMarkets(ctx, n.events, query, limit, closed)
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		for _, j := range found {
			out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
		}
	}

	return out, nil
}

// SearchMarkets finds up to limit markets by words in their event's title, open ones or
// closed ones, shaped like MarketItem with their event's title and slug.
func SearchMarkets(ctx context.Context, events ports.EventReader, query string, limit int, closed bool) ([]map[string]any, error) {
	page, err := events.ListEvents(ctx, ports.EventFilter{Title: query, Limit: limit, Closed: &closed})
	if err != nil {
		return nil, err
	}

	found := make([]map[string]any, 0, limit)

	for _, e := range page.Items {
		for _, m := range e.Markets {
			if len(found) == limit {
				return found, nil
			}

			// An open event can hold markets that already closed, like past dates.
			if !closed && m.Status != domain.MarketOpen {
				continue
			}

			j := MarketItem(m)
			j["event"] = e.Title
			j["eventSlug"] = e.Slug

			found = append(found, j)
		}
	}

	return found, nil
}
