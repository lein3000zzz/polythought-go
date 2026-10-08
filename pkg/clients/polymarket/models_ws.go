package polymarket

import (
	"encoding/json"

	"github.com/shopspring/decimal"
)

type MarketEventType string

const (
	EventBook           MarketEventType = "book"
	EventPriceChange    MarketEventType = "price_change"
	EventLastTradePrice MarketEventType = "last_trade_price"
	EventTickSizeChange MarketEventType = "tick_size_change"
	EventBestBidAsk     MarketEventType = "best_bid_ask"
	EventMarketResolved MarketEventType = "market_resolved"
)

// MarketEvent is one message from the market channel. Switch on the concrete type.
type MarketEvent interface {
	EventType() MarketEventType
}

type BookEvent struct {
	AssetID        string          `json:"asset_id"`
	Market         string          `json:"market"`
	Timestamp      string          `json:"timestamp"`
	Hash           string          `json:"hash"`
	Bids           []PriceLevel    `json:"bids"`
	Asks           []PriceLevel    `json:"asks"`
	TickSize       decimal.Decimal `json:"tick_size"`
	LastTradePrice OptDecimal      `json:"last_trade_price"`
}

type PriceChangeEvent struct {
	Market    string        `json:"market"`
	Timestamp string        `json:"timestamp"`
	Changes   []PriceChange `json:"price_changes"`
}

// PriceChange is one level update. A zero Size means the level was removed.
type PriceChange struct {
	AssetID string          `json:"asset_id"`
	Price   decimal.Decimal `json:"price"`
	Size    decimal.Decimal `json:"size"`
	Side    Side            `json:"side"`
	Hash    string          `json:"hash"`
	BestBid decimal.Decimal `json:"best_bid"`
	BestAsk decimal.Decimal `json:"best_ask"`
}

type LastTradePriceEvent struct {
	AssetID         string          `json:"asset_id"`
	Market          string          `json:"market"`
	Price           decimal.Decimal `json:"price"`
	Size            decimal.Decimal `json:"size"`
	Side            Side            `json:"side"`
	FeeRateBps      decimal.Decimal `json:"fee_rate_bps"`
	Timestamp       string          `json:"timestamp"`
	TransactionHash string          `json:"transaction_hash"`
}

type TickSizeChangeEvent struct {
	AssetID     string          `json:"asset_id"`
	Market      string          `json:"market"`
	OldTickSize decimal.Decimal `json:"old_tick_size"`
	NewTickSize decimal.Decimal `json:"new_tick_size"`
	Timestamp   string          `json:"timestamp"`
}

type BestBidAskEvent struct {
	AssetID   string          `json:"asset_id"`
	Market    string          `json:"market"`
	BestBid   decimal.Decimal `json:"best_bid"`
	BestAsk   decimal.Decimal `json:"best_ask"`
	Spread    decimal.Decimal `json:"spread"`
	Timestamp string          `json:"timestamp"`
}

type MarketResolvedEvent struct {
	Market         string `json:"market"`
	WinningAssetID string `json:"winning_asset_id"`
	WinningOutcome string `json:"winning_outcome"`
	Timestamp      string `json:"timestamp"`
}

// UnknownEvent carries event types this package does not model yet.
type UnknownEvent struct {
	Type MarketEventType
	Raw  json.RawMessage
}

func (BookEvent) EventType() MarketEventType           { return EventBook }
func (PriceChangeEvent) EventType() MarketEventType    { return EventPriceChange }
func (LastTradePriceEvent) EventType() MarketEventType { return EventLastTradePrice }
func (TickSizeChangeEvent) EventType() MarketEventType { return EventTickSizeChange }
func (BestBidAskEvent) EventType() MarketEventType     { return EventBestBidAsk }
func (MarketResolvedEvent) EventType() MarketEventType { return EventMarketResolved }
func (e UnknownEvent) EventType() MarketEventType      { return e.Type }
