//go:build integration

// Live checks of the assumptions the adapter makes about Polymarket. Run with
// `make test-integration`.

package polymarket

import (
	"context"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func newLiveReader(t *testing.T) (*Reader, *pm.DefaultGammaClient) {
	t.Helper()

	util := observability.NoopUtility()

	gamma, err := pm.NewGammaClient(util, pm.HTTPConfig{})
	require.NoError(t, err)

	clob, err := pm.NewClobClient(util, pm.HTTPConfig{})
	require.NoError(t, err)

	data, err := pm.NewDataClient(util, pm.HTTPConfig{})
	require.NoError(t, err)

	return NewReader(gamma, clob, data), gamma
}

func TestLiveAdapter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reader, gamma := newLiveReader(t)

	page, err := reader.ListMarkets(ctx, ports.MarketFilter{Limit: 20})
	require.NoError(t, err)
	require.NotEmpty(t, page.Items)

	var open domain.Market
	for _, m := range page.Items {
		if m.AcceptingOrders && len(m.Outcomes) == 2 && m.Outcomes[0].TokenID != "" {
			open = m
			break
		}
	}
	require.NotEmpty(t, open.ID, "no open order-book market on the first page")

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"open market maps to open with tokens and prices", func(t *testing.T) {
			assert.Equal(t, domain.MarketOpen, open.Status)
			assert.False(t, open.EndsAt.IsZero())

			for _, o := range open.Outcomes {
				assert.NotEmpty(t, o.TokenID)
				assert.NotEmpty(t, o.Label)
			}
		}},
		{"get market by id round-trips", func(t *testing.T) {
			got, err := reader.GetMarket(ctx, open.ID)
			require.NoError(t, err)
			assert.Equal(t, open.Slug, got.Slug)
		}},
		{"unknown market id is not found", func(t *testing.T) {
			_, err := reader.GetMarket(ctx, "0x0000000000000000000000000000000000000000000000000000000000000000")
			assert.ErrorIs(t, err, domain.ErrNotFound)
		}},
		{"closed markets are found by id", func(t *testing.T) {
			// The oldest closed market: Gamma hides it unless asked for closed markets explicitly.
			closed := true
			oldest, err := gamma.ListMarkets(ctx, pm.MarketsQuery{Closed: &closed, Limit: 1})
			require.NoError(t, err)
			require.NotEmpty(t, oldest.Items)

			got, err := reader.GetMarket(ctx, domain.MarketID(oldest.Items[0].ConditionID))
			require.NoError(t, err)
			assert.Contains(t, []domain.MarketStatus{domain.MarketClosed, domain.MarketResolved, domain.MarketArchived}, got.Status)
		}},
		{"order books come back best first", func(t *testing.T) {
			books, err := reader.OrderBooks(ctx, open.TokenIDs()...)
			require.NoError(t, err)
			require.NotEmpty(t, books)

			for _, b := range books {
				for i := 1; i < len(b.Bids); i++ {
					assert.True(t, b.Bids[i-1].Price.GreaterThanOrEqual(b.Bids[i].Price), "bids not best first")
				}

				for i := 1; i < len(b.Asks); i++ {
					assert.True(t, b.Asks[i-1].Price.LessThanOrEqual(b.Asks[i].Price), "asks not best first")
				}

				if bid, ok := b.BestBid(); ok {
					if ask, ok := b.BestAsk(); ok {
						assert.True(t, bid.Price.LessThan(ask.Price), "crossed book")
					}
				}
			}
		}},
		{"trades for the market", func(t *testing.T) {
			trades, err := reader.Trades(ctx, ports.TradeFilter{MarketIDs: []domain.MarketID{open.ID}, Limit: 5})
			require.NoError(t, err)

			for _, tr := range trades.Items {
				assert.Equal(t, open.ID, tr.MarketID)
				assert.Contains(t, []domain.Side{domain.SideBuy, domain.SideSell}, tr.Side)
				assert.False(t, tr.At.IsZero())
			}
		}},
		{"price history for the last day", func(t *testing.T) {
			history, err := reader.PriceHistory(ctx, ports.PriceHistoryFilter{
				TokenID: open.Outcomes[0].TokenID,
				Since:   time.Now().Add(-24 * time.Hour),
				Bucket:  time.Hour,
			})
			require.NoError(t, err)
			assert.NotEmpty(t, history.Items)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestLiveAdapterStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reader, _ := newLiveReader(t)

	page, err := reader.ListMarkets(ctx, ports.MarketFilter{Limit: 20})
	require.NoError(t, err)

	var tokens []domain.TokenID
	for _, m := range page.Items {
		if m.AcceptingOrders && len(m.Outcomes) > 0 && m.Outcomes[0].TokenID != "" {
			tokens = m.TokenIDs()
			break
		}
	}
	require.NotEmpty(t, tokens)

	client, err := pm.NewMarketStreamClient(observability.NoopUtility(), pm.StreamConfig{})
	require.NoError(t, err)

	stream := NewStream(client)
	require.NoError(t, stream.Subscribe(ctx, tokens...))

	streamCtx, stop := context.WithCancel(ctx)
	defer stop()

	go func() { _ = stream.Run(streamCtx) }()

	for update := range stream.Updates() {
		if snap, ok := update.(domain.BookSnapshot); ok {
			assert.Contains(t, tokens, snap.TokenID)
			assert.False(t, snap.At.IsZero())

			return
		}
	}

	require.FailNow(t, "no book snapshot")
}

func TestLiveAccountRejectsUnknownCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	clob, err := pm.NewClobClient(observability.NoopUtility(), pm.HTTPConfig{})
	require.NoError(t, err)

	err = NewAccount(clob).VerifyCredentials(ctx, domain.PolymarketCredentials{
		APIKey:     "4f6d3b52-9a3e-4c8e-8f0a-2b7c1d5e9f01",
		Secret:     "c2VjcmV0LWJ5dGVzLXNlY3JldC1ieXRlcy1zZWNyZXQ=",
		Passphrase: "not-a-real-passphrase",
		Signer:     "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
	})
	require.ErrorIs(t, err, domain.ErrCredentialsRejected, "a well-formed request with an unknown key is refused, not malformed")
}
