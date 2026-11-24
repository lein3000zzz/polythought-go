package marketdata_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/marketdata"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const alice = domain.UserID("u-1")

var now = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

// forAlice says the call counts toward alice's fair share of the reader (A11).
func forAlice(ctx context.Context) bool {
	return ports.UserOf(ctx) == alice
}

func TestSearch(t *testing.T) {
	events := portsmocks.NewMockEventReader(t)
	closed := false
	events.EXPECT().ListEvents(mock.MatchedBy(forAlice), ports.EventFilter{Title: "rain", Limit: 50, Closed: &closed}).
		Return(ports.Page[domain.Event]{Items: []domain.Event{{
			Title: "Rain in NYC", Markets: []domain.Market{{ID: "0x1", Status: domain.MarketOpen}, {ID: "0x2", Status: domain.MarketClosed}},
		}}}, nil).Once()

	svc := marketdata.New(marketdata.Deps{Events: events})

	found, err := svc.Search(context.Background(), alice, " rain ", false, 0)
	require.NoError(t, err)
	require.Len(t, found, 1, "an open event's closed market left out")
	assert.Equal(t, domain.MarketID("0x1"), found[0].Market.ID)

	for _, tc := range []struct {
		query string
		limit int
	}{{query: "", limit: 1}, {query: "rain", limit: 501}, {query: "rain", limit: -1}} {
		_, err := svc.Search(context.Background(), alice, tc.query, false, tc.limit)
		require.ErrorIs(t, err, domain.ErrInvalidInput, tc)
	}
}

func TestMarket(t *testing.T) {
	markets := portsmocks.NewMockMarketReader(t)
	markets.EXPECT().GetMarket(mock.MatchedBy(forAlice), domain.MarketID("0xabc")).Return(domain.Market{ID: "0xabc"}, nil).Once()
	markets.EXPECT().GetMarketBySlug(mock.MatchedBy(forAlice), "rain-in-nyc").Return(domain.Market{}, domain.ErrNotFound).Once()

	svc := marketdata.New(marketdata.Deps{Markets: markets})

	m, err := svc.Market(context.Background(), alice, "0xabc")
	require.NoError(t, err)
	assert.Equal(t, domain.MarketID("0xabc"), m.ID)

	_, err = svc.Market(context.Background(), alice, "rain-in-nyc")
	require.ErrorIs(t, err, domain.ErrNotFound)

	_, err = svc.Market(context.Background(), alice, " ")
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestBooks(t *testing.T) {
	books := portsmocks.NewMockOrderBookReader(t)
	books.EXPECT().OrderBooks(mock.MatchedBy(forAlice), []domain.TokenID{"t1", "t2"}).
		Return([]domain.OrderBook{{TokenID: "t2"}}, nil).Once()

	svc := marketdata.New(marketdata.Deps{Books: books})

	got, err := svc.Books(context.Background(), alice, []domain.TokenID{"t1", "t2"})
	require.NoError(t, err)
	require.Len(t, got, 2, "in the order asked")
	assert.Empty(t, got[0].TokenID, "none for t1")
	assert.Equal(t, domain.TokenID("t2"), got[1].TokenID)

	_, err = svc.Books(context.Background(), alice, nil)
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	_, err = svc.Books(context.Background(), alice, make([]domain.TokenID, 51))
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestHistory(t *testing.T) {
	history := portsmocks.NewMockPriceHistoryReader(t)

	var points []domain.PricePoint
	for i := range 10 {
		points = append(points, domain.PricePoint{At: now.Add(-time.Duration(i) * time.Hour), Price: decimal.NewFromInt(int64(i))})
	}

	history.EXPECT().PriceHistory(mock.MatchedBy(forAlice), ports.PriceHistoryFilter{
		TokenID: "t1", Since: now.Add(-24 * time.Hour), Bucket: 6 * time.Hour,
	}).Return(ports.Page[domain.PricePoint]{Items: points}, nil).Once()

	svc := marketdata.New(marketdata.Deps{History: history, Now: func() time.Time { return now }})

	got, err := svc.History(context.Background(), alice, "t1", "1d", 4)
	require.NoError(t, err)
	require.Len(t, got, 4, "evenly, at most the limit")
	assert.True(t, got[0].At.Before(got[3].At), "oldest first")
	assert.Equal(t, now.Add(-9*time.Hour), got[0].At, "the first kept")
	assert.Equal(t, now, got[3].At, "and the last")

	_, err = svc.History(context.Background(), alice, "t1", "1y", 0)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestTradesAndPositions(t *testing.T) {
	trades := portsmocks.NewMockTradeReader(t)
	trades.EXPECT().Trades(mock.MatchedBy(forAlice), ports.TradeFilter{MarketIDs: []domain.MarketID{"0xabc"}, Limit: 500}).
		Return(ports.Page[domain.Trade]{Items: []domain.Trade{{MarketID: "0xabc"}}}, nil).Once()

	address := "0x5f3C00000000000000000000000000000000A1b2"
	held := portsmocks.NewMockPositionReader(t)
	held.EXPECT().Positions(mock.MatchedBy(forAlice), mock.MatchedBy(func(f ports.PositionFilter) bool {
		return f.Limit == 50 && string(f.Wallet) == address
	})).Return(ports.Page[domain.Position]{Items: []domain.Position{{MarketID: "0xabc"}}}, nil).Once()

	svc := marketdata.New(marketdata.Deps{Trades: trades, Positions: held})

	got, err := svc.Trades(context.Background(), alice, "0xabc", 500)
	require.NoError(t, err)
	assert.Len(t, got, 1)

	_, err = svc.Trades(context.Background(), alice, "rain-in-nyc", 0)
	require.ErrorIs(t, err, domain.ErrInvalidInput, "a condition ID")

	pos, err := svc.Positions(context.Background(), alice, address, 0)
	require.NoError(t, err)
	assert.Len(t, pos, 1)

	_, err = svc.Positions(context.Background(), alice, "0xnope", 0)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}
