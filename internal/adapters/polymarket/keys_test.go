package polymarket

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

// Anvil's first key: its address and the proxy and Deposit Wallet Polymarket derives
// for it (in pkg/clients/polymarket's vectors).
const (
	anvilKey     = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
	anvilAddress = domain.WalletAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
)

func TestKeysAccount(t *testing.T) {
	keys := NewKeys(polymarketmocks.NewMockClobAccountClient(t))
	derived := pm.Production.WalletsOf(common.HexToAddress(string(anvilAddress)))

	for wallet, want := range map[domain.WalletAddress]domain.SignatureType{
		anvilAddress: domain.SignatureEOA,
		domain.WalletAddress(derived.Proxy.Hex()):         domain.SignaturePolyProxy,
		domain.WalletAddress(derived.Safe.Hex()):          domain.SignatureGnosisSafe,
		domain.WalletAddress(derived.DepositBeacon.Hex()): domain.SignatureDepositWallet,
	} {
		signer, typ, err := keys.Account(anvilKey, wallet)
		require.NoError(t, err, wallet)
		assert.Equal(t, anvilAddress, signer, "the key's own address signs")
		assert.Equal(t, want, typ, wallet)
	}

	_, _, err := keys.Account(anvilKey, "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	require.ErrorIs(t, err, domain.ErrKeyNotForWallet)
	assert.ErrorContains(t, err, string(anvilAddress), "saying whose key it is")

	_, _, err = keys.Account("hello", anvilAddress)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
}

func TestKeysAPICredentials(t *testing.T) {
	clob := polymarketmocks.NewMockClobAccountClient(t)
	keys := NewKeys(clob)

	clob.EXPECT().CreateOrDeriveAPIKey(mock.Anything, mock.MatchedBy(func(a pm.L1Auth) bool {
		return a.Signer.Address() == string(anvilAddress) && a.ChainID == pm.Polygon
	})).Return(pm.L2Credentials{Address: string(anvilAddress), APIKey: "key-1", Secret: "s", Passphrase: "p"}, nil).Once()

	creds, err := keys.APICredentials(t.Context(), anvilKey)
	require.NoError(t, err)
	assert.Equal(t, domain.PolymarketCredentials{APIKey: "key-1", Secret: "s", Passphrase: "p", Signer: anvilAddress}, creds)

	clob.EXPECT().CreateOrDeriveAPIKey(mock.Anything, mock.Anything).Return(pm.L2Credentials{}, pm.ErrUnauthorized).Once()

	_, err = keys.APICredentials(t.Context(), anvilKey)
	require.ErrorIs(t, err, domain.ErrCredentialsRejected)
}
