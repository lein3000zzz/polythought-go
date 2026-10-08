package polymarket

import "encoding/json"

// Data API v2 amounts are JSON numbers. Bare size fields are shares, _usdc fields are USD.

type Position struct {
	ProxyWallet     string      `json:"proxy_wallet"`
	ConditionID     string      `json:"condition_id"`
	TokenID         string      `json:"token_id"`
	OppositeTokenID string      `json:"opposite_token_id"`
	Outcome         string      `json:"outcome"`
	OutcomeIndex    int         `json:"outcome_index"`
	CurrentSize     json.Number `json:"current_size"`
	AvgPrice        json.Number `json:"avg_price"`
	EntryCostUSDC   json.Number `json:"entry_cost_usdc"`
	EntryFeesUSDC   json.Number `json:"entry_fees_usdc"`
	CurrentPrice    json.Number `json:"current_price"`
	CurrentValue    json.Number `json:"current_value"`
	RealizedPnl     json.Number `json:"realized_pnl"`
	UnrealizedPnl   json.Number `json:"unrealized_pnl"`
	TotalPnl        json.Number `json:"total_pnl"`
	PercentPnl      json.Number `json:"percent_pnl"`
	Status          string      `json:"status"`
	Redeemable      bool        `json:"redeemable"`
	Mergeable       bool        `json:"mergeable"`
	NegativeRisk    bool        `json:"negative_risk"`
	EventID         string      `json:"event_id"`
	Slug            string      `json:"slug"`
	Title           string      `json:"title"`
	EndDate         string      `json:"end_date"`
	FirstEntryAt    int64       `json:"first_entry_at"`
	LastEventAt     int64       `json:"last_event_at"`
}

type Trade struct {
	ProxyWallet     string      `json:"proxy_wallet"`
	Side            Side        `json:"side"`
	TokenID         string      `json:"token_id"`
	ConditionID     string      `json:"condition_id"`
	Size            json.Number `json:"size"`
	Price           json.Number `json:"price"`
	Timestamp       int64       `json:"timestamp"`
	Title           string      `json:"title"`
	Slug            string      `json:"slug"`
	EventSlug       string      `json:"event_slug"`
	Outcome         string      `json:"outcome"`
	OutcomeIndex    int         `json:"outcome_index"`
	Name            string      `json:"name"`
	Pseudonym       string      `json:"pseudonym"`
	TransactionHash string      `json:"transaction_hash"`
}

type Activity struct {
	Trade

	Type     string      `json:"type"`
	USDCSize json.Number `json:"usdc_size"`
}

type TokenHolders struct {
	TokenID string   `json:"token_id"`
	Holders []Holder `json:"holders"`
}

type Holder struct {
	ProxyWallet  string      `json:"proxy_wallet"`
	TokenID      string      `json:"token_id"`
	Amount       json.Number `json:"amount"`
	OutcomeIndex int         `json:"outcome_index"`
	Name         string      `json:"name"`
	Pseudonym    string      `json:"pseudonym"`
}

type OpenInterest struct {
	ConditionID string      `json:"condition_id"`
	Value       json.Number `json:"value"`
}

type PricePoint struct {
	Timestamp         int64       `json:"timestamp"`
	Price             json.Number `json:"price"`
	ResolutionSeconds int         `json:"resolution_seconds"`
}

// PositionsQuery maps to GET /v2/positions. User and/or Conditions is required.
type PositionsQuery struct {
	User          string
	Conditions    []string // max 20
	Status        string   // OPEN (default), REDEEMABLE, REDEEMABLE_LOST, MERGEABLE, CLOSED
	EventID       string
	SortBy        string
	SortDirection string
	Limit         int // max 1000
	Cursor        string
}

// TradesQuery maps to GET /v2/trades.
type TradesQuery struct {
	User       string
	Conditions []string
	EventID    string
	Side       Side
	TakerOnly  *bool // nil means the API default (true)
	Start      int64
	End        int64
	Limit      int
	Cursor     string
}

// ActivityQuery maps to GET /v2/activity.
type ActivityQuery struct {
	User          string // required
	Types         []string
	Conditions    []string
	EventID       string
	Side          Side
	Start         int64
	End           int64
	SortDirection string
	Limit         int
	Cursor        string
}

// HoldersQuery maps to GET /v2/holders.
type HoldersQuery struct {
	Conditions []string // required
	Limit      int      // max 1000
	MinBalance string
}

// PriceHistoryQuery maps to GET /v2/prices-history. Set Interval or Start+End, not both.
type PriceHistoryQuery struct {
	TokenID       string
	Interval      string // max, all, 1m, 1w, 1d, 6h, 1h
	Start         int64
	End           int64 // exclusive
	BucketSeconds int   // 60 to 86400
	Limit         int   // max 10000
	Cursor        string
}
