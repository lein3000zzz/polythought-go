package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// The tools an agent may be given (D262): read-only, each reading what its node does.
const (
	ToolSearch    = "searchMarkets"
	ToolBook      = "orderBook"
	ToolHistory   = "priceHistory"
	ToolTrades    = "trades"
	ToolPositions = "positions"
)

// ToolNames are the tools, in the order the node offers them.
var ToolNames = []string{ToolSearch, ToolBook, ToolHistory, ToolTrades, ToolPositions}

// Sources are what the tools read. One left nil makes its tool fail when called.
type Sources struct {
	Events    ports.EventReader
	Books     ports.OrderBookReader
	History   ports.PriceHistoryReader
	Trades    ports.TradeReader
	Positions ports.PositionReader
}

// toolRun reads what a call asks for: the result for the model, and how many results
// it holds.
type toolRun func(ctx context.Context, s Sources, input json.RawMessage, results int, now time.Time) (any, int, error)

type toolDef struct {
	llm ports.LLMTool
	run toolRun
}

// windows are the spans price history covers.
var windows = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "1d": 24 * time.Hour, "1w": 7 * 24 * time.Hour, "1m": 30 * 24 * time.Hour,
}

// tools are the definitions, their schemas closed and requiring every field, so
// Claude's and OpenAI's strict modes hold the model to them.
var tools = map[string]toolDef{
	ToolSearch: {
		llm: ports.LLMTool{
			Name:        "search_markets",
			Description: "Finds Polymarket markets whose event title has these words: each with its question, outcomes and their token IDs, prices, volume and liquidity.",
			Schema: closedSchema(map[string]any{
				"query":  map[string]any{"type": "string", "description": "Words of the event's title, like bitcoin or election."},
				"closed": map[string]any{"type": "boolean", "description": "Whether to include markets that have closed."},
			}),
		},
		run: searchMarkets,
	},
	ToolBook: {
		llm: ports.LLMTool{
			Name:        "order_book",
			Description: "An outcome's live order book: the best bid and ask, the midpoint and spread, and the top levels of each side.",
			Schema: closedSchema(map[string]any{
				"tokenId": map[string]any{"type": "string", "description": "The outcome's token ID, as search_markets gives it."},
			}),
		},
		run: orderBook,
	},
	ToolHistory: {
		llm: ports.LLMTool{
			Name:        "price_history",
			Description: "An outcome's prices over a recent window, oldest first, with how much it moved.",
			Schema: closedSchema(map[string]any{
				"tokenId": map[string]any{"type": "string", "description": "The outcome's token ID."},
				"window":  map[string]any{"type": "string", "enum": []any{"1h", "6h", "1d", "1w", "1m"}, "description": "How far back."},
			}),
		},
		run: priceHistory,
	},
	ToolTrades: {
		llm: ports.LLMTool{
			Name:        "recent_trades",
			Description: "A market's most recent trades, newest first: side, price, size and the trader's address.",
			Schema: closedSchema(map[string]any{
				"marketId": map[string]any{"type": "string", "description": "The market's condition ID, 0x…, as search_markets gives it."},
			}),
		},
		run: recentTrades,
	},
	ToolPositions: {
		llm: ports.LLMTool{
			Name:        "positions",
			Description: "The open positions of a Polymarket address, public on the chain, largest first: size, average price, value and profit.",
			Schema: closedSchema(map[string]any{
				"address": map[string]any{"type": "string", "description": "The wallet's address, 0x and 40 hex digits."},
			}),
		},
		run: positions,
	},
}

func closedSchema(props map[string]any) json.RawMessage {
	required := make([]string, 0, len(props))
	for name := range props {
		required = append(required, name)
	}

	slices.Sort(required)

	raw, _ := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false, "properties": props, "required": required,
	})

	return raw
}

// decodeInput reads a call's input into v, refusing a field it does not know: the
// providers stream it as the model writes it, unchecked.
func decodeInput(input json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the input is not what the tool takes: %w", err)
	}

	return nil
}

var errUnavailable = errors.New("this tool is not available on this instance")

func searchMarkets(ctx context.Context, s Sources, input json.RawMessage, results int, _ time.Time) (any, int, error) {
	var in struct {
		Query  string `json:"query"`
		Closed bool   `json:"closed"`
	}

	if err := decodeInput(input, &in); err != nil {
		return nil, 0, err
	}

	if s.Events == nil {
		return nil, 0, errUnavailable
	}

	if in.Query = strings.TrimSpace(in.Query); in.Query == "" {
		return nil, 0, errors.New("query: say what to look for")
	}

	found, err := markets.SearchMarkets(ctx, s.Events, in.Query, results, in.Closed)
	if err != nil {
		return nil, 0, err
	}

	return map[string]any{"markets": found}, len(found), nil
}

func orderBook(ctx context.Context, s Sources, input json.RawMessage, results int, _ time.Time) (any, int, error) {
	var in struct {
		TokenID string `json:"tokenId"`
	}

	if err := decodeInput(input, &in); err != nil {
		return nil, 0, err
	}

	if s.Books == nil {
		return nil, 0, errUnavailable
	}

	if in.TokenID = strings.TrimSpace(in.TokenID); in.TokenID == "" {
		return nil, 0, errors.New("tokenId: name the outcome")
	}

	books, err := s.Books.OrderBooks(ctx, domain.TokenID(in.TokenID))
	if err != nil {
		return nil, 0, err
	}

	if len(books) == 0 {
		return nil, 0, fmt.Errorf("no order book for token %s", in.TokenID)
	}

	b := books[0]
	out := map[string]any{"tokenId": in.TokenID, "bestBid": nil, "bestAsk": nil, "mid": nil, "spread": nil, "at": b.At}

	if bid, ok := b.BestBid(); ok {
		out["bestBid"] = bid.Price
	}

	if ask, ok := b.BestAsk(); ok {
		out["bestAsk"] = ask.Price
	}

	if mid, ok := b.Mid(); ok {
		out["mid"] = mid
	}

	if spread, ok := b.Spread(); ok {
		out["spread"] = spread
	}

	bids, asks := levels(b.Bids, results), levels(b.Asks, results)
	out["bids"], out["asks"] = bids, asks

	return out, len(bids) + len(asks), nil
}

