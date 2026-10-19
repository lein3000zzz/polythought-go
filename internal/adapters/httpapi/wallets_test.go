package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/httpapi"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

const (
	apiKey = "4f6d3b52-9a3e-4c8e-8f0a-2b7c1d5e9f01"
	secret = "c2VjcmV0LWJ5dGVz"
)

var linked = domain.Wallet{ID: "w-1", UserID: alice.ID, Address: "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed", Label: "main", CreatedAt: created}

func TestWalletRoutes(t *testing.T) {
	info := domain.CredentialsInfo{WalletID: "w-1", APIKeyHint: "4f6d…9f01", Signer: linked.Address, SignatureType: domain.SignatureEOA, CreatedAt: created, UpdatedAt: created}

	tests := []struct {
		name       string
		req        request
		setup      func(w *httpapimocks.MockWalletService)
		wantStatus int
		wantBody   []string
		check      func(t *testing.T, w *ut.ResponseRecorder)
	}{
		{
			name: "list",
			req:  request{method: "GET", path: "/api/v1/wallets", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().List(mock.Anything, mock.Anything).Return([]domain.Wallet{linked}, nil)
				w.EXPECT().TakesKeys().Return(true)
				w.EXPECT().TakesSessionKeys().Return(true)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"address":"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"`, `"hasCredentials":false`, `"hasKey":false`,
				`"hasSessionKey":false`, `"takesKeys":true`, `"takesSessionKeys":true`,
			},
		},
		{
			name: "import a key",
			req: request{method: "PUT", path: "/api/v1/wallets/w-1/key", headers: cookie,
				body: `{"privateKey":"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				withKey := info
				withKey.HasKey = true
				w.EXPECT().ImportKey(mock.Anything, mock.Anything, domain.WalletID("w-1"),
					"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80").Return(withKey, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"hasKey":true`, `"apiKeyHint":"4f6d…9f01"`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), "ac0974", "the key never comes back")
			},
		},
		{
			name: "a key that does not control the wallet",
			req:  request{method: "PUT", path: "/api/v1/wallets/w-1/key", headers: cookie, body: `{"privateKey":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().ImportKey(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0x01").Return(domain.CredentialsInfo{}, domain.ErrKeyNotForWallet)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"key_not_for_wallet"`},
		},
		{
			name: "while the instance takes none",
			req:  request{method: "PUT", path: "/api/v1/wallets/w-1/key", headers: cookie, body: `{"privateKey":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().ImportKey(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0x01").Return(domain.CredentialsInfo{}, domain.ErrKeyImportOff)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"key_import_off"`},
		},
		{
			name: "link",
			req:  request{method: "POST", path: "/api/v1/wallets", body: `{"address":"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed","label":"main"}`, headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Link(mock.Anything, mock.Anything, "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed", "main").Return(linked, nil)
			},
			wantStatus: http.StatusCreated,
			wantBody:   []string{`"id":"w-1"`},
		},
		{
			name: "invalid address",
			req:  request{method: "POST", path: "/api/v1/wallets", body: `{"address":"0x1"}`, headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Link(mock.Anything, mock.Anything, "0x1", "").Return(domain.Wallet{}, domain.ErrInvalidAddress)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"invalid_address"`},
		},
		{
			name: "already linked",
			req:  request{method: "POST", path: "/api/v1/wallets", body: `{"address":"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"}`, headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Link(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.Wallet{}, domain.ErrWalletExists)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"wallet_exists"`},
		},
		{
			name: "unlink",
			req:  request{method: "DELETE", path: "/api/v1/wallets/w-1", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Unlink(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "set credentials maps every field and echoes no secret",
			req: request{method: "PUT", path: "/api/v1/wallets/w-1/credentials", headers: cookie,
				body: `{"apiKey":"` + apiKey + `","secret":"` + secret + `","passphrase":"pp","signer":"0xabc","signatureType":1}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetCredentials(mock.Anything, mock.Anything, domain.WalletID("w-1"), wallets.CredentialsInput{
					APIKey: apiKey, Secret: secret, Passphrase: "pp", Signer: "0xabc", SignatureType: domain.SignaturePolyProxy,
				}).Return(info, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"apiKeyHint":"4f6d…9f01"`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				for _, leak := range []string{apiKey, secret, `"pp"`} {
					assert.NotContains(t, w.Body.String(), leak)
				}
			},
		},
		{
			name: "credentials info shows only a hint",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/credentials", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().CredentialsInfo(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(info, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"apiKeyHint":"4f6d…9f01"`, `"signatureType":0`},
		},
		{
			name: "no credentials stored",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/credentials", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().CredentialsInfo(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(domain.CredentialsInfo{}, domain.ErrNotFound)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "remove credentials",
			req:  request{method: "DELETE", path: "/api/v1/wallets/w-1/credentials", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().RemoveCredentials(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "credentials polymarket refuses",
			req: request{method: "PUT", path: "/api/v1/wallets/w-1/credentials", headers: cookie,
				body: `{"apiKey":"` + apiKey + `","secret":"` + secret + `","passphrase":"pp","signatureType":0}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetCredentials(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.CredentialsInfo{}, domain.ErrCredentialsRejected)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"credentials_rejected"`},
		},
		{
			name: "polymarket unreachable while verifying",
			req: request{method: "PUT", path: "/api/v1/wallets/w-1/credentials", headers: cookie,
				body: `{"apiKey":"` + apiKey + `","secret":"` + secret + `","passphrase":"pp","signatureType":0}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetCredentials(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.CredentialsInfo{}, domain.ErrUnavailable)
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   []string{`"code":"upstream_unavailable"`},
		},
		{
			name: "a session key's credentials stay",
			req:  request{method: "DELETE", path: "/api/v1/wallets/w-1/credentials", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().RemoveCredentials(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(domain.ErrSessionKeyInUse)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"session_key_in_use"`},
		},
		{
			name:       "unknown credential fields are rejected",
			req:        request{method: "PUT", path: "/api/v1/wallets/w-1/credentials", body: `{"privateKey":"0x..."}`, headers: cookie},
			setup:      func(*httpapimocks.MockWalletService) {},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authSvc := httpapimocks.NewMockAuthService(t)
			withSession(authSvc, alice)

			walletSvc := httpapimocks.NewMockWalletService(t)
			tt.setup(walletSvc)

			srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(), httpapi.Services{Auth: authSvc, Wallets: walletSvc})
			require.NoError(t, err)

			w := tt.req.do(t, srv)

			assert.Equal(t, tt.wantStatus, w.Code, w.Body.String())

			for _, s := range tt.wantBody {
				assert.Contains(t, w.Body.String(), s)
			}

			if tt.check != nil {
				tt.check(t, w)
			}
		})
	}
}

func TestWalletRoutesNeedAuthentication(t *testing.T) {
	srv, err := httpapi.New(httpapi.Config{PublicURL: publicURL}, observability.NoopUtility(),
		httpapi.Services{Auth: httpapimocks.NewMockAuthService(t), Wallets: httpapimocks.NewMockWalletService(t)})
	require.NoError(t, err)

	w := request{method: "GET", path: "/api/v1/wallets"}.do(t, srv)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
