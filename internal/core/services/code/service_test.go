package code_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

var at = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// compiler compiles anything to the same script.
type compiler struct{ err error }

func (c compiler) Compile(_ context.Context, source string) (ports.CompiledCode, error) {
	return ports.CompiledCode{Script: "compiled: " + source, Exports: "__exports", Invoke: "__invoke", Hash: "h"}, c.err
}

func (c compiler) CompileExpression(_ context.Context, expr string) (string, error) {
	return expr, c.err
}

func (c compiler) CompileWorkflow(_ context.Context, source string) (ports.CompiledCode, error) {
	return ports.CompiledCode{Script: "workflow: " + source, Exports: "__exports", Invoke: "__workflow", Hash: "h"}, c.err
}

// runner keeps the job it was given, and returns output, or "ok".
type runner struct {
	job    ports.CodeJob
	output string
}

func (r *runner) Run(_ context.Context, job ports.CodeJob) (ports.CodeResult, error) {
	r.job = job

	if r.output != "" {
		return ports.CodeResult{Output: json.RawMessage(r.output)}, nil
	}

	return ports.CodeResult{Output: json.RawMessage(`"ok"`)}, nil
}

type data struct {
	markets   *portsmocks.MockMarketReader
	events    *portsmocks.MockEventReader
	books     *portsmocks.MockOrderBookReader
	wallets   *portsmocks.MockWalletDirectory
	positions *portsmocks.MockPositionReader
}

// host runs some code for alice and returns how its data requests are answered.
func host(t *testing.T) (ports.CodeHost, data) {
	t.Helper()

	d := data{
		markets: portsmocks.NewMockMarketReader(t), events: portsmocks.NewMockEventReader(t),
		books: portsmocks.NewMockOrderBookReader(t), wallets: portsmocks.NewMockWalletDirectory(t),
		positions: portsmocks.NewMockPositionReader(t),
	}

	r := &runner{}
	s := code.New(compiler{}, r, code.Data{
		Markets: d.markets, Events: d.events, OrderBooks: d.books, Wallets: d.wallets, Positions: d.positions,
	})

	_, err := s.Call(context.Background(), ports.CodeCall{Owner: "alice", Source: "src", Export: "run"})
	require.NoError(t, err)

	return r.job.Host, d
}

func ask(t *testing.T, h ports.CodeHost, name, args string) (string, error) {
	t.Helper()

	answer, err := h(context.Background(), name, json.RawMessage(args))

	return string(answer), err
}

