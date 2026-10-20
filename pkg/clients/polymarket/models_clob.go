package polymarket

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

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
	NegRisk      bool              `json:"nr"` // its orders go to the neg risk exchange
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

// TradeStatus is how far a trade's settlement on chain has come. The REST API prefixes
// it (TRADE_STATUS_CONFIRMED), the WebSocket does not; both read as the bare word.
type TradeStatus string

const (
	TradeMatched               TradeStatus = "MATCHED" // sent to be executed on chain
	TradeMatchedNotBroadcasted TradeStatus = "MATCHED_NOT_BROADCASTED"
	TradeMined                 TradeStatus = "MINED"    // in a block, not final
	TradeRetrying              TradeStatus = "RETRYING" // failed for now; it may yet be mined
	TradeConfirmed             TradeStatus = "CONFIRMED"
	TradeFailed                TradeStatus = "FAILED"
)

const tradeStatusPrefix = "TRADE_STATUS_"

func (s *TradeStatus) UnmarshalJSON(b []byte) error {
	var raw string
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	*s = TradeStatus(strings.TrimPrefix(strings.ToUpper(raw), tradeStatusPrefix))

	return nil
}

// Final is whether the trade will not change again: confirmed, or failed.
func (s TradeStatus) Final() bool {
	return s == TradeConfirmed || s == TradeFailed
}

// TraderSide is which side of a trade the account was on.
type TraderSide string

const (
	TraderTaker TraderSide = "TAKER"
	TraderMaker TraderSide = "MAKER" // its part is among MakerOrders
)

// ClobTrade is one of the account's trades: a taker order matched against makers' at
// one go, the account on either side. Sizes are shares, prices pUSD a share.
type ClobTrade struct {
	ID              string          `json:"id"`
	TakerOrderID    string          `json:"taker_order_id"`
	Market          string          `json:"market"`   // condition ID
	AssetID         string          `json:"asset_id"` // token ID
	Side            Side            `json:"side"`     // the taker's
	Size            decimal.Decimal `json:"size"`
	Price           decimal.Decimal `json:"price"`
	FeeRateBps      OptDecimal      `json:"fee_rate_bps"` // what it means since V2 is not documented
	Status          TradeStatus     `json:"status"`
	MatchTime       EpochTime       `json:"match_time"`
	LastUpdate      EpochTime       `json:"last_update"`
	Outcome         string          `json:"outcome"`
	BucketIndex     int             `json:"bucket_index"`
	Owner           string          `json:"owner"`         // the taker's API key
	MakerAddress    string          `json:"maker_address"` // the taker order's maker
	TransactionHash string          `json:"transaction_hash"`
	TraderSide      TraderSide      `json:"trader_side"`
	MakerOrders     []MakerOrder    `json:"maker_orders"`
	ErrMsg          string          `json:"err_msg"`
}

// MakerOrder is a maker order's part in a trade.
type MakerOrder struct {
	OrderID       string          `json:"order_id"`
	Owner         string          `json:"owner"`
	MakerAddress  string          `json:"maker_address"`
	MatchedAmount decimal.Decimal `json:"matched_amount"` // shares
	Price         decimal.Decimal `json:"price"`
	FeeRateBps    OptDecimal      `json:"fee_rate_bps"`
	AssetID       string          `json:"asset_id"`
	Outcome       string          `json:"outcome"`
	Side          Side            `json:"side"`
}

// ClobTradesQuery filters the account's trades; all are optional. After and Before are Unix
// seconds.
type ClobTradesQuery struct {
	ID           string
	MakerAddress string
	Market       string // condition ID
	AssetID      string // token ID
	After        int64
	Before       int64
	Cursor       string
}

// Cancellation is how a cancel went: the orders cancelled, and why each other was not.
type Cancellation struct {
	Canceled    []string          `json:"canceled"`
	NotCanceled map[string]string `json:"not_canceled"`
}

// CancelMarketQuery names the orders to cancel: a market's, an outcome's, or both.
type CancelMarketQuery struct {
	AssetID string `json:"asset_id,omitempty"` // token ID
	Market  string `json:"market,omitempty"`   // condition ID
}

// EpochTime is a time the CLOB gives as Unix seconds or milliseconds, a number or text,
// or as a date; zero when absent.
type EpochTime struct{ time.Time }

// epochMillis is where milliseconds start, as @polymarket/client tells them.
const epochMillis = 1_000_000_000_000

func (t *EpochTime) UnmarshalJSON(b []byte) error {
	raw := strings.Trim(string(b), `"`)
	if raw == "" || raw == "null" || raw == "0" {
		*t = EpochTime{}
		return nil
	}

	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		if n < epochMillis {
			t.Time = time.Unix(n, 0).UTC()
		} else {
			t.Time = time.UnixMilli(n).UTC()
		}

		return nil
	}

	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return fmt.Errorf("polymarket: %q is no time", raw)
	}

	t.Time = at.UTC()

	return nil
}
