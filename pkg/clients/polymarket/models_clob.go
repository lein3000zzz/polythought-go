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
