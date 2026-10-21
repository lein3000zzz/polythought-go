package ai_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/ai"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var d = decimal.RequireFromString

// asks is the model asking for tools.
func asks(calls ...ports.LLMToolCall) ports.LLMResponse {
	r := said("")
	r.StopReason = "tool_use"
	r.Turn = ports.LLMMessage{Role: ports.LLMAssistant, ToolCalls: calls, Native: "the provider's own"}

	return r
}

func call(id, name, input string) ports.LLMToolCall {
	return ports.LLMToolCall{ID: id, Name: name, Input: json.RawMessage(input)}
}

func bitcoin(t *testing.T) ai.Sources {
	events := portsmocks.NewMockEventReader(t)
	events.EXPECT().ListEvents(mock.Anything, mock.MatchedBy(func(f ports.EventFilter) bool {
		return f.Title == "bitcoin" && f.Limit == 20 && !*f.Closed
	})).Return(ports.Page[domain.Event]{Items: []domain.Event{{Title: "Bitcoin", Markets: []domain.Market{{
		ID: "0xabc", Question: "BTC above 100k?", Status: domain.MarketOpen,
		Outcomes: []domain.Outcome{{TokenID: "t-yes", Label: "Yes", Price: d("0.42")}},
	}}}}}, nil).Maybe()

	return ai.Sources{Events: events}
}

func TestToolLoop(t *testing.T) {
	llm := &fakeLLM{answers: []any{
		asks(call("c1", "search_markets", `{"query":"bitcoin","closed":false}`)),
		said("Watch BTC above 100k."),
	}}

	out := runWith(t, llm, bitcoin(t), map[string]any{"tools": []any{ai.ToolSearch, ai.ToolBook}}, map[string]any{})

	answer := out[engine.DefaultPort][0].JSON["answer"].(map[string]any)
	assert.Equal(t, "Watch BTC above 100k.", answer["text"])
	assert.Equal(t, 1, answer["toolCalls"])
	assert.Equal(t, 1, answer["tries"], "tool rounds are no tries")
	assert.Equal(t, map[string]any{"input": int64(20), "output": int64(10)}, answer["usage"], "every call counted")

	entry := answer["tools"].([]any)[0].(map[string]any)
	assert.Equal(t, "search_markets", entry["name"])
	assert.Equal(t, map[string]any{"query": "bitcoin", "closed": false}, entry["input"])
	assert.Equal(t, 1, entry["results"])
	assert.NotNil(t, entry["output"], "in full, by default (D264)")

	first := llm.asked[0]
	require.Len(t, first.Tools, 2, "only the tools picked")
	assert.Equal(t, "search_markets", first.Tools[0].Name)
	assert.Equal(t, "order_book", first.Tools[1].Name)

	second := llm.asked[1].Messages
	require.Len(t, second, 3)
	assert.Equal(t, "the provider's own", second[1].Native, "the turn back as it came")

	result := second[2].ToolResults[0]
	assert.Equal(t, "c1", result.CallID)
	assert.False(t, result.IsError)
	assert.Contains(t, result.Content, `"question":"BTC above 100k?"`)
	assert.Contains(t, result.Content, `"price":"0.42"`, "decimals exact, as text")
}

func TestToolResultsShown(t *testing.T) {
	llm := &fakeLLM{answers: []any{asks(call("c1", "search_markets", `{"query":"bitcoin","closed":false}`)), said("ok")}}

	out := runWith(t, llm, bitcoin(t), map[string]any{"tools": []any{ai.ToolSearch}, "toolResults": ai.ResultsInputs}, map[string]any{})

	entry := out[engine.DefaultPort][0].JSON["answer"].(map[string]any)["tools"].([]any)[0].(map[string]any)
	assert.NotContains(t, entry, "output", "inputs only")
	assert.Equal(t, 1, entry["results"])
}