func TestRun(t *testing.T) {
	t.Parallel()

	r := &runner{}
	s := code.New(compiler{}, r, code.Data{})

	res, err := s.Call(context.Background(), ports.CodeCall{
		Owner: "alice", Source: "export const run = (n, ctx) => n", Export: "run",
		Args:      []any{[]map[string]any{{"price": decimal.RequireFromString("0.0200"), "at": at}}},
		Params:    map[string]any{"threshold": "0.02"},
		Execution: ports.CodeExecution{ID: "ex-1", WorkflowID: "wf-1", StartedAt: at},
		Limits:    ports.CodeLimits{HostCalls: 5},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `"ok"`, string(res.Output))

	assert.Equal(t, "compiled: export const run = (n, ctx) => n", r.job.Source)
	assert.Equal(t, "__invoke", r.job.Entry, "through the SDK")
	assert.Equal(t, ports.CodeLimits{HostCalls: 5}, r.job.Limits)
	assert.JSONEq(t, `{
		"exports": "__exports", "export": "run",
		"args": [[{"price": "0.02", "at": "2026-10-15T12:00:00Z"}]],
		"context": {
			"params": {"threshold": "0.02"},
			"execution": {"id": "ex-1", "workflowId": "wf-1", "startedAt": "2026-10-15T12:00:00Z"}
		}
	}`, string(r.job.Input), "decimals as text, times as RFC 3339")

	t.Run("nothing to pass is empty, not null", func(t *testing.T) {
		_, err := s.Call(context.Background(), ports.CodeCall{Owner: "alice", Source: "s", Export: "run"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"exports":"__exports","export":"run","args":[],"context":{"params":{},"execution":{"id":"","workflowId":"","startedAt":"0001-01-01T00:00:00Z"}}}`, string(r.job.Input))
	})

	t.Run("code that does not compile does not run", func(t *testing.T) {
		r := &runner{}
		compileErr := &domain.CodeError{Kind: domain.CodeDidNotCompile, Message: "line 1, column 1: nope"}

		_, err := code.New(compiler{err: compileErr}, r, code.Data{}).Call(context.Background(), ports.CodeCall{Source: "?", Export: "run"})
		require.ErrorIs(t, err, compileErr)
		assert.Empty(t, r.job.Source)
	})
}

func TestMarkets(t *testing.T) {
	t.Parallel()

	market := domain.Market{
		ID: "0xabc", Slug: "btc-100k", Question: "BTC above 100k?", Status: domain.MarketOpen,
		TickSize: decimal.RequireFromString("0.01"), EndsAt: at,
		Outcomes: []domain.Outcome{{TokenID: "t1", Label: "Yes", Price: decimal.RequireFromString("0.42")}},
	}

	t.Run("search: open markets of matching events", func(t *testing.T) {
		t.Parallel()

		h, d := host(t)
		closed := market
		closed.Status = domain.MarketClosed

		d.events.EXPECT().ListEvents(mock.Anything, mock.MatchedBy(func(f ports.EventFilter) bool {
			return f.Title == "bitcoin" && f.Limit == 2 && !*f.Closed
		})).Return(ports.Page[domain.Event]{Items: []domain.Event{
			{Title: "Bitcoin", Slug: "bitcoin", Markets: []domain.Market{closed, market, market, market}},
		}}, nil)

		answer, err := ask(t, h, "markets.search", `{"query":"bitcoin","limit":2}`)
		require.NoError(t, err)

		var found []map[string]any
		require.NoError(t, json.Unmarshal([]byte(answer), &found))
		require.Len(t, found, 2)
		assert.Equal(t, "Bitcoin", found[0]["event"])
		assert.Equal(t, "0.01", found[0]["tickSize"])
		assert.Equal(t, "2026-10-15T12:00:00Z", found[0]["endsAt"])
	})

	t.Run("search: bounds", func(t *testing.T) {
		t.Parallel()

		h, _ := host(t)

		_, err := ask(t, h, "markets.search", `{"query":"x","limit":51}`)
		require.EqualError(t, err, "markets.search: limit must be from 1 to 50, not 51")

		_, err = ask(t, h, "markets.search", `{"query":"  "}`)
		require.EqualError(t, err, "markets.search: give some words to search for")

		_, err = ask(t, h, "markets.search", `{"query":3}`)
		require.EqualError(t, err, "markets.search: these are not the arguments it takes")
	})

	t.Run("get: by ID or slug, null when there is none", func(t *testing.T) {
		t.Parallel()

		h, d := host(t)
		d.markets.EXPECT().GetMarket(mock.Anything, domain.MarketID("0xabc")).Return(market, nil)
		d.markets.EXPECT().GetMarketBySlug(mock.Anything, "btc-100k").Return(market, nil)
		d.markets.EXPECT().GetMarketBySlug(mock.Anything, "nope").Return(domain.Market{}, domain.ErrNotFound)

		for _, id := range []string{"0xabc", "btc-100k"} {
			answer, err := ask(t, h, "markets.get", `{"id":"`+id+`"}`)
			require.NoError(t, err)
			assert.Contains(t, answer, `"question":"BTC above 100k?"`)
		}

		answer, err := ask(t, h, "markets.get", `{"id":"nope"}`)
		require.NoError(t, err)
		assert.Equal(t, "null", answer)
	})
}

func TestOrderBooks(t *testing.T) {
	t.Parallel()

	level := func(price, size string) domain.Level {
		return domain.Level{Price: decimal.RequireFromString(price), Size: decimal.RequireFromString(size)}
	}

	h, d := host(t)
	d.books.EXPECT().OrderBooks(mock.Anything, []domain.TokenID{"t1", "t2", "t3"}).Return([]domain.OrderBook{
		{TokenID: "t3", At: at},
		{TokenID: "t1", Bids: []domain.Level{level("0.40", "10"), level("0.39", "5")}, Asks: []domain.Level{level("0.42", "7")}, At: at},
	}, nil)

	answer, err := ask(t, h, "orderBooks.get", `{"tokenIds":["t1","t2","t3"],"depth":1}`)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"tokenId":"t1","bestBid":"0.4","bestAsk":"0.42","mid":"0.41","spread":"0.02","bidDepth":"10","askDepth":"7",
		 "bids":[{"price":"0.4","size":"10"}],"asks":[{"price":"0.42","size":"7"}],"at":"2026-10-15T12:00:00Z"},
		null,
		{"tokenId":"t3","bestBid":null,"bestAsk":null,"mid":null,"spread":null,"bidDepth":"0","askDepth":"0",
		 "bids":[],"asks":[],"at":"2026-10-15T12:00:00Z"}
	]`, answer, "in the order asked, null where there is no book")

	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = "t"
	}

	raw, _ := json.Marshal(map[string]any{"tokenIds": tooMany})
	_, err = ask(t, h, "orderBooks.get", string(raw))
	require.EqualError(t, err, "orderBooks.get: give from 1 to 50 token IDs, not 51")

	_, err = ask(t, h, "orderBooks.get", `{"tokenIds":["t1"],"depth":0}`)
	require.EqualError(t, err, "orderBooks.get: depth must be from 1 to 50, not 0")
}

func TestPositions(t *testing.T) {
	t.Parallel()

	h, d := host(t)
	wallet := domain.Wallet{ID: "w-1", UserID: "alice", Address: "0xwallet"}

	d.wallets.EXPECT().Wallet(mock.Anything, domain.UserID("alice"), domain.WalletID("w-1")).Return(wallet, nil)
	d.wallets.EXPECT().Wallet(mock.Anything, domain.UserID("alice"), domain.WalletID("bobs")).Return(domain.Wallet{}, domain.ErrNotFound)
	d.positions.EXPECT().Positions(mock.Anything, ports.PositionFilter{Wallet: "0xwallet", Limit: 50}).Return(ports.Page[domain.Position]{
		Items: []domain.Position{{TokenID: "t1", Title: "BTC?", Size: decimal.RequireFromString("12.5")}},
	}, nil)

	answer, err := ask(t, h, "positions.list", `{"walletId":"w-1"}`)
	require.NoError(t, err)
	assert.Contains(t, answer, `"walletId":"w-1"`)
	assert.Contains(t, answer, `"size":"12.5"`)

	_, err = ask(t, h, "positions.list", `{"walletId":"bobs"}`)
	require.EqualError(t, err, `positions.list: you have no wallet "bobs"`, "the owner's wallets only")

	_, err = ask(t, h, "positions.list", `{"walletId":"w-1","limit":501}`)
	require.EqualError(t, err, "positions.list: limit must be from 1 to 500, not 501")
}

func TestUnknownRequest(t *testing.T) {
	t.Parallel()

	h, _ := host(t)

	_, err := ask(t, h, "wallets.drain", `{}`)
	require.EqualError(t, err, `there is no data request "wallets.drain"`)
}
