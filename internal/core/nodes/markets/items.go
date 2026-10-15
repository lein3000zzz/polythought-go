// Package markets holds the nodes that read market data through the core ports.
package markets

import (
	"errors"
	"fmt"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
)

var ErrNoOutcomes = errors.New("item has no outcomes with token ids")

// MarketItem is the JSON shape of a market, in items and in the SDK's answers. Decimals
// and times stay typed: they marshal to strings and RFC 3339, and engine.Params reads
// either form back.
func MarketItem(m domain.Market) map[string]any {
	outcomes := make([]any, len(m.Outcomes))
	for i, o := range m.Outcomes {
		outcomes[i] = map[string]any{
			"tokenId": string(o.TokenID),
			"label":   o.Label,
			"price":   o.Price,
		}
	}

	return map[string]any{
		"id":              string(m.ID),
		"slug":            m.Slug,
		"question":        m.Question,
		"status":          string(m.Status),
		"acceptingOrders": m.AcceptingOrders,
		"negRisk":         m.NegRisk,
		"tickSize":        m.TickSize,
		"minOrderSize":    m.MinOrderSize,
		"volume24h":       m.Volume24h,
		"liquidity":       m.Liquidity,
		"endsAt":          m.EndsAt,
		"outcomes":        outcomes,
	}
}

type outcomeRef struct {
	tokenID domain.TokenID
	label   string
}

// outcomesOf reads the outcomes of a market item. It accepts the shape MarketItem builds
// and the same data after a JSON round trip.
func outcomesOf(item engine.Item) ([]outcomeRef, error) {
	raw, ok := item.JSON["outcomes"].([]any)
	if !ok {
		return nil, ErrNoOutcomes
	}

	refs := make([]outcomeRef, 0, len(raw))

	for i, o := range raw {
		fields, ok := o.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("outcome %d is %T, not an object", i, o)
		}

		token, _ := fields["tokenId"].(string)
		label, _ := fields["label"].(string)

		if token != "" {
			refs = append(refs, outcomeRef{tokenID: domain.TokenID(token), label: label})
		}
	}

	if len(refs) == 0 {
		return nil, ErrNoOutcomes
	}

	return refs, nil
}

// optionalStrings reads a list param that may be absent.
func optionalStrings(p engine.Params, name string) ([]string, error) {
	list, err := p.Strings(name)
	if errors.Is(err, engine.ErrMissingParam) {
		return nil, nil
	}

	return list, err
}

// MarketFields describe a MarketItem.
func MarketFields() []engine.Field {
	return []engine.Field{
		shape.Text("id", "The market's condition ID, 0x…"),
		shape.Text("slug", "Its name in its polymarket.com address."),
		shape.Text("question", "What it settles."),
		shape.OneOf("status", "Whether it trades.", string(domain.MarketOpen), string(domain.MarketClosed),
			string(domain.MarketResolved), string(domain.MarketArchived)),
		shape.Bool("acceptingOrders", "Whether its order books take orders now."),
		shape.Bool("negRisk", "Part of an event whose outcomes exclude each other."),
		shape.Decimal("tickSize", "The smallest price step."),
		shape.Decimal("minOrderSize", "The smallest order, in shares."),
		shape.Decimal("volume24h", "Dollars traded in the last 24 hours."),
		shape.Decimal("liquidity", "Dollars on its order books."),
		shape.Time("endsAt", "When it is expected to settle."),
		shape.ListOf("outcomes", "What it can settle as, such as Yes and No.",
			shape.Text("tokenId", "The outcome's token ID."),
			shape.Text("label", "Like Yes or No."),
			shape.Decimal("price", "The last price, a probability from 0 to 1."),
		),
	}
}

// FoundFields describe a market a search found: MarketFields with its event.
func FoundFields() []engine.Field {
	return append(MarketFields(),
		shape.Text("event", "The title of the event it belongs to."),
		shape.Text("eventSlug", "The event's name in its polymarket.com address."),
	)
}

// The fields per outcome that nodes reading a market's outcomes add to what they say.
func outcomeFields() []engine.Field {
	return []engine.Field{
		shape.Nullable(shape.Text("marketId", "The market's ID, from the item received.")),
		shape.Nullable(shape.Text("question", "The market's question, from the item received.")),
		shape.Text("outcome", "The outcome's label, like Yes."),
		shape.Text("tokenId", "The outcome's token ID."),
	}
}
