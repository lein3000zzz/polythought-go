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
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var (
	sdkOnce   sync.Once
	sdkRunner *jsruntime.Runner
)

// withSDK runs source's export run through the code service, with the SDK and data.
func withSDK(t *testing.T, source string, args []any, data code.Data, limits ports.CodeLimits) (ports.CodeResult, error) {
	t.Helper()

	sdkOnce.Do(func() {
		sdk, err := tscompiler.SDK()
		require.NoError(t, err)

		sdkRunner, err = jsruntime.New(context.Background(), observability.NoopUtility(), jsruntime.Config{Prelude: sdk})
		require.NoError(t, err)
	})

	return code.New(compiler(t), sdkRunner, data).Run(context.Background(), code.Call{
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
