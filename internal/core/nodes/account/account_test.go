package account_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/account"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const owner = domain.UserID("u-1")

var (
	wallet = domain.Wallet{ID: "w-1", UserID: owner, Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"}
	creds  = domain.PolymarketCredentials{APIKey: "k", Secret: "s", Passphrase: "p", Signer: wallet.Address}
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type mocks struct {
	wallets   *portsmocks.MockWalletDirectory
	positions *portsmocks.MockPositionReader
	account   *portsmocks.MockAccountReader
}

func run(t *testing.T, node engine.Node, params map[string]any, setup func(m mocks)) (engine.Ports, error) {
	t.Helper()

	m := mocks{
		wallets:   portsmocks.NewMockWalletDirectory(t),
		positions: portsmocks.NewMockPositionReader(t),
		account:   portsmocks.NewMockAccountReader(t),
	}
	setup(m)

	var n engine.Node

	switch node.Type().Name {
	case account.PositionsType:
		n = account.NewPositions(m.wallets, m.positions)
	case account.OrdersType:
		n = account.NewOrders(m.wallets, m.account)
	default:
		n = account.NewBalance(m.wallets, m.account)
	}

	rc := engine.RunContext{OwnerID: owner, Params: engine.LiteralParams(n.Type(), params)}

	out, err := n.Run(context.Background(), rc, engine.Ports{engine.DefaultPort: {{JSON: map[string]any{}}}})
	if err == nil {
		require.NoError(t, shape.Check(n.Type(), out), "items as declared")
	}

	return out, err
}

func TestPositions(t *testing.T) {
	node := account.NewPositions(nil, nil)

	tests := []struct {
		name       string
		params     map[string]any
		setup      func(m mocks)
		wantItems  int
		wantFailed int
		check      func(t *testing.T, out engine.Ports)
	}{
		{
			name:   "positions of the owner's wallet",
			params: map[string]any{"wallet": "w-1"},
			setup: func(m mocks) {
				m.wallets.EXPECT().Wallet(mock.Anything, owner, domain.WalletID("w-1")).Return(wallet, nil)
				m.positions.EXPECT().Positions(mock.Anything, ports.PositionFilter{Wallet: wallet.Address, Limit: 50}).
					Return(ports.Page[domain.Position]{Items: []domain.Position{{MarketID: "0xm", TokenID: "t1", Outcome: "Yes", Size: dec("100"), Value: dec("42")}}}, nil)
			},
			wantItems: 1,
			check: func(t *testing.T, out engine.Ports) {
				j := out[engine.DefaultPort][0].JSON
				assert.Equal(t, "w-1", j["walletId"])
				assert.Equal(t, string(wallet.Address), j["address"])
				assert.True(t, j["value"].(decimal.Decimal).Equal(dec("42")))
			},
		},
		{
			name:   "someone else's wallet fails the item",
			params: map[string]any{"wallet": "w-9"},
			setup: func(m mocks) {
				m.wallets.EXPECT().Wallet(mock.Anything, owner, domain.WalletID("w-9")).Return(domain.Wallet{}, domain.ErrNotFound)
			},
			wantFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, node, tt.params, tt.setup)
			require.NoError(t, err)

			assert.Len(t, out[engine.DefaultPort], tt.wantItems)
			assert.Len(t, out[engine.ErrorPort], tt.wantFailed)

			if tt.check != nil {
				tt.check(t, out)
			}
		})
	}
}

