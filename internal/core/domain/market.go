package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type MarketStatus string

const (
	MarketOpen     MarketStatus = "open"
	MarketClosed   MarketStatus = "closed"
	MarketResolved MarketStatus = "resolved"
	MarketArchived MarketStatus = "archived"
)

// Event groups related markets, like every candidate of one election.
type Event struct {
	ID          EventID
	Slug        string
	Title       string
	Description string
	Markets     []Market
	Tags        []string
	NegRisk     bool
	StartsAt    time.Time
	EndsAt      time.Time
	Volume24h   decimal.Decimal
	Liquidity   decimal.Decimal
}

// Market is one question with its outcomes. Outcomes carry resolved token IDs,
// so callers never deal with the API's market versions.
type Market struct {
	ID              MarketID
	Slug            string
	Question        string
	Description     string
	Outcomes        []Outcome
	Status          MarketStatus
	AcceptingOrders bool
	NegRisk         bool
	TickSize        decimal.Decimal
	MinOrderSize    decimal.Decimal
	Volume24h       decimal.Decimal
	Liquidity       decimal.Decimal
	EndsAt          time.Time
	Tags            []string // what it is about, like Politics or Crypto
}

type Outcome struct {
	TokenID TokenID
	Label   string
	Price   decimal.Decimal
}

func (m Market) Outcome(id TokenID) (Outcome, bool) {
	for _, o := range m.Outcomes {
		if o.TokenID == id {
			return o, true
		}
	}

	return Outcome{}, false
}

func (m Market) TokenIDs() []TokenID {
	ids := make([]TokenID, len(m.Outcomes))
	for i, o := range m.Outcomes {
		ids[i] = o.TokenID
	}

	return ids
}
