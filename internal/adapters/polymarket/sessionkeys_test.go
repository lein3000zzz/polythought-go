package polymarket

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	polymarketmocks "github.com/lein3000zzz/polythought-go/internal/mocks/polymarket"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

var testBuilder = pm.BuilderCredentials{Key: "builder-key", Secret: "c2VjcmV0", Passphrase: "pass"}

const sessionSigner = domain.WalletAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

func newSessionKeys(t *testing.T, builder bool) (*SessionKeys, *polymarketmocks.MockRelayerClient) {
	t.Helper()

	relayer := polymarketmocks.NewMockRelayerClient(t)
	s := NewSessionKeys(relayer, func() (pm.BuilderCredentials, bool) { return testBuilder, builder })
	s.now = func() time.Time { return time.Unix(1760875200, 300_000_000) }

	return s, relayer
}

func depositWallet() domain.WalletAddress {
	return domain.WalletAddress(pm.Production.WalletsOf(common.HexToAddress(string(anvilAddress))).DepositBeacon.Hex())
}

func TestSessionKeysIsDepositWallet(t *testing.T) {
	s, _ := newSessionKeys(t, true)
	derived := pm.Production.WalletsOf(common.HexToAddress(string(anvilAddress)))

	assert.True(t, s.IsDepositWallet(anvilAddress, domain.WalletAddress(derived.DepositBeacon.Hex())))
	assert.True(t, s.IsDepositWallet(anvilAddress, domain.WalletAddress(derived.DepositUUPS.Hex())))

	for _, w := range []common.Address{common.HexToAddress(string(anvilAddress)), derived.Proxy, derived.Safe} {
		assert.False(t, s.IsDepositWallet(anvilAddress, domain.WalletAddress(w.Hex())), "not a Deposit Wallet: %s", w)
	}

	assert.False(t, s.IsDepositWallet(sessionSigner, depositWallet()), "someone else's")
}

func TestSessionKeysNewKey(t *testing.T) {
	s, _ := newSessionKeys(t, true)

	key, signer, err := s.NewKey()
	require.NoError(t, err)

	k, err := crypto.HexToECDSA(key[2:])
	require.NoError(t, err)
	assert.Equal(t, string(signer), crypto.PubkeyToAddress(k.PublicKey).Hex())
}

