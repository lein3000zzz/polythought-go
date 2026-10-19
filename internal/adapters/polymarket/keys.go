package polymarket

import (
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var _ ports.TradingKeys = (*Keys)(nil)

// Keys serves ports.TradingKeys: Polymarket's wallet derivation, and the CLOB's L1
// endpoints for API keys.
type Keys struct {
	clob       pm.ClobAccountClient
	derivation pm.Derivation
}

func NewKeys(clob pm.ClobAccountClient) *Keys {
	return &Keys{clob: clob, derivation: pm.Production}
}

func (k *Keys) Account(key string, wallet domain.WalletAddress) (domain.WalletAddress, domain.SignatureType, error) {
	signer, err := pm.NewOrderSigner(key, false)
	if err != nil {
		return "", 0, fmt.Errorf("%w: not a private key: 64 hex digits, 0x or not", domain.ErrInvalidInput)
	}

	typ, ok := k.derivation.Classify(common.HexToAddress(signer.Address()), common.HexToAddress(string(wallet)))
	if !ok {
		return "", 0, fmt.Errorf("%w: its address is %s, and %s is not its own, its proxy, its Safe or its Deposit Wallet",
			domain.ErrKeyNotForWallet, signer.Address(), wallet)
	}

	return domain.WalletAddress(signer.Address()), domain.SignatureType(typ), nil
}

func (k *Keys) APICredentials(ctx context.Context, key string) (domain.PolymarketCredentials, error) {
	signer, err := pm.NewOrderSigner(key, false)
	if err != nil {
		return domain.PolymarketCredentials{}, fmt.Errorf("%w: not a private key", domain.ErrInvalidInput)
	}

	creds, err := k.clob.CreateOrDeriveAPIKey(ctx, pm.L1Auth{Signer: signer, ChainID: pm.Polygon})
	if err != nil {
		return domain.PolymarketCredentials{}, accountError(err)
	}

	return domain.PolymarketCredentials{
		APIKey: creds.APIKey, Secret: creds.Secret, Passphrase: creds.Passphrase,
		Signer: domain.WalletAddress(signer.Address()),
	}, nil
}
