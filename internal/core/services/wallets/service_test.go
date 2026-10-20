package wallets_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

const (
	apiKey     = "4f6d3b52-9a3e-4c8e-8f0a-2b7c1d5e9f01"
	secret     = "c2VjcmV0LWJ5dGVz" // base64url
	passphrase = "pass-phrase"
	walletAddr = domain.WalletAddress("0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed")
	signerAddr = "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359"
)

var (
	now     = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	owner   = domain.User{ID: "u-1", Role: domain.RoleUser}
	session = auth.Principal{User: owner, SessionID: "s-1"}
	token   = auth.Principal{User: owner, TokenID: "t-1"}
	wallet  = domain.Wallet{ID: "w-1", UserID: owner.ID, Address: walletAddr}
	errDB   = errors.New("db down")
)

// fakeBox seals by prefixing the associated data, so tests can check which binding each
// field got, and opening with the wrong binding fails like real AEAD would.
func fakeBox(t *testing.T) *portsmocks.MockSecretBox {
	box := portsmocks.NewMockSecretBox(t)

	box.EXPECT().Seal(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, plain, aad []byte) ([]byte, error) {
			return append(append(bytes.Clone(aad), '|'), plain...), nil
		}).Maybe()

	box.EXPECT().Open(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, sealed, aad []byte) ([]byte, error) {
			plain, ok := bytes.CutPrefix(sealed, append(bytes.Clone(aad), '|'))
			if !ok {
				return nil, errors.New("decrypt failed")
			}

			return plain, nil
		}).Maybe()

	return box
}

// newService verifies every credential successfully; tests about verification use
// newServiceWithVerifier.
func newService(t *testing.T) (*wallets.Service, *portsmocks.MockWalletRepository) {
	t.Helper()

	verifier := portsmocks.NewMockCredentialsVerifier(t)
	verifier.EXPECT().VerifyCredentials(mock.Anything, mock.Anything).Return(nil).Maybe()

	return newServiceWithVerifier(t, verifier)
}

func newServiceWithVerifier(t *testing.T, verifier *portsmocks.MockCredentialsVerifier) (*wallets.Service, *portsmocks.MockWalletRepository) {
	t.Helper()

	repo := portsmocks.NewMockWalletRepository(t)

	return wallets.New(repo, fakeBox(t), verifier, logging.Noop(), wallets.WithClock(func() time.Time { return now })), repo
}

