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

var creds = domain.PolymarketCredentials{
	APIKey: "Key-1", Secret: "c2VjcmV0", Passphrase: "pp",
	Signer: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", SignatureType: domain.SignaturePolyProxy,
}

var l2 = pm.L2Credentials{Address: "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359", APIKey: "Key-1", Secret: "c2VjcmV0", Passphrase: "pp"}

func TestVerifyCredentials(t *testing.T) {
	tests := []struct {
		name    string
		keys    []string
		err     error
		wantErr error
	}{
		{name: "key is listed (case-insensitive)", keys: []string{"other", "key-1"}},
		{name: "key is not listed", keys: []string{"other"}, wantErr: domain.ErrCredentialsRejected},
		{name: "refused", err: pm.ErrUnauthorized, wantErr: domain.ErrCredentialsRejected},
		{name: "secret cannot be decoded", err: pm.ErrInvalidSecret, wantErr: domain.ErrCredentialsRejected},
		{name: "exchange unreachable", err: errors.New("dial tcp: timeout"), wantErr: domain.ErrUnavailable},
		{name: "caller cancelled", err: context.Canceled, wantErr: context.Canceled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clob := polymarketmocks.NewMockClobAccountClient(t)
			clob.EXPECT().APIKeys(mock.Anything, l2).Return(tt.keys, tt.err)

			err := NewAccount(clob).VerifyCredentials(context.Background(), creds)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestAccountReads(t *testing.T) {
	tests := []struct {
		name  string
		setup func(c *polymarketmocks.MockClobAccountClient)
		call  func(a *Account) (any, error)
		want  any
	}{
		{
			name: "orders map to the domain",
			setup: func(c *polymarketmocks.MockClobAccountClient) {
				c.EXPECT().OpenOrders(mock.Anything, l2, pm.OpenOrdersQuery{Market: "0xm", Cursor: "c1"}).Return(pm.Page[pm.OpenOrder]{
					Items: []pm.OpenOrder{{
						ID: "0xo", Status: "LIVE", Market: "0xm", AssetID: "t1", Side: pm.SideSell, Outcome: "Yes",
						Price: dec("0.6"), OriginalSize: dec("10"), SizeMatched: dec("4"), OrderType: "GTD",
						CreatedAt: 1700000000, Expiration: "1800000000",
					}, {
						ID: "0xp", Status: "LIVE", Side: pm.SideBuy, OrderType: "GTC", Expiration: "0",
					}},
					NextCursor: "c2",
				}, nil)
			},
			call: func(a *Account) (any, error) {
				return a.OpenOrders(context.Background(), creds, ports.OrderFilter{MarketID: "0xm", Cursor: "c1"})
			},
			want: ports.Page[domain.Order]{Items: []domain.Order{{
				ID: "0xo", MarketID: "0xm", TokenID: "t1", Outcome: "Yes", Side: domain.SideSell,
				Price: dec("0.6"), Size: dec("10"), Filled: dec("4"), Status: "live", Type: "GTD",
				CreatedAt: time.Unix(1700000000, 0).UTC(), ExpiresAt: time.Unix(1800000000, 0).UTC(),
			}, {
				ID: "0xp", Side: domain.SideBuy, Status: "live", Type: "GTC", CreatedAt: time.Unix(0, 0).UTC(),
			}}, NextCursor: "c2"},
		},
		{
			name: "balance is scaled from fixed point and uses the signature type",
			setup: func(c *polymarketmocks.MockClobAccountClient) {
				c.EXPECT().BalanceAllowance(mock.Anything, l2, pm.BalanceQuery{AssetType: pm.AssetCollateral, SignatureType: 1}).
					Return(pm.BalanceAllowance{Balance: dec("123456789")}, nil)
			},
			call: func(a *Account) (any, error) { return a.CollateralBalance(context.Background(), creds) },
			want: dec("123.456789"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clob := polymarketmocks.NewMockClobAccountClient(t)
			tt.setup(clob)

			got, err := tt.call(NewAccount(clob))
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
