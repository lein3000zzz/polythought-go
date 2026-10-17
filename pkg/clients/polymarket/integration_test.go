//go:build integration

// Live checks against the public Polymarket APIs. They catch upstream changes the
// fixtures cannot. Run with `make test-integration`.

package polymarket

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const liveTimeout = 30 * time.Second

type liveEnv struct {
	gamma  *DefaultGammaClient
	clob   *DefaultClobClient
	data   *DefaultDataClient
	market Market // an open order-book market with two outcomes
}

func newLiveEnv(t *testing.T, ctx context.Context) liveEnv {
	t.Helper()

	util := observability.NoopUtility()

	gamma, err := NewGammaClient(util, HTTPConfig{})
	require.NoError(t, err)

	clob, err := NewClobClient(util, HTTPConfig{})
	require.NoError(t, err)

	data, err := NewDataClient(util, HTTPConfig{})
	require.NoError(t, err)

	closed := false
	page, err := gamma.ListMarkets(ctx, MarketsQuery{Limit: 50, Closed: &closed})
	require.NoError(t, err)

	for _, m := range page.Items {
		if m.EnableOrderBook && m.AcceptingOrders && len(m.TokenIDs()) == 2 {
			return liveEnv{gamma: gamma, clob: clob, data: data, market: m}
		}
	}

	require.FailNow(t, "no open order-book market on the first page")

	return liveEnv{}
}

func liveContext(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	t.Cleanup(cancel)

	return ctx
}