func TestSessionKeysBatches(t *testing.T) {
	s, relayer := newSessionKeys(t, true)
	relayer.EXPECT().ExecuteParams(mock.Anything, testBuilder, string(anvilAddress), pm.RelayerDepositWallet).
		Return(pm.ExecuteParams{Address: "0x1111111111111111111111111111111111111111", Nonce: "7"}, nil).Times(2)

	b, err := s.Authorization(t.Context(), anvilAddress, depositWallet(), sessionSigner)
	require.NoError(t, err)
	assert.Equal(t, ports.SessionKeyBatch{
		Wallet: depositWallet(), Owner: anvilAddress, Signer: sessionSigner, Nonce: "7",
		ValidUntil: time.Unix(1776409200, 0).UTC(), Deadline: time.Unix(1760875800, 0).UTC(),
	}, b)

	// The owner's signature is the official client's for the same batch (in the vectors).
	key, err := crypto.HexToECDSA(anvilKey[2:])
	require.NoError(t, err)

	digest, err := toBatch(b).Digest(pm.Polygon)
	require.NoError(t, err)
	assert.Equal(t, "0xef5611587df2a080b8fbc0587fa44ca8fc56488df0f6d587bf27806cd1839b81", hexutil.Encode(digest))

	sig, err := crypto.Sign(digest, key)
	require.NoError(t, err)
	sig[64] += 27

	signer, err := s.Signer(b, hexutil.Encode(sig))
	require.NoError(t, err)
	assert.Equal(t, anvilAddress, signer)

	_, err = s.Signer(b, "0x1234")
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	r, err := s.Revocation(t.Context(), anvilAddress, depositWallet(), sessionSigner)
	require.NoError(t, err)
	assert.True(t, r.Revoke)
	assert.True(t, r.ValidUntil.IsZero())

	digest, err = toBatch(r).Digest(pm.Polygon)
	require.NoError(t, err)
	assert.Equal(t, "0x1552db18d8985235e0253094c1d50c0eb92e5b806ec900db24b65376c2950b12", hexutil.Encode(digest))

	raw, err := s.TypedData(r)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"primaryType":"Batch"`)
}

func TestSessionKeysWithoutBuilderKey(t *testing.T) {
	s, _ := newSessionKeys(t, false)
	assert.False(t, s.Available())

	_, err := s.Authorization(t.Context(), anvilAddress, depositWallet(), sessionSigner)
	require.ErrorIs(t, err, domain.ErrSessionKeysOff)

	_, err = s.Submit(t.Context(), ports.SessionKeyBatch{}, "0x")
	require.ErrorIs(t, err, domain.ErrSessionKeysOff)

	_, err = s.Confirmed(t.Context(), "tx-1")
	require.ErrorIs(t, err, domain.ErrSessionKeysOff)
}

func TestSessionKeysSubmit(t *testing.T) {
	b := ports.SessionKeyBatch{
		Wallet: depositWallet(), Owner: anvilAddress, Signer: sessionSigner, Nonce: "7",
		ValidUntil: time.Unix(1776409200, 0), Deadline: time.Unix(1760875800, 0),
	}

	t.Run("an authorisation, for the CLOB only", func(t *testing.T) {
		s, relayer := newSessionKeys(t, true)

		var sent pm.SessionSignerRequest

		relayer.EXPECT().AuthorizeSessionSigner(mock.Anything, testBuilder, mock.Anything).
			RunAndReturn(func(_ context.Context, _ pm.BuilderCredentials, req pm.SessionSignerRequest) (pm.RelayerOperation, error) {
				sent = req
				return pm.RelayerOperation{Status: "SUBMITTED", TransactionID: "tx-1"}, nil
			})

		id, err := s.Submit(t.Context(), b, "0xsig")
		require.NoError(t, err)
		assert.Equal(t, "tx-1", id)

		assert.Equal(t, []string{"CLOB"}, sent.Scopes)
		assert.Equal(t, int64(1776409200), sent.ValidUntil)
		assert.Equal(t, int64(1760875800), sent.Deadline)
		assert.Equal(t, "0xsig", sent.Signature)
		assert.Equal(t, idempotencyKey(b), sent.IdempotencyKey)
	})

	t.Run("a revocation", func(t *testing.T) {
		s, relayer := newSessionKeys(t, true)
		r := b
		r.Revoke, r.ValidUntil = true, time.Time{}

		relayer.EXPECT().RevokeSessionSigner(mock.Anything, testBuilder, mock.MatchedBy(func(req pm.SessionSignerRequest) bool {
			return req.Scopes == nil && req.ValidUntil == 0 && req.IdempotencyKey == idempotencyKey(r)
		})).Return(pm.RelayerOperation{Status: "PENDING", TransactionID: "tx-2"}, nil)

		id, err := s.Submit(t.Context(), r, "0xsig")
		require.NoError(t, err)
		assert.Equal(t, "tx-2", id)
	})

	t.Run("the same batch, the same idempotency key", func(t *testing.T) {
		other := b
		other.Nonce = "8"
		revoke := b
		revoke.Revoke = true

		assert.Equal(t, idempotencyKey(b), idempotencyKey(b))
		assert.NotEqual(t, idempotencyKey(b), idempotencyKey(other))
		assert.NotEqual(t, idempotencyKey(b), idempotencyKey(revoke))
	})

	errs := []struct {
		name string
		err  error
		want error
	}{
		{"refused", fmt.Errorf("%w: FAILED", pm.ErrRelayerRefused), domain.ErrRelayerRefused},
		{"a builder key refused", pm.ErrUnauthorized, domain.ErrRelayerRefused},
		{"a bad request", &pm.APIError{StatusCode: 400, Body: "invalid signature"}, domain.ErrRelayerRefused},
		{"too many", &pm.APIError{StatusCode: 429}, domain.ErrUnavailable},
		{"down", &pm.APIError{StatusCode: 502}, domain.ErrUnavailable},
		{"unreachable", errors.New("dial tcp: refused"), domain.ErrUnavailable},
	}

	for _, tt := range errs {
		t.Run(tt.name, func(t *testing.T) {
			s, relayer := newSessionKeys(t, true)
			relayer.EXPECT().AuthorizeSessionSigner(mock.Anything, testBuilder, mock.Anything).Return(pm.RelayerOperation{}, tt.err)

			_, err := s.Submit(t.Context(), b, "0xsig")
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestSessionKeysConfirmed(t *testing.T) {
	tests := []struct {
		tx      pm.RelayerTransaction
		done    bool
		wantErr string
	}{
		{pm.RelayerTransaction{State: "STATE_MINED"}, false, ""},
		{pm.RelayerTransaction{State: pm.TransactionConfirmed, Hash: "0xhash"}, true, ""},
		{pm.RelayerTransaction{State: pm.TransactionFailed, Error: "execution reverted"}, false, "STATE_FAILED: execution reverted"},
		{pm.RelayerTransaction{State: pm.TransactionInvalid}, false, "STATE_INVALID"},
	}

	for _, tt := range tests {
		t.Run(tt.tx.State, func(t *testing.T) {
			s, relayer := newSessionKeys(t, true)
			relayer.EXPECT().Transaction(mock.Anything, testBuilder, "tx-1").Return(tt.tx, nil)

			done, err := s.Confirmed(t.Context(), "tx-1")
			assert.Equal(t, tt.done, done)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			require.ErrorIs(t, err, domain.ErrRelayerRefused)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