func TestLink(t *testing.T) {
	tests := []struct {
		name    string
		address string
		label   string
		setup   func(r *portsmocks.MockWalletRepository)
		want    domain.Wallet
		wantErr error
	}{
		{
			name: "lowercase address is stored checksummed", address: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", label: "  main ",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().CountForUser(mock.Anything, owner.ID).Return(0, nil)
				r.EXPECT().Create(mock.Anything, mock.MatchedBy(func(w domain.Wallet) bool {
					return w.Address == walletAddr && w.Label == "main" && w.UserID == owner.ID && w.ID != ""
				})).Return(nil)
			},
			want: domain.Wallet{UserID: owner.ID, Address: walletAddr, Label: "main", CreatedAt: now},
		},
		{name: "bad address", address: "0x123", setup: func(*portsmocks.MockWalletRepository) {}, wantErr: domain.ErrInvalidAddress},
		{name: "long label", address: string(walletAddr), label: string(make([]byte, 65)), setup: func(*portsmocks.MockWalletRepository) {}, wantErr: domain.ErrInvalidInput},
		{
			name: "per-user limit", address: string(walletAddr),
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().CountForUser(mock.Anything, owner.ID).Return(20, nil)
			},
			wantErr: domain.ErrInvalidInput,
		},
		{
			name: "already linked", address: string(walletAddr),
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().CountForUser(mock.Anything, owner.ID).Return(1, nil)
				r.EXPECT().Create(mock.Anything, mock.Anything).Return(domain.ErrWalletExists)
			},
			wantErr: domain.ErrWalletExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newService(t)
			tt.setup(repo)

			got, err := svc.Link(context.Background(), token, tt.address, tt.label)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			got.ID = ""
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetCredentials(t *testing.T) {
	valid := wallets.CredentialsInput{APIKey: apiKey, Secret: secret, Passphrase: passphrase, SignatureType: domain.SignatureEOA}

	with := func(f func(*wallets.CredentialsInput)) wallets.CredentialsInput {
		in := valid
		f(&in)

		return in
	}

	tests := []struct {
		name    string
		p       auth.Principal
		in      wallets.CredentialsInput
		setup   func(r *portsmocks.MockWalletRepository)
		check   func(t *testing.T, saved ports.SealedCredentials, info domain.CredentialsInfo)
		wantErr error
	}{
		{
			name: "seals every secret bound to its wallet and field",
			p:    session, in: valid,
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)
			},
			check: func(t *testing.T, saved ports.SealedCredentials, info domain.CredentialsInfo) {
				assert.Equal(t, "polymarket-credentials/w-1/api_key|"+apiKey, string(saved.APIKey))
				assert.Equal(t, "polymarket-credentials/w-1/secret|"+secret, string(saved.Secret))
				assert.Equal(t, "polymarket-credentials/w-1/passphrase|"+passphrase, string(saved.Passphrase))
				assert.Equal(t, "4f6d…9f01", info.APIKeyHint)
				assert.Equal(t, walletAddr, info.Signer, "signer defaults to the wallet")
				assert.Equal(t, now, info.CreatedAt)
			},
		},
		{
			name: "proxy accounts name their signer",
			p:    session,
			in: with(func(in *wallets.CredentialsInput) {
				in.Signer = "0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359"
				in.SignatureType = domain.SignaturePolyProxy
			}),
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)
			},
			check: func(t *testing.T, _ ports.SealedCredentials, info domain.CredentialsInfo) {
				assert.Equal(t, domain.WalletAddress(signerAddr), info.Signer)
				assert.Equal(t, domain.SignaturePolyProxy, info.SignatureType)
			},
		},
		{
			name: "replacing keeps the original creation time",
			p:    session, in: valid,
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().Credentials(mock.Anything, wallet.ID).
					Return(ports.SealedCredentials{Info: domain.CredentialsInfo{CreatedAt: now.Add(-time.Hour)}}, nil)
			},
			check: func(t *testing.T, _ ports.SealedCredentials, info domain.CredentialsInfo) {
				assert.Equal(t, now.Add(-time.Hour), info.CreatedAt)
				assert.Equal(t, now, info.UpdatedAt)
			},
		},
		{name: "not with an api token", p: token, in: valid, setup: func(*portsmocks.MockWalletRepository) {}, wantErr: domain.ErrForbidden},
		{
			name: "someone else's wallet",
			p:    session, in: valid,
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(domain.Wallet{}, domain.ErrNotFound)
			},
			wantErr: domain.ErrNotFound,
		},
	}

	invalid := []struct {
		name string
		in   wallets.CredentialsInput
		err  error
	}{
		{"api key is not a uuid", with(func(in *wallets.CredentialsInput) { in.APIKey = "key" }), domain.ErrInvalidInput},
		{"secret is not base64url", with(func(in *wallets.CredentialsInput) { in.Secret = "not base64!" }), domain.ErrInvalidInput},
		{"missing passphrase", with(func(in *wallets.CredentialsInput) { in.Passphrase = " " }), domain.ErrInvalidInput},
		{"unknown signature type", with(func(in *wallets.CredentialsInput) { in.SignatureType = 7 }), domain.ErrInvalidInput},
		{"bad signer", with(func(in *wallets.CredentialsInput) { in.Signer = "0xnope" }), domain.ErrInvalidAddress},
	}

	for _, iv := range invalid {
		tests = append(tests, struct {
			name    string
			p       auth.Principal
			in      wallets.CredentialsInput
			setup   func(r *portsmocks.MockWalletRepository)
			check   func(t *testing.T, saved ports.SealedCredentials, info domain.CredentialsInfo)
			wantErr error
		}{
			name: iv.name, p: session, in: iv.in, wantErr: iv.err,
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
			},
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newService(t)
			tt.setup(repo)

			var saved ports.SealedCredentials
			if tt.wantErr == nil {
				repo.EXPECT().SaveCredentials(mock.Anything, mock.Anything).
					RunAndReturn(func(_ context.Context, c ports.SealedCredentials) error {
						saved = c
						return nil
					})
			}

			info, err := svc.SetCredentials(context.Background(), tt.p, wallet.ID, tt.in)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, saved.Info, info)
			tt.check(t, saved, info)
		})
	}
}

