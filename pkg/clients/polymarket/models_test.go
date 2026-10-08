package polymarket

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStringifiedStrings(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "encoded array", in: `"[\"Yes\", \"No\"]"`, want: []string{"Yes", "No"}},
		{name: "plain array", in: `["Yes","No"]`, want: []string{"Yes", "No"}},
		{name: "null", in: `null`},
		{name: "empty string", in: `""`},
		{name: "invalid inner json", in: `"[not json"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Stringified[string]
			err := json.Unmarshal([]byte(tt.in), &got)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, []string(got))
		})
	}
}

func TestStringifiedDecimals(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "encoded prices", in: `"[\"0.0265\", \"0.9735\"]"`, want: []string{"0.0265", "0.9735"}},
		{name: "plain numbers", in: `[0.5, 0.5]`, want: []string{"0.5", "0.5"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Stringified[decimal.Decimal]
			require.NoError(t, json.Unmarshal([]byte(tt.in), &got))
			require.Len(t, got, len(tt.want))

			for i, want := range tt.want {
				assert.True(t, got[i].Equal(decimal.RequireFromString(want)), "index %d: got %s", i, got[i])
			}
		})
	}
}

func TestOptDecimal(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantValid bool
		want      string
		wantErr   bool
	}{
		{name: "empty string is absent", in: `""`, want: "0"},
		{name: "null is absent", in: `null`, want: "0"},
		{name: "quoted number", in: `"0.027"`, wantValid: true, want: "0.027"},
		{name: "bare number", in: `0.027`, wantValid: true, want: "0.027"},
		{name: "zero is present", in: `"0"`, wantValid: true, want: "0"},
		{name: "not a number", in: `"abc"`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got OptDecimal
			err := json.Unmarshal([]byte(tt.in), &got)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantValid, got.Valid)
			assert.Equal(t, tt.want, got.Decimal.String())
		})
	}
}

func TestMarketTokenIDs(t *testing.T) {
	tests := []struct {
		name   string
		market Market
		want   []string
	}{
		{
			name:   "v1 uses clobTokenIds",
			market: Market{Version: "v1", ClobTokenIDs: []string{"a", "b"}, PositionIDs: []string{"x", "y"}},
			want:   []string{"a", "b"},
		},
		{
			name:   "missing version is treated as v1",
			market: Market{ClobTokenIDs: []string{"a", "b"}},
			want:   []string{"a", "b"},
		},
		{
			// Synthetic until a v2 market is live; TestDecodeFixtures covers the real one.
			name:   "v2 uses positionIds",
			market: Market{Version: "v2", ClobTokenIDs: []string{"a", "b"}, PositionIDs: []string{"x", "y"}},
			want:   []string{"x", "y"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.market.TokenIDs())
		})
	}
}

func TestDecodeFixtures(t *testing.T) {
	tests := []struct {
		file     string
		optional bool // written by `make fixtures` only once such data exists upstream
		check    func(t *testing.T, raw []byte)
	}{
		{
			file: "gamma_markets_keyset.json",
			check: func(t *testing.T, raw []byte) {
				var page struct {
					Markets    []Market `json:"markets"`
					NextCursor string   `json:"next_cursor"`
				}
				require.NoError(t, json.Unmarshal(raw, &page))
				require.NotEmpty(t, page.Markets)
				assert.NotEmpty(t, page.NextCursor)

				for _, m := range page.Markets {
					assert.Regexp(t, `^0x[0-9a-f]{64}$`, m.ConditionID)
					assert.NotEmpty(t, m.Question)
					assert.NotEmpty(t, m.Outcomes)
					assert.Len(t, m.OutcomePrices, len(m.Outcomes))
					assert.Len(t, m.TokenIDs(), len(m.Outcomes))
				}
			},
		},
		{
			file:     "gamma_market_v2.json",
			optional: true,
			check: func(t *testing.T, raw []byte) {
				var m Market
				require.NoError(t, json.Unmarshal(raw, &m))
				assert.Equal(t, "v2", m.Version)
				assert.NotEmpty(t, m.PositionIDs)
				assert.Equal(t, m.PositionIDs, m.TokenIDs())
			},
		},
		{
			file: "gamma_events_keyset.json",
			check: func(t *testing.T, raw []byte) {
				var page struct {
					Events []Event `json:"events"`
				}
				require.NoError(t, json.Unmarshal(raw, &page))
				require.Len(t, page.Events, 1)
				assert.NotEmpty(t, page.Events[0].Markets)
			},
		},
		{
			file: "clob_books.json",
			check: func(t *testing.T, raw []byte) {
				var books []OrderBook
				require.NoError(t, json.Unmarshal(raw, &books))
				require.Len(t, books, 2)

				traded, fresh := books[0], books[1]
				assert.True(t, traded.LastTradePrice.Valid)
				require.NotEmpty(t, traded.Bids)
				require.NotEmpty(t, traded.Asks)
				assert.False(t, fresh.LastTradePrice.Valid, "never-traded book has no last trade price")

				// Worst to best: the best bid is the highest and comes last.
				best := traded.Bids[len(traded.Bids)-1].Price
				assert.True(t, best.GreaterThanOrEqual(traded.Bids[0].Price), "bids not ascending: %v", traded.Bids)
			},
		},
		{
			file: "clob_market_info.json",
			check: func(t *testing.T, raw []byte) {
				var info MarketInfo
				require.NoError(t, json.Unmarshal(raw, &info))
				assert.Len(t, info.Tokens, 2)
				assert.False(t, info.MinTickSize.IsZero())
				assert.NotEmpty(t, info.Version)
			},
		},
		{
			file: "data_trades.json",
			check: func(t *testing.T, raw []byte) {
				var page dataEnvelope[[]Trade]
				require.NoError(t, json.Unmarshal(raw, &page))
				require.NotEmpty(t, page.Data)
				assert.NotEmpty(t, page.Pagination.NextCursor)

				tr := page.Data[0]
				assert.False(t, tr.Price.IsZero())
				assert.False(t, tr.Size.IsZero())
				assert.NotZero(t, tr.Timestamp)
				assert.NotEmpty(t, tr.Side)
			},
		},
		{
			file: "data_positions.json",
			check: func(t *testing.T, raw []byte) {
				var page dataEnvelope[[]Position]
				require.NoError(t, json.Unmarshal(raw, &page))
				require.NotEmpty(t, page.Data)
				assert.False(t, page.Data[0].CurrentSize.IsZero())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if tt.optional && errors.Is(err, fs.ErrNotExist) {
				t.Skip("no live data of this kind yet; `make fixtures` writes it once it exists")
			}

			require.NoError(t, err)
			tt.check(t, raw)
		})
	}
}