func TestLiveGamma(t *testing.T) {
	ctx := liveContext(t)
	env := newLiveEnv(t, ctx)
	m := env.market

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"market by slug", func(t *testing.T) {
			got, err := env.gamma.GetMarketBySlug(ctx, m.Slug)
			require.NoError(t, err)
			assert.Equal(t, m.ConditionID, got.ConditionID)
		}},
		{"market by id", func(t *testing.T) {
			got, err := env.gamma.GetMarket(ctx, m.ID)
			require.NoError(t, err)
			assert.Equal(t, m.ConditionID, got.ConditionID)
		}},
		{"unknown slug is not found", func(t *testing.T) {
			_, err := env.gamma.GetMarketBySlug(ctx, "polythought-no-such-market")
			assert.ErrorIs(t, err, ErrNotFound)
		}},
		{"markets filtered by condition", func(t *testing.T) {
			page, err := env.gamma.ListMarkets(ctx, MarketsQuery{ConditionIDs: []string{m.ConditionID}})
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			assert.Equal(t, m.ID, page.Items[0].ID)
		}},
		{"events page and event by slug", func(t *testing.T) {
			page, err := env.gamma.ListEvents(ctx, EventsQuery{Limit: 5})
			require.NoError(t, err)
			require.NotEmpty(t, page.Items)
			assert.NotEmpty(t, page.NextCursor)

			event, err := env.gamma.GetEventBySlug(ctx, page.Items[0].Slug)
			require.NoError(t, err)
			assert.Equal(t, page.Items[0].ID, event.ID)
		}},
		{"tags", func(t *testing.T) {
			tags, err := env.gamma.ListTags(ctx, TagsQuery{Limit: 5})
			require.NoError(t, err)
			assert.NotEmpty(t, tags)
		}},
		{"search", func(t *testing.T) {
			res, err := env.gamma.Search(ctx, SearchQuery{Query: "bitcoin", LimitPerType: 3})
			require.NoError(t, err)
			assert.NotEmpty(t, res.Events)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLiveClob(t *testing.T) {
	ctx := liveContext(t)
	env := newLiveEnv(t, ctx)
	tokens := env.market.TokenIDs()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"order books", func(t *testing.T) {
			books, err := env.clob.GetOrderBooks(ctx, tokens...)
			require.NoError(t, err)
			assert.Len(t, books, len(tokens))
		}},
		{"midpoints", func(t *testing.T) {
			mids, err := env.clob.GetMidpoints(ctx, tokens...)
			require.NoError(t, err)
			assert.Len(t, mids, len(tokens))
		}},
		{"spreads", func(t *testing.T) {
			spreads, err := env.clob.GetSpreads(ctx, tokens...)
			require.NoError(t, err)
			assert.Len(t, spreads, len(tokens))
		}},
		{"prices quote both sides", func(t *testing.T) {
			prices, err := env.clob.GetPrices(ctx,
				PriceRequest{TokenID: tokens[0], Side: SideBuy},
				PriceRequest{TokenID: tokens[0], Side: SideSell},
			)
			require.NoError(t, err)
			assert.Len(t, prices[tokens[0]], 2)
		}},
		{"last trade prices", func(t *testing.T) {
			_, err := env.clob.GetLastTradePrices(ctx, tokens...)
			require.NoError(t, err)
		}},
		{"market info", func(t *testing.T) {
			info, err := env.clob.GetMarketInfo(ctx, env.market.ConditionID)
			require.NoError(t, err)
			assert.Len(t, info.Tokens, len(tokens))
		}},
		{"unknown market info is not found", func(t *testing.T) {
			_, err := env.clob.GetMarketInfo(ctx, "0xdead")
			assert.ErrorIs(t, err, ErrNotFound)
		}},
		{"server clock is close to ours", func(t *testing.T) {
			ts, err := env.clob.ServerTime(ctx)
			require.NoError(t, err)
			assert.WithinDuration(t, time.Now(), time.Unix(ts, 0), time.Minute)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLiveData(t *testing.T) {
	ctx := liveContext(t)
	env := newLiveEnv(t, ctx)
	cid := env.market.ConditionID

	holders, err := env.data.Holders(ctx, HoldersQuery{Conditions: []string{cid}, Limit: 3})
	require.NoError(t, err)
	require.NotEmpty(t, holders)
	require.NotEmpty(t, holders[0].Holders)

	wallet := holders[0].Holders[0].ProxyWallet

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"trades keep filters across pages", func(t *testing.T) {
			q := TradesQuery{Conditions: []string{cid}, Limit: 3}

			for range 2 {
				page, err := env.data.Trades(ctx, q)
				require.NoError(t, err)

				for _, tr := range page.Items {
					assert.Equal(t, cid, tr.ConditionID)
				}

				if page.NextCursor == "" {
					return
				}

				q.Cursor = page.NextCursor
			}
		}},
		{"positions", func(t *testing.T) {
			page, err := env.data.Positions(ctx, PositionsQuery{User: wallet, Limit: 5})
			require.NoError(t, err)
			assert.NotEmpty(t, page.Items)
		}},
		{"activity", func(t *testing.T) {
			_, err := env.data.Activity(ctx, ActivityQuery{User: wallet, Types: []string{"TRADE"}, Limit: 5})
			require.NoError(t, err)
		}},
		{"open interest", func(t *testing.T) {
			oi, err := env.data.OpenInterest(ctx, cid)
			require.NoError(t, err)
			assert.Len(t, oi, 1)
		}},
		{"price history", func(t *testing.T) {
			page, err := env.data.PriceHistory(ctx, PriceHistoryQuery{TokenID: env.market.TokenIDs()[0], Interval: "1d", Limit: 5})
			require.NoError(t, err)
			assert.NotEmpty(t, page.Items)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

// TestLiveV2Market checks the v2 token rule against real data once a v2 market exists.
func TestLiveV2Market(t *testing.T) {
	ctx := liveContext(t)
	env := newLiveEnv(t, ctx)

	closed := false
	q := MarketsQuery{Limit: 100, Closed: &closed}

	for range 20 {
		page, err := env.gamma.ListMarkets(ctx, q)
		require.NoError(t, err)

		for _, m := range page.Items {
			if m.Version != "v2" {
				continue
			}

			require.NotEmpty(t, m.PositionIDs)
			assert.Equal(t, m.PositionIDs, m.TokenIDs())

			if m.EnableOrderBook {
				books, err := env.clob.GetOrderBooks(ctx, m.TokenIDs()...)
				require.NoError(t, err)
				assert.Len(t, books, len(m.TokenIDs()), "position IDs should be tradable token IDs")
			}

			return
		}

		if page.NextCursor == "" {
			break
		}

		q.AfterCursor = page.NextCursor
	}

	t.Skip("no v2 market published yet")
}

func TestLiveStream(t *testing.T) {
	ctx := liveContext(t)
	env := newLiveEnv(t, ctx)

	stream, err := NewMarketStreamClient(observability.NoopUtility(), StreamConfig{})
	require.NoError(t, err)
	require.NoError(t, stream.Subscribe(ctx, env.market.TokenIDs()...))

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() { _ = stream.Run(streamCtx) }()

	for ev := range stream.Events() {
		if book, ok := ev.(BookEvent); ok {
			assert.Equal(t, env.market.ConditionID, book.Market)
			assert.Contains(t, env.market.TokenIDs(), book.AssetID)

			return
		}
	}

	require.FailNow(t, "stream closed before a book snapshot arrived")
}

// The activity topic is no longer documented: this says when it stops working as read.
func TestLiveActivity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	stream, err := NewActivityStreamClient(observability.NoopUtility(), ActivityStreamConfig{})
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()

	var trades, matched int

	for trades < 20 || matched < 1 {
		select {
		case ev := <-stream.Events():
			tr, ok := ev.(ActivityTrade)
			if !ok {
				continue
			}

			if tr.Type == ActivityOrdersMatched {
				matched++
				continue
			}

			trades++

			assert.Regexp(t, `^0x[0-9a-fA-F]{40}$`, tr.ProxyWallet)
			assert.NotEmpty(t, tr.TransactionHash)
			assert.NotEmpty(t, tr.Asset)
			assert.True(t, tr.Size.IsPositive(), "a fill has shares")
			assert.Positive(t, tr.Timestamp)
			assert.Contains(t, []Side{SideBuy, SideSell}, tr.Side)
		case <-ctx.Done():
			require.FailNow(t, "too little activity", "%d trades, %d matched orders", trades, matched)
		}
	}

	cancel()
	require.NoError(t, <-done)
}