func TestToolLimit(t *testing.T) {
	search := call("c1", "search_markets", `{"query":"bitcoin","closed":false}`)

	t.Run("past it, told to answer with what it has", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{asks(search), asks(call("c2", "search_markets", `{"query":"eth","closed":false}`)), said("Done.")}}

		out := runWith(t, llm, bitcoin(t), map[string]any{"tools": []any{ai.ToolSearch}, "toolCalls": 1}, map[string]any{})

		answer := out[engine.DefaultPort][0].JSON["answer"].(map[string]any)
		assert.Equal(t, "Done.", answer["text"])
		assert.Equal(t, 1, answer["toolCalls"], "the one past it not made")

		told := llm.asked[2].Messages[4].ToolResults[0]
		assert.True(t, told.IsError)
		assert.Contains(t, told.Content, "the limit of 1 is reached. Answer now with what you have.")
	})

	t.Run("asking on after that fails the item", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{asks(search), asks(search), asks(search)}}

		out := runWith(t, llm, bitcoin(t), map[string]any{"tools": []any{ai.ToolSearch}, "toolCalls": 1}, map[string]any{})
		require.Len(t, out[engine.ErrorPort], 1)
		assert.ErrorContains(t, out[engine.ErrorPort][0].Err, "kept asking for tools past the limit of 1 calls")
		assert.Len(t, llm.asked, 3)
	})
}

func TestToolFailures(t *testing.T) {
	llm := &fakeLLM{answers: []any{
		asks(
			call("c1", "search_markets", `{"query":"bitcoin","extra":1}`),
			call("c2", "order_book", `{"tokenId":"t-1"}`),
			call("c3", "delete_everything", `{}`),
		),
		said("I could not look."),
	}}

	out := runWith(t, llm, ai.Sources{}, map[string]any{"tools": []any{ai.ToolSearch, ai.ToolBook}}, map[string]any{})

	answer := out[engine.DefaultPort][0].JSON["answer"].(map[string]any)
	assert.Equal(t, 3, answer["toolCalls"])

	results := llm.asked[1].Messages[2].ToolResults
	require.Len(t, results, 3, "every call answered")

	for _, r := range results {
		assert.True(t, r.IsError, r.Name)
	}

	assert.Contains(t, results[0].Content, `unknown field "extra"`, "the input checked, as the provider does not")
	assert.Contains(t, results[1].Content, "not available on this instance")
	assert.Contains(t, results[2].Content, "no tool is named delete_everything", "only those picked")

	trace := answer["tools"].([]any)
	assert.Contains(t, trace[0].(map[string]any)["error"], "unknown field")
}

func TestToolSettings(t *testing.T) {
	for name, params := range map[string]map[string]any{
		"an unknown tool":  {"tools": []any{"shell"}},
		"no calls":         {"tools": []any{ai.ToolSearch}, "toolCalls": 0},
		"too many calls":   {"tools": []any{ai.ToolSearch}, "toolCalls": 26},
		"no results":       {"tools": []any{ai.ToolSearch}, "results": 0},
		"too many results": {"tools": []any{ai.ToolSearch}, "results": 51},
	} {
		t.Run(name+" fails the item, unasked", func(t *testing.T) {
			llm := &fakeLLM{}
			out := run(t, llm, params, map[string]any{})
			require.Len(t, out[engine.ErrorPort], 1)
			assert.Empty(t, llm.asked)
		})
	}

	t.Run("none picked, none offered", func(t *testing.T) {
		llm := &fakeLLM{answers: []any{said("ok")}}
		run(t, llm, map[string]any{"toolCalls": 0}, map[string]any{})
		assert.Empty(t, llm.asked[0].Tools)
	})
}

// eachTool runs one tool through the node, its results as the model reads them.
func eachTool(t *testing.T, sources ai.Sources, tool string, c ports.LLMToolCall, params map[string]any) (map[string]any, ports.LLMToolResult) {
	t.Helper()

	llm := &fakeLLM{answers: []any{asks(c), said("ok")}}

	base := map[string]any{"tools": []any{tool}}
	for k, v := range params {
		base[k] = v
	}

	out := runWith(t, llm, sources, base, map[string]any{})
	require.Len(t, out[engine.DefaultPort], 1)

	result := llm.asked[1].Messages[2].ToolResults[0]

	var got map[string]any
	if !result.IsError {
		require.NoError(t, json.Unmarshal([]byte(result.Content), &got))
	}

	return got, result
}

func TestOrderBookTool(t *testing.T) {
	books := portsmocks.NewMockOrderBookReader(t)
	books.EXPECT().OrderBooks(mock.Anything, []domain.TokenID{"t-yes"}).Return([]domain.OrderBook{{
		Bids: []domain.Level{{Price: d("0.41"), Size: d("100")}, {Price: d("0.40"), Size: d("50")}, {Price: d("0.39"), Size: d("9")}},
		Asks: []domain.Level{{Price: d("0.43"), Size: d("80")}},
	}}, nil)

	got, _ := eachTool(t, ai.Sources{Books: books}, ai.ToolBook, call("c", "order_book", `{"tokenId":"t-yes"}`), map[string]any{"results": 2})

	assert.Equal(t, "0.41", got["bestBid"])
	assert.Equal(t, "0.43", got["bestAsk"])
	assert.Equal(t, "0.02", got["spread"])
	assert.Len(t, got["bids"], 2, "the top levels, as many as results")
	assert.Len(t, got["asks"], 1)
}

