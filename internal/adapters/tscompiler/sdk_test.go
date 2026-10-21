package tscompiler_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/codenodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/customnodes"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	sdkOnce   sync.Once
	sdkRunner *jsruntime.Runner
)

// withSDKRunner is a runner with the SDK as its prelude, as the server's.
func withSDKRunner(t *testing.T) *jsruntime.Runner {
	t.Helper()

	sdkOnce.Do(func() {
		sdk, err := tscompiler.SDK()
		require.NoError(t, err)

		sdkRunner, err = jsruntime.New(context.Background(), observability.NoopUtility(), jsruntime.Config{Prelude: sdk})
		require.NoError(t, err)
	})

	return sdkRunner
}

// withSDK runs source's export run through the code service, with the SDK and data.
func withSDK(t *testing.T, source string, args []any, data code.Data, limits ports.CodeLimits) (ports.CodeResult, error) {
	t.Helper()

	return code.New(compiler(t), withSDKRunner(t), data).Call(context.Background(), ports.CodeCall{
		Owner: "alice", Source: source, Export: "run", Args: args,
		Params:    map[string]any{"threshold": "0.02"},
		Execution: ports.CodeExecution{ID: "ex-1", WorkflowID: "wf-1", StartedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)},
		Limits:    limits,
	})
}

func TestSDKDecimals(t *testing.T) {
	t.Parallel()

	res, err := withSDK(t, `
		import { dec } from "@polythought/sdk"
		export function run() {
			return {
				sum: dec("0.1").plus("0.2"),
				float: 0.1 + 0.2,
				third: dec(1).div(3),
				tiny: dec("0.00000001").times(1),
				huge: dec("123456789012345678901234567890").times(10),
				rounded: [dec("2.345").round(2), dec("2.345").round(2, 0)],
				fixed: dec("1.5").toFixed(3),
				compared: [dec("0.02").lt("0.03"), dec("0.1").eq(0.1), dec("1").cmp("2")],
			}
		}`, nil, code.Data{}, ports.CodeLimits{})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"sum": "0.3", "float": 0.30000000000000004,
		"third": "0.33333333333333333333",
		"tiny": "0.00000001",
		"huge": "1234567890123456789012345678900",
		"rounded": ["2.35", "2.34"],
		"fixed": "1.500",
		"compared": [true, true, -1]
	}`, string(res.Output), "exact, and text in JSON")

	_, err = withSDK(t, `
		import { dec } from "@polythought/sdk"
		export const run = () => dec("lots")`, nil, code.Data{}, ports.CodeLimits{})

	var ce *domain.CodeError
	require.ErrorAs(t, err, &ce)
	assert.Contains(t, ce.Message, "Invalid number")
}

func TestSDKContext(t *testing.T) {
	t.Parallel()

	res, err := withSDK(t, `
		import type { Context, Item } from "@polythought/sdk"
		export function run(items: Item[], ctx: Context<{ threshold: string }>) {
			ctx.log("got", items.length, "items")
			return {
				params: ctx.params,
				execution: ctx.execution,
				frozen: Object.isFrozen(ctx) && Object.isFrozen(ctx.params),
				first: items[0],
			}
		}`, []any{[]map[string]any{{"spread": decimal.RequireFromString("0.01")}, {}}}, code.Data{}, ports.CodeLimits{})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"params": {"threshold": "0.02"},
		"execution": {"id": "ex-1", "workflowId": "wf-1", "startedAt": "2026-10-15T12:00:00Z"},
		"frozen": true,
		"first": {"spread": "0.01"}
	}`, string(res.Output))
	assert.Equal(t, []ports.CodeLog{{Level: "log", Text: "got 2 items"}}, res.Logs)

	_, err = withSDK(t, `export const go = () => 1`, nil, code.Data{}, ports.CodeLimits{})
	require.EqualError(t, err, "Error: the code exports no function named run")
}