func TestCredentialsDecrypt(t *testing.T) {
	stored := func(walletID string) ports.SealedCredentials {
		aad := "polymarket-credentials/" + walletID + "/"

		return ports.SealedCredentials{
			Info:       domain.CredentialsInfo{WalletID: wallet.ID, Signer: walletAddr, SignatureType: domain.SignatureEOA},
			APIKey:     []byte(aad + "api_key|" + apiKey),
			Secret:     []byte(aad + "secret|" + secret),
			Passphrase: []byte(aad + "passphrase|" + passphrase),
		}
	}

	tests := []struct {
		name    string
		sealed  ports.SealedCredentials
		wantErr bool
	}{
		{name: "opens with its own binding", sealed: stored("w-1")},
		{name: "ciphertext copied from another wallet fails", sealed: stored("w-2"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newService(t)
			repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
			repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(tt.sealed, nil)

			got, err := svc.Credentials(context.Background(), owner.ID, wallet.ID)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, domain.PolymarketCredentials{
				APIKey: apiKey, Secret: secret, Passphrase: passphrase, Signer: walletAddr, SignatureType: domain.SignatureEOA,
			}, got)
		})
	}
}

func TestScopedOperations(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(r *portsmocks.MockWalletRepository)
		call    func(s *wallets.Service) error
		wantErr error
	}{
		{
			name: "list",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().ListForUser(mock.Anything, owner.ID).Return([]domain.Wallet{wallet}, nil)
			},
			call: func(s *wallets.Service) error {
				_, err := s.List(context.Background(), token)
				return err
			},
		},
		{
			name: "unlink",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().Delete(mock.Anything, owner.ID, wallet.ID).Return(nil)
			},
			call: func(s *wallets.Service) error { return s.Unlink(context.Background(), token, wallet.ID) },
		},
		{
			name: "credentials info",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, nil)
			},
			call: func(s *wallets.Service) error {
				_, err := s.CredentialsInfo(context.Background(), token, wallet.ID)
				return err
			},
		},
		{
			name: "credentials info of a wallet without them",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)
			},
			call: func(s *wallets.Service) error {
				_, err := s.CredentialsInfo(context.Background(), token, wallet.ID)
				return err
			},
			wantErr: domain.ErrNotFound,
		},
		{
			name: "remove credentials",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
				r.EXPECT().DeleteCredentials(mock.Anything, wallet.ID).Return(nil)
			},
			call: func(s *wallets.Service) error { return s.RemoveCredentials(context.Background(), token, wallet.ID) },
		},
		{
			name: "repository errors pass through",
			setup: func(r *portsmocks.MockWalletRepository) {
				r.EXPECT().CountForUser(mock.Anything, owner.ID).Return(0, errDB)
			},
			call: func(s *wallets.Service) error {
				_, err := s.Link(context.Background(), token, string(walletAddr), "")
				return err
			},
			wantErr: errDB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newService(t)
			tt.setup(repo)

			require.ErrorIs(t, tt.call(svc), tt.wantErr)
		})
	}
}

func TestSetCredentialsVerifiesFirst(t *testing.T) {
	valid := wallets.CredentialsInput{APIKey: apiKey, Secret: secret, Passphrase: passphrase, SignatureType: domain.SignatureEOA}

	tests := []struct {
		name      string
		verifyErr error
		wantErr   error
	}{
		{name: "accepted credentials are stored"},
		{name: "rejected credentials are not stored", verifyErr: domain.ErrCredentialsRejected, wantErr: domain.ErrCredentialsRejected},
		{name: "unverifiable credentials are not stored", verifyErr: domain.ErrUnavailable, wantErr: domain.ErrUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verifier := portsmocks.NewMockCredentialsVerifier(t)
			verifier.EXPECT().VerifyCredentials(mock.Anything, domain.PolymarketCredentials{
				APIKey: apiKey, Secret: secret, Passphrase: passphrase, Signer: walletAddr, SignatureType: domain.SignatureEOA,
			}).Return(tt.verifyErr).Once()

			svc, repo := newServiceWithVerifier(t, verifier)
			repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)

			if tt.wantErr == nil {
				repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{}, domain.ErrNotFound)
				repo.EXPECT().SaveCredentials(mock.Anything, mock.Anything).Return(nil).Once()
			}

			_, err := svc.SetCredentials(context.Background(), session, wallet.ID, valid)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestWalletLookupIsScoped(t *testing.T) {
	svc, repo := newService(t)
	repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)

	got, err := svc.Wallet(context.Background(), owner.ID, wallet.ID)
	require.NoError(t, err)
	assert.Equal(t, wallet, got)
}
