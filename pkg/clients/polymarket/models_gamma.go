package polymarket

import "github.com/shopspring/decimal"

// Market is a Gamma market. Only the fields used so far are declared; the rest are ignored.
type Market struct {
	ID                    string                       `json:"id"`
	Question              string                       `json:"question"`
	Description           string                       `json:"description"`
	ConditionID           string                       `json:"conditionId"`
	Slug                  string                       `json:"slug"`
	Version               string                       `json:"version"`
	Outcomes              Stringified[string]          `json:"outcomes"`
	OutcomePrices         Stringified[decimal.Decimal] `json:"outcomePrices"`
	ClobTokenIDs          Stringified[string]          `json:"clobTokenIds"`
	PositionIDs           []string                     `json:"positionIds"`
	Active                bool                         `json:"active"`
	Closed                bool                         `json:"closed"`
	Archived              bool                         `json:"archived"`
	EnableOrderBook       bool                         `json:"enableOrderBook"`
	AcceptingOrders       bool                         `json:"acceptingOrders"`
	NegRisk               bool                         `json:"negRisk"`
	UMAResolutionStatus   string                       `json:"umaResolutionStatus"` // v1: "resolved" once settled
	ResolutionStatus      string                       `json:"resolutionStatus"`    // v2: inactive, active, resolved
	StartDate             string                       `json:"startDate"`
	EndDate               string                       `json:"endDate"`
	Volume                decimal.Decimal              `json:"volume"`
	Liquidity             decimal.Decimal              `json:"liquidity"`
	VolumeNum             decimal.Decimal              `json:"volumeNum"`
	LiquidityNum          decimal.Decimal              `json:"liquidityNum"`
	Volume24hr            decimal.Decimal              `json:"volume24hr"`
	BestBid               decimal.Decimal              `json:"bestBid"`
	BestAsk               decimal.Decimal              `json:"bestAsk"`
	Spread                decimal.Decimal              `json:"spread"`
	LastTradePrice        decimal.Decimal              `json:"lastTradePrice"`
	OneDayPriceChange     decimal.Decimal              `json:"oneDayPriceChange"`
	OrderPriceMinTickSize decimal.Decimal              `json:"orderPriceMinTickSize"`
	OrderMinSize          decimal.Decimal              `json:"orderMinSize"`
}

// TokenIDs returns the tradable IDs. v2 markets use positionIds, v1 use clobTokenIds.
func (m Market) TokenIDs() []string {
	if m.Version == "v2" {
		return m.PositionIDs
	}

	return m.ClobTokenIDs
}

type Event struct {
	ID              string          `json:"id"`
	Ticker          string          `json:"ticker"`
	Slug            string          `json:"slug"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	Version         string          `json:"version"`
	StartDate       string          `json:"startDate"`
	EndDate         string          `json:"endDate"`
	Active          bool            `json:"active"`
	Closed          bool            `json:"closed"`
	Archived        bool            `json:"archived"`
	Featured        bool            `json:"featured"`
	NegRisk         bool            `json:"negRisk"`
	EnableNegRisk   bool            `json:"enableNegRisk"`
	NegRiskMarketID string          `json:"negRiskMarketID"`
	Volume          decimal.Decimal `json:"volume"`
	Volume24hr      decimal.Decimal `json:"volume24hr"`
	Liquidity       decimal.Decimal `json:"liquidity"`
	OpenInterest    decimal.Decimal `json:"openInterest"`
	Markets         []Market        `json:"markets"`
	Tags            []Tag           `json:"tags"`
}

type Tag struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Slug  string `json:"slug"`
}

type Profile struct {
	Name         string `json:"name"`
	Pseudonym    string `json:"pseudonym"`
	ProxyWallet  string `json:"proxyWallet"`
	ProfileImage string `json:"profileImage"`
}

type SearchResult struct {
	Events     []Event          `json:"events"`
	Tags       []Tag            `json:"tags"`
	Profiles   []Profile        `json:"profiles"`
	Pagination SearchPagination `json:"pagination"`
}

type SearchPagination struct {
	HasMore      bool `json:"hasMore"`
	TotalResults int  `json:"totalResults"`
}

// MarketsQuery maps to GET /markets/keyset.
type MarketsQuery struct {
	Limit        int // max 100
	AfterCursor  string
	Closed       *bool // nil means the API default (false)
	TagIDs       []string
	ConditionIDs []string
	Slugs        []string
	TokenIDs     []string // the markets of these outcome tokens
}

// EventsQuery maps to GET /events/keyset.
type EventsQuery struct {
	Limit         int // max 100
	AfterCursor   string
	Closed        *bool
	Live          *bool
	Featured      *bool
	TitleSearch   string
	SeriesIDs     []string
	ExcludeTagIDs []string
}

type TagsQuery struct {
	Limit  int
	Offset int
}

// SearchQuery maps to GET /public-search, which pages by number rather than cursor.
type SearchQuery struct {
	Query        string
	LimitPerType int
	Page         int
	EventsStatus string
}