func TestSDKRequests(t *testing.T) {
	t.Parallel()

	events := portsmocks.NewMockEventReader(t)
	wallets := portsmocks.NewMockWalletDirectory(t)

	events.EXPECT().ListEvents(mock.Anything, mock.Anything).Return(ports.Page[domain.Event]{Items: []domain.Event{{
		Title: "Bitcoin", Markets: []domain.Market{{ID: "0xabc", Question: "BTC above 100k?", Status: domain.MarketOpen}},
	}}}, nil)
	wallets.EXPECT().Wallet(mock.Anything, domain.UserID("alice"), domain.WalletID("bobs")).Return(domain.Wallet{}, domain.ErrNotFound)

	data := code.Data{Events: events, Wallets: wallets}

	res, err := withSDK(t, `
		import { markets, positions } from "@polythought/sdk"
		export async function run() {
			const found = await markets.search("bitcoin", { limit: 1 })
			let refused = ""
			try { await positions.list("bobs") } catch (e) { refused = (e as Error).message }
			return { found: found.map((m) => m.question), refused, host: typeof (globalThis as any).__host }
		}`, nil, data, ports.CodeLimits{})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"found": ["BTC above 100k?"],
		"refused": "positions.list: you have no wallet \"bobs\"",
		"host": "undefined"
	}`, string(res.Output), "only the SDK reaches out")
	assert.Equal(t, 2, res.HostCalls)

	_, err = withSDK(t, `
		import { markets } from "@polythought/sdk"
		export async function run() {
			for (let i = 0; i < 3; i++) await markets.search("bitcoin").catch(() => null)
		}`, nil, data, ports.CodeLimits{HostCalls: 2})

	var ce *domain.CodeError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, domain.CodeTooManyCalls, ce.Kind, "each request counts")
}

// TestSDKContract checks that every data request answers with the fields sdk.d.ts
// declares, no more and no fewer.
func TestSDKContract(t *testing.T) {
	t.Parallel()

	ms := portsmocks.NewMockMarketReader(t)
	events := portsmocks.NewMockEventReader(t)
	books := portsmocks.NewMockOrderBookReader(t)
	wallets := portsmocks.NewMockWalletDirectory(t)
	positions := portsmocks.NewMockPositionReader(t)

	market := domain.Market{ID: "0xabc", Status: domain.MarketOpen, Outcomes: []domain.Outcome{{TokenID: "t1"}}}
	one := decimal.NewFromInt(1)

	ms.EXPECT().GetMarket(mock.Anything, mock.Anything).Return(market, nil)
	events.EXPECT().ListEvents(mock.Anything, mock.Anything).Return(ports.Page[domain.Event]{Items: []domain.Event{{Markets: []domain.Market{market}}}}, nil)
	books.EXPECT().OrderBooks(mock.Anything, mock.Anything).Return([]domain.OrderBook{{
		TokenID: "t1", Bids: []domain.Level{{Price: one, Size: one}}, Asks: []domain.Level{{Price: one, Size: one}},
	}}, nil)
	wallets.EXPECT().Wallet(mock.Anything, mock.Anything, mock.Anything).Return(domain.Wallet{ID: "w-1"}, nil)
	positions.EXPECT().Positions(mock.Anything, mock.Anything).Return(ports.Page[domain.Position]{Items: []domain.Position{{}}}, nil)

	res, err := withSDK(t, `
		import { markets, orderBooks, positions } from "@polythought/sdk"
		export async function run() {
			const market = (await markets.get("0xabc"))!
			const book = (await orderBooks.get(["t1"]))[0]!
			return {
				Market: Object.keys(market),
				Outcome: Object.keys(market.outcomes[0]),
				FoundMarket: Object.keys((await markets.search("x"))[0]),
				OrderBook: Object.keys(book),
				Level: Object.keys(book.bids[0]),
				Position: Object.keys((await positions.list("w-1"))[0]),
			}
		}`, nil, code.Data{Markets: ms, Events: events, OrderBooks: books, Wallets: wallets, Positions: positions}, ports.CodeLimits{})
	require.NoError(t, err)

	var answered map[string][]string
	require.NoError(t, json.Unmarshal(res.Output, &answered))

	declared := declaredFields(t)

	for name, fields := range answered {
		slices.Sort(fields)
		assert.Equal(t, declared[name], fields, "interface %s", name)
	}
}

var (
	interfaceStart = regexp.MustCompile(`^export interface (\w+)(?:<.*>)?(?: extends (\w+))? \{$`)
	field          = regexp.MustCompile(`^    (?:readonly )?(\w+)\??: `)
)

// declaredFields reads the fields of each interface in sdk.d.ts, with the ones it
// extends.
func declaredFields(t *testing.T) map[string][]string {
	t.Helper()

	f, err := os.Open("sdk/types/sdk.d.ts")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	fields := map[string][]string{}
	extends := map[string]string{}
	current := ""

	for sc := bufio.NewScanner(f); sc.Scan(); {
		line := sc.Text()

		switch m := interfaceStart.FindStringSubmatch(line); {
		case m != nil:
			current = m[1]
			extends[current] = m[2]
		case line == "}":
			current = ""
		case current != "":
			if m := field.FindStringSubmatch(line); m != nil {
				fields[current] = append(fields[current], m[1])
			}
		}
	}

	for name, base := range extends {
		if base != "" {
			fields[name] = append(fields[name], fields[base]...)
		}
	}

	for _, fs := range fields {
		slices.Sort(fs)
	}

	require.NotEmpty(t, fields["Market"], "the declarations are there")

	return fields
}

func TestSDKBuild(t *testing.T) {
	t.Parallel()

	sdk, err := tscompiler.SDK()
	require.NoError(t, err)
	assert.Less(t, len(sdk), 32<<10, "small: every run parses it")
	assert.True(t, strings.HasPrefix(sdk, "var "+tscompiler.SDKGlobal+"="), sdk[:min(len(sdk), 80)])
}

func TestExpressionsEndToEnd(t *testing.T) {
	t.Parallel()

	_, err := withSDK(t, `export const run = () => null`, nil, code.Data{}, ports.CodeLimits{}) // starts the runner
	require.NoError(t, err)

	svc := code.New(compiler(t), sdkRunner, code.Data{})

	exprs := []string{
		"spread * 100",
		"dec(bestAsk).minus(bestBid)",
		"item.question.toUpperCase()",
		"params.least",
		"execution.id",
		"params",      // the node's settings, though the item has a field so named
		"item.params", // which this reaches
		"`${question}!`",
		"missing + 1",
		"(x as number) +",
		"new Date(0)",
		"outcomes[1].label",
	}

	results, err := svc.Evaluate(context.Background(), engine.Evaluation{
		Expressions: exprs,
		Items: []map[string]any{
			{
				"question": "BTC?", "spread": "0.012", "bestBid": "0.43", "bestAsk": "0.45", "params": "a field",
				"outcomes": []any{map[string]any{"label": "Yes"}, map[string]any{"label": "No"}},
			},
			{"question": "ETH?", "spread": 0.5, "bestBid": "0.1", "bestAsk": "0.3", "outcomes": []any{}},
		},
		Params:    map[string]any{"least": "0.02"},
		Execution: map[string]any{"id": "ex-1"},
	})
	require.NoError(t, err)
	require.Len(t, results, 2)

	first := results[0]
	want := []any{
		json.Number("1.2"), "0.02", "BTC?", "0.02", "ex-1",
		map[string]any{"least": "0.02"}, "a field", "BTC?!", nil, nil, "1970-01-01T00:00:00.000Z", "No",
	}

	for j, w := range want {
		if w == nil {
			continue
		}

		assert.Empty(t, first[j].Err, exprs[j])
		assert.Equal(t, w, first[j].Value, exprs[j])
	}

	assert.Contains(t, first[8].Err, "ReferenceError", "a field the item lacks, in the sandbox")
	assert.Contains(t, first[9].Err, "line 1, column 16: Unexpected end of the expression")
	assert.Contains(t, results[1][9].Err, "Unexpected end", "for every item")

	assert.Equal(t, json.Number("50"), results[1][0].Value)
	assert.Equal(t, "0.2", results[1][1].Value)
	assert.Contains(t, results[1][11].Err, "TypeError", "each item on its own")
}

// A node that sees them has items, every item on main; others keep the name for a field.
func TestExpressionsSeeItems(t *testing.T) {
	t.Parallel()

	_, err := withSDK(t, `export const run = () => null`, nil, code.Data{}, ports.CodeLimits{}) // starts the runner
	require.NoError(t, err)

	svc := code.New(compiler(t), sdkRunner, code.Data{})

	items := []map[string]any{{"question": "BTC?", "items": "a field"}, {"question": "ETH?"}}
	exprs := []string{"items.map((i) => i.question).join(', ')", "item.items", "items.length"}

	results, err := svc.Evaluate(context.Background(), engine.Evaluation{Expressions: exprs, Items: items, All: items})
	require.NoError(t, err)

	for i := range items {
		assert.Equal(t, "BTC?, ETH?", results[i][0].Value, "every item, from each")
		assert.Equal(t, json.Number("2"), results[i][2].Value)
	}

	assert.Equal(t, "a field", results[0][1].Value, "the field, as item.items")

	results, err = svc.Evaluate(context.Background(), engine.Evaluation{Expressions: exprs, Items: items})
	require.NoError(t, err)
	assert.Contains(t, results[0][0].Err, "TypeError", "the field, a string, has no map")
	assert.Contains(t, results[1][0].Err, "ReferenceError", "not a name for a node that does not see them")
}

func TestCodeNodesEndToEnd(t *testing.T) {
	t.Parallel()

	_, err := withSDK(t, `export const run = () => null`, nil, code.Data{}, ports.CodeLimits{}) // starts the runner
	require.NoError(t, err)

	events := portsmocks.NewMockEventReader(t)
	events.EXPECT().ListEvents(mock.Anything, mock.Anything).Return(ports.Page[domain.Event]{Items: []domain.Event{{
		Markets: []domain.Market{{ID: "0x1", Question: "Found?", Status: domain.MarketOpen}},
	}}}, nil).Maybe()

	svc := code.New(compiler(t), sdkRunner, code.Data{Events: events})

	runNode := func(t *testing.T, node *codenodes.Node, params map[string]any, items ...map[string]any) engine.Ports {
		t.Helper()

		in := engine.Ports{}
		for _, it := range items {
			in.Add(engine.DefaultPort, engine.Item{JSON: it})
		}

		out, err := node.Run(context.Background(), engine.RunContext{
			ExecutionID: "ex-1", WorkflowID: "wf-1", OwnerID: "alice", StartedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
			Params: engine.LiteralParams(node.Type(), params),
		}, in)
		require.NoError(t, err)

		return out
	}

	t.Run("the starters run", func(t *testing.T) {
		out := runNode(t, codenodes.NewTransform(svc, logging.Noop()), nil, map[string]any{"q": "A"})
		assert.Equal(t, "2026-10-16T12:00:00Z", out[engine.DefaultPort][0].JSON["seenAt"])

		out = runNode(t, codenodes.NewFilter(svc, logging.Noop()), nil, map[string]any{"question": "A"}, map[string]any{})
		assert.Len(t, out[engine.DefaultPort], 1)

		out = runNode(t, codenodes.NewRoute(svc, logging.Noop()), nil, map[string]any{"spread": "0.05"}, map[string]any{"spread": "0.01"})
		assert.Len(t, out["wide"], 1)
		assert.Len(t, out["tight"], 1)

		out = runNode(t, codenodes.NewAggregate(svc, logging.Noop()), nil, map[string]any{}, map[string]any{}, map[string]any{})
		assert.Equal(t, json.Number("3"), out[engine.DefaultPort][0].JSON["count"])
	})

	t.Run("one item throwing fails only itself, and code may wait for data", func(t *testing.T) {
		out := runNode(t, codenodes.NewFilter(svc, logging.Noop()), map[string]any{"source": `
			import { markets } from "@polythought/sdk"
			export async function keep(item: { n: number }) {
				if (item.n === 2) throw new RangeError("two")
				const found = await markets.search("x")
				return found.length === 1 && item.n > 1
			}`}, map[string]any{"n": 1}, map[string]any{"n": 2}, map[string]any{"n": 3})

		assert.Equal(t, 3, out[engine.DefaultPort][0].JSON["n"], "items pass on as they came")
		require.Len(t, out[engine.ErrorPort], 1)
		assert.EqualError(t, out[engine.ErrorPort][0].Err, "RangeError: two")
	})
}

// A custom node's run gets every item and its settings, and returns the items by port.
func TestCustomNodeEndToEnd(t *testing.T) {
	t.Parallel()

	_, err := withSDK(t, `export const run = () => null`, nil, code.Data{}, ports.CodeLimits{}) // starts the runner
	require.NoError(t, err)

	svc := code.New(compiler(t), sdkRunner, code.Data{})

	runNode := func(t *testing.T, def domain.CustomNode, params map[string]any, items ...map[string]any) engine.Ports {
		t.Helper()

		node := customnodes.New(def, svc, logging.Noop())
		require.NoError(t, engine.NewRegistry().RegisterNode(node))

		in := engine.Ports{}
		for _, it := range items {
			in.Add(engine.DefaultPort, engine.Item{JSON: it})
		}

		out, err := node.Run(context.Background(), engine.RunContext{
			ExecutionID: "ex-1", WorkflowID: "wf-1", OwnerID: "alice", StartedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
			Params: engine.LiteralParams(node.Type(), params),
		}, in)
		require.NoError(t, err)

		return out
	}

	split := domain.CustomNode{
		Name: "spread-split", Label: "Spread split", Outputs: []string{"wide", "tight"},
		Settings: []domain.CustomSetting{{Name: "threshold", Kind: "decimal", Default: "0.02"}},
		Items:    domain.CustomItems{Passed: true},
		Source: `
			import { dec } from "@polythought/sdk"
			import type { Context } from "@polythought/sdk"

			type Book = { spread: string }

			export function run(items: Book[], ctx: Context<{ threshold: string }>) {
				const wide = items.filter((i) => dec(i.spread).gt(ctx.params.threshold))
				return { wide, tight: items.filter((i) => !wide.includes(i)) }
			}`,
	}

	t.Run("by port, with its settings", func(t *testing.T) {
		out := runNode(t, split, map[string]any{"threshold": "0.03"},
			map[string]any{"spread": "0.05"}, map[string]any{"spread": "0.01"}, map[string]any{"spread": "0.04"})

		assert.Len(t, out["wide"], 2)
		assert.Equal(t, "0.01", out["tight"][0].JSON["spread"])
		assert.Empty(t, out[engine.ErrorPort])
	})

	t.Run("items not matching its fields fail every item it got", func(t *testing.T) {
		summary := domain.CustomNode{
			Name: "summary", Label: "Summary", Outputs: []string{"main"},
			Items:  domain.CustomItems{Fields: []domain.CustomField{{Name: "count", Kind: "number"}}},
			Source: `export function run(items: unknown[]) { return [{ count: items.length }, { total: 1 }] }`,
		}

		out := runNode(t, summary, nil, map[string]any{"q": "A"}, map[string]any{"q": "B"})

		assert.Empty(t, out[engine.DefaultPort])
		require.Len(t, out[engine.ErrorPort], 2)
		assert.EqualError(t, out[engine.ErrorPort][0].Err, "what run returned item 1 does not match the node's fields: field count is missing")
	})
}
