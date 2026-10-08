package polymarket

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

type clientMocks struct {
	gamma *polymarketmocks.MockGammaClient
	clob  *polymarketmocks.MockClobClient
	data  *polymarketmocks.MockDataClient
}

var errUpstream = errors.New("upstream down")

func TestReader(t *testing.T) {
	closed := true
	since := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)

	apiMarket := pm.Market{ConditionID: "0xc", Slug: "s", Outcomes: []string{"Yes"}, ClobTokenIDs: []string{"t1"}}

	tests := []struct {
		name    string
		setup   func(m clientMocks)
		call    func(ctx context.Context, r *Reader) (any, error)
		want    any
		wantErr error
	}{
		{
			name: "list markets maps filter and page",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().ListMarkets(mock.Anything, pm.MarketsQuery{
					Limit: 10, AfterCursor: "cur", Closed: &closed,
					TagIDs: []string{"tag"}, ConditionIDs: []string{"0xa", "0xb"}, Slugs: []string{"s"},
				}).Return(pm.Page[pm.Market]{Items: []pm.Market{apiMarket}, NextCursor: "next"}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.ListMarkets(ctx, ports.MarketFilter{
					Limit: 10, Cursor: "cur", Closed: &closed,
					MarketIDs: []domain.MarketID{"0xa", "0xb"}, Slugs: []string{"s"}, TagIDs: []string{"tag"},
				})
			},
			want: ports.Page[domain.Market]{Items: []domain.Market{toMarket(apiMarket)}, NextCursor: "next"},
		},
		{
			name: "get market finds an open market in one lookup",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().ListMarkets(mock.Anything, pm.MarketsQuery{ConditionIDs: []string{"0xc"}, Limit: 1}).
					Return(pm.Page[pm.Market]{Items: []pm.Market{apiMarket}}, nil).Once()
			},
			call: func(ctx context.Context, r *Reader) (any, error) { return r.GetMarket(ctx, "0xc") },
			want: toMarket(apiMarket),
		},
		{
			name: "get market retries a miss among closed markets",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().ListMarkets(mock.Anything, pm.MarketsQuery{ConditionIDs: []string{"0xc"}, Limit: 1}).
					Return(pm.Page[pm.Market]{}, nil).Once()
				m.gamma.EXPECT().ListMarkets(mock.Anything, pm.MarketsQuery{ConditionIDs: []string{"0xc"}, Closed: &closed, Limit: 1}).
					Return(pm.Page[pm.Market]{Items: []pm.Market{apiMarket}}, nil).Once()
			},
			call: func(ctx context.Context, r *Reader) (any, error) { return r.GetMarket(ctx, "0xc") },
			want: toMarket(apiMarket),
		},
		{
			name: "get market with no match anywhere is not found",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().ListMarkets(mock.Anything, mock.Anything).Return(pm.Page[pm.Market]{}, nil).Twice()
			},
			call:    func(ctx context.Context, r *Reader) (any, error) { return r.GetMarket(ctx, "0xmissing") },
			wantErr: domain.ErrNotFound,
		},
		{
			name: "get market stops on a client error",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().ListMarkets(mock.Anything, mock.Anything).Return(pm.Page[pm.Market]{}, errUpstream).Once()
			},
			call:    func(ctx context.Context, r *Reader) (any, error) { return r.GetMarket(ctx, "0xc") },
			wantErr: errUpstream,
		},
		{
			name: "get market by slug",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().GetMarketBySlug(mock.Anything, "s").Return(apiMarket, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) { return r.GetMarketBySlug(ctx, "s") },
			want: toMarket(apiMarket),
		},
		{
			name: "client not found becomes domain not found",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().GetMarketBySlug(mock.Anything, "gone").Return(pm.Market{}, pm.ErrNotFound)
			},
			call:    func(ctx context.Context, r *Reader) (any, error) { return r.GetMarketBySlug(ctx, "gone") },
			wantErr: domain.ErrNotFound,
		},
		{
			name: "other client errors pass through",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().GetEvent(mock.Anything, "7").Return(pm.Event{}, errUpstream)
			},
			call:    func(ctx context.Context, r *Reader) (any, error) { return r.GetEvent(ctx, "7") },
			wantErr: errUpstream,
		},
		{
			name: "list events maps filter",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().ListEvents(mock.Anything, pm.EventsQuery{
					Limit: 5, AfterCursor: "c", Closed: &closed, Featured: &closed, TitleSearch: "btc",
				}).Return(pm.Page[pm.Event]{Items: []pm.Event{{ID: "1"}}}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.ListEvents(ctx, ports.EventFilter{Limit: 5, Cursor: "c", Closed: &closed, Featured: &closed, Title: "btc"})
			},
			want: ports.Page[domain.Event]{Items: []domain.Event{toEvent(pm.Event{ID: "1"})}},
		},
		{
			name: "get event by slug",
			setup: func(m clientMocks) {
				m.gamma.EXPECT().GetEventBySlug(mock.Anything, "e").Return(pm.Event{ID: "7"}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) { return r.GetEventBySlug(ctx, "e") },
			want: toEvent(pm.Event{ID: "7"}),
		},
		{
			name: "order books are sorted best first",
			setup: func(m clientMocks) {
				m.clob.EXPECT().GetOrderBooks(mock.Anything, []string{"t1"}).Return([]pm.OrderBook{{
					Market: "0xc", AssetID: "t1", Timestamp: "1791450379907", TickSize: dec("0.01"),
					Bids: []pm.PriceLevel{{Price: dec("0.1"), Size: dec("5")}, {Price: dec("0.2"), Size: dec("6")}},
					Asks: []pm.PriceLevel{{Price: dec("0.4"), Size: dec("7")}, {Price: dec("0.3"), Size: dec("8")}},
				}}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) { return r.OrderBooks(ctx, "t1") },
			want: []domain.OrderBook{{
				MarketID: "0xc", TokenID: "t1", TickSize: dec("0.01"), At: time.UnixMilli(1791450379907).UTC(),
				Bids: []domain.Level{{Price: dec("0.2"), Size: dec("6")}, {Price: dec("0.1"), Size: dec("5")}},
				Asks: []domain.Level{{Price: dec("0.3"), Size: dec("8")}, {Price: dec("0.4"), Size: dec("7")}},
			}},
		},
		{
			name: "trades map filter and rows",
			setup: func(m clientMocks) {
				m.data.EXPECT().Trades(mock.Anything, pm.TradesQuery{
					User: "0xw", Conditions: []string{"0xc"}, Side: pm.SideSell,
					Start: since.Unix(), End: until.Unix(), Limit: 3, Cursor: "c",
				}).Return(pm.Page[pm.Trade]{Items: []pm.Trade{{
					ProxyWallet: "0xw", Side: pm.SideSell, TokenID: "t1", ConditionID: "0xc",
					Price: dec("0.5"), Size: dec("10"), Timestamp: 1791450374, Outcome: "No", TransactionHash: "0xh",
				}}, NextCursor: "n"}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.Trades(ctx, ports.TradeFilter{
					Wallet: "0xw", MarketIDs: []domain.MarketID{"0xc"}, Side: domain.SideSell,
					Since: since, Until: until, Limit: 3, Cursor: "c",
				})
			},
			want: ports.Page[domain.Trade]{Items: []domain.Trade{{
				MarketID: "0xc", TokenID: "t1", Outcome: "No", Side: domain.SideSell, Price: dec("0.5"), Size: dec("10"),
				Wallet: "0xw", At: time.Unix(1791450374, 0).UTC(), TxHash: "0xh",
			}}, NextCursor: "n"},
		},
		{
			name: "unbounded trades send no time range",
			setup: func(m clientMocks) {
				m.data.EXPECT().Trades(mock.Anything, pm.TradesQuery{}).Return(pm.Page[pm.Trade]{}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) { return r.Trades(ctx, ports.TradeFilter{}) },
			want: ports.Page[domain.Trade]{Items: []domain.Trade{}},
		},
		{
			name: "positions",
			setup: func(m clientMocks) {
				m.data.EXPECT().Positions(mock.Anything, pm.PositionsQuery{User: "0xw", Limit: 2}).
					Return(pm.Page[pm.Position]{Items: []pm.Position{{
						ProxyWallet: "0xw", ConditionID: "0xc", TokenID: "t1", Outcome: "Yes",
						CurrentSize: dec("100"), AvgPrice: dec("0.2"), CurrentPrice: dec("0.3"), CurrentValue: dec("30"),
						RealizedPnl: dec("1"), UnrealizedPnl: dec("10"), Redeemable: true,
					}}}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.Positions(ctx, ports.PositionFilter{Wallet: "0xw", Limit: 2})
			},
			want: ports.Page[domain.Position]{Items: []domain.Position{{
				Wallet: "0xw", MarketID: "0xc", TokenID: "t1", Outcome: "Yes",
				Size: dec("100"), AvgPrice: dec("0.2"), CurrentPrice: dec("0.3"), Value: dec("30"),
				RealizedPnL: dec("1"), UnrealizedPnL: dec("10"), Redeemable: true,
			}}},
		},
		{
			name: "price history without a range asks for everything",
			setup: func(m clientMocks) {
				m.data.EXPECT().PriceHistory(mock.Anything, pm.PriceHistoryQuery{TokenID: "t1", Interval: "max", BucketSeconds: 3600}).
					Return(pm.Page[pm.PricePoint]{Items: []pm.PricePoint{{Timestamp: 1791450000, Price: dec("0.42")}}}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.PriceHistory(ctx, ports.PriceHistoryFilter{TokenID: "t1", Bucket: time.Hour})
			},
			want: ports.Page[domain.PricePoint]{Items: []domain.PricePoint{{At: time.Unix(1791450000, 0).UTC(), Price: dec("0.42")}}},
		},
		{
			name: "price history with a closed range",
			setup: func(m clientMocks) {
				m.data.EXPECT().PriceHistory(mock.Anything, pm.PriceHistoryQuery{TokenID: "t1", Start: since.Unix(), End: until.Unix()}).
					Return(pm.Page[pm.PricePoint]{}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.PriceHistory(ctx, ports.PriceHistoryFilter{TokenID: "t1", Since: since, Until: until})
			},
			want: ports.Page[domain.PricePoint]{Items: []domain.PricePoint{}},
		},
		{
			name: "price history since a time runs until now",
			setup: func(m clientMocks) {
				m.data.EXPECT().PriceHistory(mock.Anything, mock.MatchedBy(func(q pm.PriceHistoryQuery) bool {
					return q.Interval == "" && q.Start == since.Unix() && time.Since(time.Unix(q.End, 0)) < time.Minute
				})).Return(pm.Page[pm.PricePoint]{}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.PriceHistory(ctx, ports.PriceHistoryFilter{TokenID: "t1", Since: since})
			},
			want: ports.Page[domain.PricePoint]{Items: []domain.PricePoint{}},
		},
		{
			name: "price history until a time starts at the beginning",
			setup: func(m clientMocks) {
				m.data.EXPECT().PriceHistory(mock.Anything, pm.PriceHistoryQuery{TokenID: "t1", Start: 1, End: until.Unix()}).
					Return(pm.Page[pm.PricePoint]{}, nil)
			},
			call: func(ctx context.Context, r *Reader) (any, error) {
				return r.PriceHistory(ctx, ports.PriceHistoryFilter{TokenID: "t1", Until: until})
			},
			want: ports.Page[domain.PricePoint]{Items: []domain.PricePoint{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := clientMocks{
				gamma: polymarketmocks.NewMockGammaClient(t),
				clob:  polymarketmocks.NewMockClobClient(t),
				data:  polymarketmocks.NewMockDataClient(t),
			}
			tt.setup(m)

			got, err := tt.call(context.Background(), NewReader(m.gamma, m.clob, m.data))

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
