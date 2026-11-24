package markets

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const EventPricesType = "polymarket.eventPrices"

var _ engine.Node = (*EventPrices)(nil)

// EventPrices adds up the Yes prices of every outcome of an event still taking orders
// (D437): what buying every Yes costs, what selling them all brings, and the midpoints, as
// each outcome's book has them now. A weather market's buckets, or a league's futures, in
// one node: their sum against 1, or the total they should come to.
type EventPrices struct {
	events ports.EventReader
	books  ports.OrderBookReader
}

func NewEventPrices(events ports.EventReader, books ports.OrderBookReader) *EventPrices {
	return &EventPrices{events: events, books: books}
}

var eventPricesField = engine.Field{Name: "prices", Kind: engine.FieldObject, Description: "The event's prices added up.", Fields: []engine.Field{
	shape.Text("event", "The event's title."),
	shape.Text("eventSlug", "Its slug."),
	shape.Decimal("asks", "The best asks of every outcome's Yes added up: what buying one share of each costs."),
	shape.Decimal("bids", "The best bids added up: what selling one of each brings."),
	shape.Decimal("mids", "The midpoints added up, of the outcomes with both sides."),
	shape.Number("outcomes", "The outcomes taking orders, counted."),
	shape.Number("leftOut", "The event's markets left out: closed, resolved or not taking orders."),
	shape.Number("noAsk", "Outcomes with no ask: buying every Yes is not possible while any has none."),
	shape.Number("noBid", "Outcomes with no bid."),
	shape.ListOf("each", "Each outcome's prices.",
		shape.Text("question", "Its market's question."),
		shape.Text("marketId", "Its market."),
		shape.Text("tokenId", "Its Yes token."),
		shape.Nullable(shape.Decimal("bid", "Its best bid.")),
		shape.Nullable(shape.Decimal("ask", "Its best ask.")),
		shape.Nullable(shape.Decimal("mid", "Its midpoint.")),
	),
}}

func (*EventPrices) Type() engine.NodeType {
	return engine.NodeType{
		Name:  EventPricesType,
		Label: "Event prices",
		Description: "Adds up the Yes prices of every outcome of an event that still takes orders: the best asks, what " +
			"buying one of each costs, the best bids and the midpoints, from their order books now. Above 1, a set of " +
			"buckets is overpriced.",
		Inputs:    []string{engine.DefaultPort},
		Outputs:   []string{engine.DefaultPort},
		SeesItems: true,
		Items:     &engine.Items{Passed: true, Named: "field", Fields: []engine.Field{eventPricesField}},
		Params: []engine.ParamSpec{
			{
				Name: "event", Label: "Event", Kind: engine.ParamString, Required: true,
				Description: "Its link, like https://polymarket.com/event/…, its slug or its ID; from a market, {{ eventSlug }}.",
			},
			{Name: "field", Label: "Prices field", Kind: engine.ParamString, Default: "prices", Description: "Where the sums go on the item."},
		},
	}
}

type eventItem struct {
	item  engine.Item
	src   engine.ItemRef
	event string
	field string
}

func (n *EventPrices) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	var work []eventItem

	for i, item := range in[engine.DefaultPort] {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		event, err := p.String("event")
		if err != nil {
			out.Fail(item, src, err)
			continue
		}

		field, err := p.String("field")
		if err != nil {
			return nil, err
		}

		work = append(work, eventItem{item: item, src: src, event: strings.TrimSpace(event), field: field})
	}

	// Each event read once, however many items name it; then every Yes book at once.
	events := map[string]domain.Event{}
	failed := map[string]error{}

	var tokens []domain.TokenID

	for _, w := range work {
		if _, done := events[w.event]; done || failed[w.event] != nil {
			continue
		}

		e, err := n.event(ctx, w.event)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}

			failed[w.event] = err

			continue
		}

		events[w.event] = e

		for _, o := range yesOf(e) {
			tokens = append(tokens, o.TokenID)
		}
	}

	books := map[domain.TokenID]domain.OrderBook{}

	if len(tokens) > 0 {
		got, err := n.books.OrderBooks(ctx, tokens...)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}

			for _, w := range work {
				out.Fail(w.item, w.src, err)
			}

			return out, nil
		}

		for _, b := range got {
			books[b.TokenID] = b
		}
	}

	for _, w := range work {
		if err := failed[w.event]; err != nil {
			out.Fail(w.item, w.src, err)
			continue
		}

		e, ok := events[w.event]
		if !ok {
			continue // its event param failed, above
		}

		j := maps.Clone(w.item.JSON)
		if j == nil {
			j = map[string]any{}
		}

		j[w.field] = sums(e, books)
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{w.src}})
	}

	return out, nil
}