func TestPriceHistoryTool(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	var points []domain.PricePoint
	for i := range 10 {
		points = append(points, domain.PricePoint{At: now.Add(time.Duration(i-10) * time.Hour), Price: d("0.40").Add(decimal.New(int64(i), -2))})
	}

	history := portsmocks.NewMockPriceHistoryReader(t)
	history.EXPECT().PriceHistory(mock.Anything, mock.MatchedBy(func(f ports.PriceHistoryFilter) bool {
		return f.TokenID == "t-yes" && f.Since.Equal(now.Add(-24*time.Hour)) && f.Bucket == 24*time.Hour/5
	})).Return(ports.Page[domain.PricePoint]{Items: points}, nil)

	got, _ := eachToolAt(t, now, ai.Sources{History: history}, ai.ToolHistory,
		call("c", "price_history", `{"tokenId":"t-yes","window":"1d"}`), map[string]any{"results": 5})

	list := got["points"].([]any)
	require.Len(t, list, 5, "spread evenly over the window")
	assert.Equal(t, "0.4", list[0].(map[string]any)["price"], "the first")
	assert.Equal(t, "0.49", list[4].(map[string]any)["price"], "and the last")
	assert.Equal(t, "0.09", got["change"])
	assert.Equal(t, "22.5", got["changePct"])

	_, bad := eachTool(t, ai.Sources{History: history}, ai.ToolHistory, call("c", "price_history", `{"tokenId":"t-yes","window":"2y"}`), nil)
	assert.True(t, bad.IsError)
	assert.Contains(t, bad.Content, `window: "2y" is not 1h, 6h, 1d, 1w or 1m`)
}

func TestTradesAndPositionsTools(t *testing.T) {
	trades := portsmocks.NewMockTradeReader(t)
	trades.EXPECT().Trades(mock.Anything, ports.TradeFilter{MarketIDs: []domain.MarketID{"0xabc"}, Limit: 20}).
		Return(ports.Page[domain.Trade]{Items: []domain.Trade{{
			MarketID: "0xabc", TokenID: "t-yes", Outcome: "Yes", Side: domain.SideBuy, Price: d("0.5"), Size: d("10"), Wallet: "0x1111111111111111111111111111111111111111",
		}}}, nil)

	got, _ := eachTool(t, ai.Sources{Trades: trades}, ai.ToolTrades, call("c", "recent_trades", `{"marketId":"0xabc"}`), nil)
	list := got["trades"].([]any)
	require.Len(t, list, 1)
	assert.Equal(t, "5", list[0].(map[string]any)["value"])

	address := "0x2222222222222222222222222222222222222222"
	positions := portsmocks.NewMockPositionReader(t)
	positions.EXPECT().Positions(mock.Anything, ports.PositionFilter{Wallet: domain.WalletAddress(address), Limit: 20}).
		Return(ports.Page[domain.Position]{Items: []domain.Position{
			{Title: "Small", Value: d("3")}, {Title: "Big", Value: d("300")},
		}}, nil)

	got, _ = eachTool(t, ai.Sources{Positions: positions}, ai.ToolPositions, call("c", "positions", `{"address":"`+address+`"}`), nil)
	held := got["positions"].([]any)
	require.Len(t, held, 2)
	assert.Equal(t, "Big", held[0].(map[string]any)["title"], "largest first")

	_, bad := eachTool(t, ai.Sources{Positions: positions}, ai.ToolPositions, call("c", "positions", `{"address":"alice"}`), nil)
	assert.True(t, bad.IsError)
	assert.True(t, strings.HasPrefix(bad.Content, "address: "), bad.Content)
}

// eachToolAt is eachTool with a clock, for the price history.
func eachToolAt(t *testing.T, now time.Time, sources ai.Sources, tool string, c ports.LLMToolCall, params map[string]any) (map[string]any, ports.LLMToolResult) {
	t.Helper()

	clock = func() time.Time { return now }
	defer func() { clock = nil }()

	return eachTool(t, sources, tool, c, params)
}
