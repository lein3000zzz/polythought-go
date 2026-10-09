package polymarket

import "github.com/shopspring/decimal"

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type PriceLevel struct {
	Price decimal.Decimal `json:"price"`
	Size  decimal.Decimal `json:"size"`
}

// OrderBook levels are sorted worst to best: the best bid and best ask are the last elements.
type OrderBook struct {
	Market         string          `json:"market"`
	AssetID        string          `json:"asset_id"`
	Timestamp      string          `json:"timestamp"`
	Hash           string          `json:"hash"`
	Bids           []PriceLevel    `json:"bids"`
	Asks           []PriceLevel    `json:"asks"`
	MinOrderSize   decimal.Decimal `json:"min_order_size"`
	TickSize       decimal.Decimal `json:"tick_size"`
	NegRisk        bool            `json:"neg_risk"`
	LastTradePrice OptDecimal      `json:"last_trade_price"`
}

type PriceRequest struct {
	TokenID string `json:"token_id"`
	Side    Side   `json:"side"`
}

// SidePrices maps a side to its price for one token.
type SidePrices map[Side]decimal.Decimal

type LastTrade struct {
	TokenID string          `json:"token_id"`
	Price   decimal.Decimal `json:"price"`
	Side    Side            `json:"side"`
}

// MarketInfo is GET /clob-markets/{condition_id}, which uses abbreviated keys.
type MarketInfo struct {
	ConditionID  string            `json:"c"`
	Tokens       []MarketInfoToken `json:"t"`
	MinOrderSize decimal.Decimal   `json:"mos"`
	MinTickSize  decimal.Decimal   `json:"mts"`
	MakerBaseFee decimal.Decimal   `json:"mbf"`
	TakerBaseFee decimal.Decimal   `json:"tbf"`
	Fees         MarketInfoFees    `json:"fd"`
	RFQEnabled   bool              `json:"rfqe"`
	Version      string            `json:"v"`
}

type MarketInfoToken struct {
	TokenID string `json:"t"`
	Outcome string `json:"o"`
}

type MarketInfoFees struct {
	Rate      decimal.Decimal `json:"r"`
	Exponent  decimal.Decimal `json:"e"`
	TakerOnly bool            `json:"to"`
}

// Cursors of the CLOB's paginated endpoints.
const (
	clobFirstCursor = "MA=="
	clobEndCursor   = "LTE="
)

type AssetType string

const (
	AssetCollateral  AssetType = "COLLATERAL"
	AssetConditional AssetType = "CONDITIONAL"
)

// OpenOrder is one of the account's orders. Sizes are shares, already normalized.
type OpenOrder struct {
	ID              string          `json:"id"`
	Status          string          `json:"status"` // LIVE, MATCHED, CANCELED, CANCELED_MARKET_RESOLVED, INVALID
	Market          string          `json:"market"`
	AssetID         string          `json:"asset_id"`
	Side            Side            `json:"side"`
	OriginalSize    decimal.Decimal `json:"original_size"`
	SizeMatched     decimal.Decimal `json:"size_matched"`
	Price           decimal.Decimal `json:"price"`
	Outcome         string          `json:"outcome"`
	OrderType       string          `json:"order_type"`
	MakerAddress    string          `json:"maker_address"`
	CreatedAt       int64           `json:"created_at"`
	Expiration      string          `json:"expiration"` // unix seconds, "0" for none
	AssociateTrades []string        `json:"associate_trades"`
}

type OpenOrdersQuery struct {
	ID      string
	Market  string // condition ID
	AssetID string // token ID
	Cursor  string
}

type BalanceQuery struct {
	AssetType     AssetType
	TokenID       string // for CONDITIONAL
	SignatureType int
}

// BalanceAllowance amounts are fixed-point integers with 6 decimals, as the CLOB sends
// them: 1500000 is 1.5 USD.
type BalanceAllowance struct {
	Balance    decimal.Decimal            `json:"balance"`
	Allowances map[string]decimal.Decimal `json:"allowances"`
}