// event reads an event by its link, its slug or its ID.
func (n *EventPrices) event(ctx context.Context, ref string) (domain.Event, error) {
	if ref == "" {
		return domain.Event{}, errors.New("no event: give its link, slug or ID")
	}

	slug := ref

	if u, err := url.Parse(ref); err == nil && u.Host != "" {
		// https://polymarket.com/event/<slug>, maybe a market's after it.
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 2 || parts[0] != "event" {
			return domain.Event{}, fmt.Errorf("%q is no event's link: one is https://polymarket.com/event/…", ref)
		}

		slug = parts[1]
	}

	var (
		e   domain.Event
		err error
	)

	if isID(slug) {
		e, err = n.events.GetEvent(ctx, domain.EventID(slug))
	} else {
		e, err = n.events.GetEventBySlug(ctx, slug)
	}

	if errors.Is(err, domain.ErrNotFound) {
		return domain.Event{}, fmt.Errorf("no event %q on Polymarket", ref)
	}

	return e, err
}

func isID(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return s != ""
}

// yesOf are the Yes tokens of an event's markets taking orders.
func yesOf(e domain.Event) []domain.Outcome {
	var out []domain.Outcome

	for _, m := range e.Markets {
		if yes, ok := yesIn(m); ok {
			out = append(out, yes)
		}
	}

	return out
}

// yesIn is a market's Yes outcome, the one labelled Yes or else its first, while it takes
// orders.
func yesIn(m domain.Market) (domain.Outcome, bool) {
	if m.Status != domain.MarketOpen || !m.AcceptingOrders || len(m.Outcomes) == 0 {
		return domain.Outcome{}, false
	}

	yes := m.Outcomes[0]

	for _, o := range m.Outcomes {
		if strings.EqualFold(o.Label, "yes") {
			yes = o
			break
		}
	}

	return yes, yes.TokenID != ""
}

// sums are an event's Yes prices added up, as their books have them.
func sums(e domain.Event, books map[domain.TokenID]domain.OrderBook) map[string]any {
	var (
		asks, bids, mids       decimal.Decimal
		outcomes, noAsk, noBid int
		each                   []any
	)

	for _, m := range e.Markets {
		yes, ok := yesIn(m)
		if !ok {
			continue
		}

		token := yes.TokenID
		outcomes++
		b := books[token]
		row := map[string]any{"question": m.Question, "marketId": string(m.ID), "tokenId": string(token), "bid": nil, "ask": nil, "mid": nil}

		if bid, ok := b.BestBid(); ok {
			bids, row["bid"] = bids.Add(bid.Price), bid.Price
		} else {
			noBid++
		}

		if ask, ok := b.BestAsk(); ok {
			asks, row["ask"] = asks.Add(ask.Price), ask.Price
		} else {
			noAsk++
		}

		if mid, ok := b.Mid(); ok {
			mids, row["mid"] = mids.Add(mid), mid
		}

		each = append(each, row)
	}

	if each == nil {
		each = []any{}
	}

	return map[string]any{
		"event": e.Title, "eventSlug": e.Slug,
		"asks": asks, "bids": bids, "mids": mids,
		"outcomes": outcomes, "leftOut": len(e.Markets) - outcomes, "noAsk": noAsk, "noBid": noBid,
		"each": each,
	}
}