func levels(side []domain.Level, n int) []map[string]any {
	out := make([]map[string]any, 0, min(n, len(side)))
	for _, l := range side[:min(n, len(side))] {
		out = append(out, map[string]any{"price": l.Price, "size": l.Size})
	}

	return out
}

func priceHistory(ctx context.Context, s Sources, input json.RawMessage, results int, now time.Time) (any, int, error) {
	var in struct {
		TokenID string `json:"tokenId"`
		Window  string `json:"window"`
	}

	if err := decodeInput(input, &in); err != nil {
		return nil, 0, err
	}

	if s.History == nil {
		return nil, 0, errUnavailable
	}

	window, ok := windows[in.Window]

	switch {
	case strings.TrimSpace(in.TokenID) == "":
		return nil, 0, errors.New("tokenId: name the outcome")
	case !ok:
		return nil, 0, fmt.Errorf("window: %q is not 1h, 6h, 1d, 1w or 1m", in.Window)
	}

	page, err := s.History.PriceHistory(ctx, ports.PriceHistoryFilter{
		TokenID: domain.TokenID(strings.TrimSpace(in.TokenID)), Since: now.Add(-window),
		Bucket: max(window/time.Duration(results), time.Minute),
	})
	if err != nil {
		return nil, 0, err
	}

	points := slices.SortedFunc(slices.Values(page.Items), func(a, b domain.PricePoint) int { return a.At.Compare(b.At) })
	points = evenly(points, results)

	out := map[string]any{"tokenId": in.TokenID, "window": in.Window, "points": []map[string]any{}, "change": nil, "changePct": nil}

	list := make([]map[string]any, len(points))
	for i, p := range points {
		list[i] = map[string]any{"at": p.At.UTC(), "price": p.Price}
	}

	out["points"] = list

	if len(points) > 1 {
		then, last := points[0].Price, points[len(points)-1].Price
		out["change"] = last.Sub(then)

		if !then.IsZero() {
			out["changePct"] = last.Sub(then).Div(then).Mul(decimal.NewFromInt(100)).Round(2)
		}
	}

	return out, len(points), nil
}

// evenly keeps n of the points, evenly, the first and the last among them.
func evenly(points []domain.PricePoint, n int) []domain.PricePoint {
	if len(points) <= n || n < 2 {
		return points[:min(len(points), max(n, 0))]
	}

	out := make([]domain.PricePoint, n)
	for i := range out {
		out[i] = points[i*(len(points)-1)/(n-1)]
	}

	return out
}

func recentTrades(ctx context.Context, s Sources, input json.RawMessage, results int, _ time.Time) (any, int, error) {
	var in struct {
		MarketID string `json:"marketId"`
	}

	if err := decodeInput(input, &in); err != nil {
		return nil, 0, err
	}

	if s.Trades == nil {
		return nil, 0, errUnavailable
	}

	if in.MarketID = strings.TrimSpace(in.MarketID); in.MarketID == "" {
		return nil, 0, errors.New("marketId: name the market")
	}

	page, err := s.Trades.Trades(ctx, ports.TradeFilter{MarketIDs: []domain.MarketID{domain.MarketID(in.MarketID)}, Limit: results})
	if err != nil {
		return nil, 0, err
	}

	list := make([]map[string]any, 0, min(results, len(page.Items)))
	for _, t := range page.Items[:min(results, len(page.Items))] {
		list = append(list, map[string]any{
			"outcome": t.Outcome, "tokenId": string(t.TokenID), "side": string(t.Side), "price": t.Price,
			"size": t.Size, "value": t.Price.Mul(t.Size), "wallet": string(t.Wallet), "at": t.At,
		})
	}

	return map[string]any{"marketId": in.MarketID, "trades": list}, len(list), nil
}

func positions(ctx context.Context, s Sources, input json.RawMessage, results int, _ time.Time) (any, int, error) {
	var in struct {
		Address string `json:"address"`
	}

	if err := decodeInput(input, &in); err != nil {
		return nil, 0, err
	}

	if s.Positions == nil {
		return nil, 0, errUnavailable
	}

	address, err := domain.ParseAddress(strings.TrimSpace(in.Address))
	if err != nil {
		return nil, 0, fmt.Errorf("address: %w", err)
	}

	page, err := s.Positions.Positions(ctx, ports.PositionFilter{Wallet: address, Limit: results})
	if err != nil {
		return nil, 0, err
	}

	held := slices.SortedFunc(slices.Values(page.Items), func(a, b domain.Position) int { return b.Value.Cmp(a.Value) })

	list := make([]map[string]any, 0, min(results, len(held)))
	for _, p := range held[:min(results, len(held))] {
		list = append(list, map[string]any{
			"marketId": string(p.MarketID), "tokenId": string(p.TokenID), "title": p.Title, "outcome": p.Outcome,
			"size": p.Size, "avgPrice": p.AvgPrice, "currentPrice": p.CurrentPrice, "value": p.Value,
			"realizedPnl": p.RealizedPnL, "unrealizedPnl": p.UnrealizedPnL,
		})
	}

	return map[string]any{"address": string(address), "positions": list}, len(list), nil
}