func TestOrders(t *testing.T) {
	node := account.NewOrders(nil, nil)
	order := domain.Order{ID: "0xo", MarketID: "0xm", Side: domain.SideBuy, Size: dec("10"), Filled: dec("4"), Status: "live"}

	tests := []struct {
		name       string
		params     map[string]any
		setup      func(m mocks)
		wantItems  int
		wantFailed error
	}{
		{
			name:   "follows pages and filters by market",
			params: map[string]any{"wallet": "w-1", "market": "0xm"},
			setup: func(m mocks) {
				m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-1")).Return(creds, nil)
				m.account.EXPECT().OpenOrders(mock.Anything, creds, ports.OrderFilter{MarketID: "0xm"}).
					Return(ports.Page[domain.Order]{Items: []domain.Order{order}, NextCursor: "c2"}, nil).Once()
				m.account.EXPECT().OpenOrders(mock.Anything, creds, ports.OrderFilter{MarketID: "0xm", Cursor: "c2"}).
					Return(ports.Page[domain.Order]{Items: []domain.Order{order}}, nil).Once()
			},
			wantItems: 2,
		},
		{
			name:   "wallet without credentials",
			params: map[string]any{"wallet": "w-1"},
			setup: func(m mocks) {
				m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-1")).Return(domain.PolymarketCredentials{}, domain.ErrNotFound)
				m.wallets.EXPECT().Wallet(mock.Anything, owner, domain.WalletID("w-1")).Return(wallet, nil)
			},
			wantFailed: account.ErrNoCredentials,
		},
		{
			name:   "unknown wallet",
			params: map[string]any{"wallet": "w-9"},
			setup: func(m mocks) {
				m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-9")).Return(domain.PolymarketCredentials{}, domain.ErrNotFound)
				m.wallets.EXPECT().Wallet(mock.Anything, owner, domain.WalletID("w-9")).Return(domain.Wallet{}, domain.ErrNotFound)
			},
			wantFailed: domain.ErrNotFound,
		},
		{
			name:   "rejected credentials",
			params: map[string]any{"wallet": "w-1"},
			setup: func(m mocks) {
				m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-1")).Return(creds, nil)
				m.account.EXPECT().OpenOrders(mock.Anything, creds, mock.Anything).Return(ports.Page[domain.Order]{}, domain.ErrCredentialsRejected)
			},
			wantFailed: domain.ErrCredentialsRejected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, node, tt.params, tt.setup)
			require.NoError(t, err)

			assert.Len(t, out[engine.DefaultPort], tt.wantItems)

			if tt.wantFailed != nil {
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorIs(t, out[engine.ErrorPort][0].Err, tt.wantFailed)
			} else {
				assert.Empty(t, out[engine.ErrorPort])
				assert.True(t, out[engine.DefaultPort][0].JSON["remaining"].(decimal.Decimal).Equal(dec("6")))
			}
		})
	}
}

func TestOrdersStopAtThePageCap(t *testing.T) {
	out, err := run(t, account.NewOrders(nil, nil), map[string]any{"wallet": "w-1"}, func(m mocks) {
		m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-1")).Return(creds, nil)
		m.account.EXPECT().OpenOrders(mock.Anything, creds, mock.Anything).
			Return(ports.Page[domain.Order]{Items: []domain.Order{{ID: "o", Side: domain.SideBuy}}, NextCursor: "more"}, nil).Times(10)
	})
	require.NoError(t, err)
	assert.Len(t, out[engine.DefaultPort], 10)
}

func TestBalance(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(m mocks)
		wantFailed error
	}{
		{
			name: "one item with the balance",
			setup: func(m mocks) {
				m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-1")).Return(creds, nil)
				m.account.EXPECT().CollateralBalance(mock.Anything, creds).Return(dec("12.5"), nil)
			},
		},
		{
			name: "exchange unreachable fails the item",
			setup: func(m mocks) {
				m.wallets.EXPECT().Credentials(mock.Anything, owner, domain.WalletID("w-1")).Return(creds, nil)
				m.account.EXPECT().CollateralBalance(mock.Anything, creds).Return(decimal.Zero, domain.ErrUnavailable)
			},
			wantFailed: domain.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := run(t, account.NewBalance(nil, nil), map[string]any{"wallet": "w-1"}, tt.setup)
			require.NoError(t, err)

			if tt.wantFailed != nil {
				require.Len(t, out[engine.ErrorPort], 1)
				assert.ErrorIs(t, out[engine.ErrorPort][0].Err, tt.wantFailed)

				return
			}

			require.Len(t, out[engine.DefaultPort], 1)
			j := out[engine.DefaultPort][0].JSON
			assert.True(t, j["balance"].(decimal.Decimal).Equal(dec("12.5")))
			assert.Equal(t, string(wallet.Address), j["signer"])
		})
	}
}

func TestWalletParamIsRequired(t *testing.T) {
	r := engine.NewRegistry()
	require.NoError(t, r.RegisterTrigger(stubTrigger{}))
	require.NoError(t, r.RegisterNode(account.NewBalance(nil, nil)))

	err := r.Validate(domain.Workflow{
		Nodes:       []domain.NodeSpec{{ID: "t", Type: "test.trigger"}, {ID: "b", Type: account.BalanceType}},
		Connections: []domain.Connection{{From: "t", FromPort: "main", To: "b", ToPort: "main"}},
	})
	require.ErrorIs(t, err, domain.ErrInvalidWorkflow)
	assert.Contains(t, err.Error(), `missing required param "wallet"`)
}

type stubTrigger struct{}

func (stubTrigger) Type() engine.NodeType {
	return engine.NodeType{Name: "test.trigger", Outputs: []string{engine.DefaultPort}}
}

func (stubTrigger) Start(ctx context.Context, _ engine.TriggerContext, _ engine.Emit) error {
	<-ctx.Done()
	return nil
}
