package polymarket

import "encoding/json"

// Market is a Gamma market. Only the fields used so far are declared; the rest are ignored.
type Market struct {
	ID                    string           `json:"id"`
	Question              string           `json:"question"`
	Description           string           `json:"description"`
	ConditionID           string           `json:"conditionId"`
	Slug                  string           `json:"slug"`
	Version               string           `json:"version"`
	Outcomes              StringifiedSlice `json:"outcomes"`
	OutcomePrices         StringifiedSlice `json:"outcomePrices"`
	ClobTokenIDs          StringifiedSlice `json:"clobTokenIds"`
	PositionIDs           []string         `json:"positionIds"`
	Active                bool             `json:"active"`
	Closed                bool             `json:"closed"`
	Archived              bool             `json:"archived"`
	EnableOrderBook       bool             `json:"enableOrderBook"`
	AcceptingOrders       bool             `json:"acceptingOrders"`
	NegRisk               bool             `json:"negRisk"`
	StartDate             string           `json:"startDate"`
	EndDate               string           `json:"endDate"`
	Volume                string           `json:"volume"`
	Liquidity             string           `json:"liquidity"`
	VolumeNum             json.Number      `json:"volumeNum"`
	LiquidityNum          json.Number      `json:"liquidityNum"`
	Volume24hr            json.Number      `json:"volume24hr"`
	BestBid               json.Number      `json:"bestBid"`
	BestAsk               json.Number      `json:"bestAsk"`
	Spread                json.Number      `json:"spread"`
	LastTradePrice        json.Number      `json:"lastTradePrice"`
	OneDayPriceChange     json.Number      `json:"oneDayPriceChange"`
	OrderPriceMinTickSize json.Number      `json:"orderPriceMinTickSize"`
	OrderMinSize          json.Number      `json:"orderMinSize"`
}

// TokenIDs returns the tradable IDs. v2 markets use positionIds, v1 use clobTokenIds.
func (m Market) TokenIDs() []string {
	if m.Version == "v2" {
		return m.PositionIDs
	}

	return m.ClobTokenIDs
}

type Event struct {
	ID              string      `json:"id"`
	Ticker          string      `json:"ticker"`
	Slug            string      `json:"slug"`
	Title           string      `json:"title"`
	Description     string      `json:"description"`
	Version         string      `json:"version"`
	StartDate       string      `json:"startDate"`
	EndDate         string      `json:"endDate"`
	Active          bool        `json:"active"`
	Closed          bool        `json:"closed"`
	Archived        bool        `json:"archived"`
	Featured        bool        `json:"featured"`
	NegRisk         bool        `json:"negRisk"`
	EnableNegRisk   bool        `json:"enableNegRisk"`
	NegRiskMarketID string      `json:"negRiskMarketID"`
	Volume          json.Number `json:"volume"`
	Volume24hr      json.Number `json:"volume24hr"`
	Liquidity       json.Number `json:"liquidity"`
	OpenInterest    json.Number `json:"openInterest"`
	Markets         []Market    `json:"markets"`
	Tags            []Tag       `json:"tags"`
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
