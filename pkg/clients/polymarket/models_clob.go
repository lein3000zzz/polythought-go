package polymarket

import "encoding/json"

type Side string

const (
	SideBuy  Side = "BUY"
	SideSell Side = "SELL"
)

type PriceLevel struct {
	Price string `json:"price"`
	Size  string `json:"size"`
}

// OrderBook levels are sorted worst to best: the best bid and best ask are the last elements.
type OrderBook struct {
	Market         string       `json:"market"`
	AssetID        string       `json:"asset_id"`
	Timestamp      string       `json:"timestamp"`
	Hash           string       `json:"hash"`
	Bids           []PriceLevel `json:"bids"`
	Asks           []PriceLevel `json:"asks"`
	MinOrderSize   string       `json:"min_order_size"`
	TickSize       string       `json:"tick_size"`
	NegRisk        bool         `json:"neg_risk"`
	LastTradePrice string       `json:"last_trade_price"`
}

type PriceRequest struct {
	TokenID string `json:"token_id"`
	Side    Side   `json:"side"`
}

// SidePrices maps a side to its price for one token.
type SidePrices map[Side]string

type LastTrade struct {
	TokenID string `json:"token_id"`
	Price   string `json:"price"`
	Side    Side   `json:"side"`
}

// MarketInfo is GET /clob-markets/{condition_id}, which uses abbreviated keys.
type MarketInfo struct {
	ConditionID  string            `json:"c"`
	Tokens       []MarketInfoToken `json:"t"`
	MinOrderSize json.Number       `json:"mos"`
	MinTickSize  json.Number       `json:"mts"`
	MakerBaseFee json.Number       `json:"mbf"`
	TakerBaseFee json.Number       `json:"tbf"`
	Fees         MarketInfoFees    `json:"fd"`
	RFQEnabled   bool              `json:"rfqe"`
	Version      string            `json:"v"`
}

type MarketInfoToken struct {
	TokenID string `json:"t"`
	Outcome string `json:"o"`
}

type MarketInfoFees struct {
	Rate      json.Number `json:"r"`
	Exponent  json.Number `json:"e"`
	TakerOnly bool        `json:"to"`
}
