// Package markets holds the nodes that read market data through the core ports.
package markets

import (
	"errors"
	"fmt"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

var ErrNoOutcomes = errors.New("item has no outcomes with token ids")

// marketItem is the JSON shape of a market. Decimals and times stay typed: they marshal
// to strings and RFC 3339, and engine.Params reads either form back.
func marketItem(m domain.Market) map[string]any {
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

// outcomesOf reads the outcomes of a market item. It accepts the shape marketItem builds
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
