package httpapi_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

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

func TestSessionKeyRoutes(t *testing.T) {
	key := domain.SessionKey{
		WalletID: "w-1", Signer: "0x70997970C51812dc3A010C7d01b50e0d17dc79C8", Owner: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266",
		State: domain.SessionKeySigning, ValidUntil: created.Add(4315 * time.Hour), CreatedAt: created, UpdatedAt: created,
	}
	active := key
	active.State = domain.SessionKeyActive
	failed := key
	failed.State, failed.Error = domain.SessionKeyFailed, "not confirmed in 15 minutes"

	signing := wallets.SessionKeySigning{Key: key, TypedData: json.RawMessage(`{"primaryType":"Batch","domain":{"chainId":137}}`)}

	tests := []struct {
		name       string
		req        request
		setup      func(w *httpapimocks.MockWalletService)
		wantStatus int
		wantBody   []string
		check      func(t *testing.T, w *ut.ResponseRecorder)
	}{
		{
			name: "show",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/session-key", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SessionKeys(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(wallets.SessionKeys{Active: &active, Pending: &failed}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"active":{"signer":"0x70997970C51812dc3A010C7d01b50e0d17dc79C8"`, `"state":"active"`,
				`"pending":{`, `"state":"failed"`, `"error":"not confirmed in 15 minutes"`,
			},
		},
		{
			name: "none",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/session-key", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SessionKeys(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(wallets.SessionKeys{}, nil)
			},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.JSONEq(t, `{}`, w.Body.String())
			},
		},
		{
			name: "start: the typed data, chainId a number still",
			req: request{method: "POST", path: "/api/v1/wallets/w-1/session-key", headers: cookie,
				body: `{"owner":"0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartSessionKey(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266").Return(signing, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"typedData":{"domain":{"chainId":137},"primaryType":"Batch"}`, `"key":{"signer":`, `"state":"signing"`},
		},
		{
			name: "start: not a Deposit Wallet",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartSessionKey(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0x01").Return(wallets.SessionKeySigning{}, domain.ErrNotDepositWallet)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"not_deposit_wallet"`},
		},
		{
			name: "start: no Builder key",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartSessionKey(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.SessionKeySigning{}, domain.ErrSessionKeysOff)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"session_keys_off"`},
		},
		{
			name: "start: one being authorised",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartSessionKey(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.SessionKeySigning{}, domain.ErrSessionKeyPending)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"session_key_pending"`},
		},
		{
			name: "sign",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				authorizing := key
				authorizing.State = domain.SessionKeyAuthorizing
				w.EXPECT().SignSessionKey(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0x5ign").Return(authorizing, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"state":"authorizing"`},
		},
		{
			name: "sign: someone else's",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SignSessionKey(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.SessionKey{}, domain.ErrWrongSigner)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"wrong_signer"`},
		},
		{
			name: "sign: too late",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SignSessionKey(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.SessionKey{}, domain.ErrSignatureExpired)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"signature_expired"`},
		},
		{
			name: "sign: the Relayer refuses",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SignSessionKey(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(domain.SessionKey{}, domain.ErrRelayerRefused)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"relayer_refused"`},
		},
		{
			name: "start revoking",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/revocation", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartRevocation(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(signing, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"typedData":{`},
		},
		{
			name: "revoke",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/revocation/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().RevokeSessionKey(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0x5ign").Return(wallets.Revocation{Revoked: true}, nil)
			},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.JSONEq(t, `{"revoked":true}`, w.Body.String())
			},
		},
		{
			name: "revoke: removed, not revoked",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/session-key/revocation/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().RevokeSessionKey(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.Revocation{Problem: "upstream service unavailable"}, nil)
			},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.JSONEq(t, `{"revoked":false,"problem":"upstream service unavailable"}`, w.Body.String())
			},
		},
		{
			name: "remove without revoking",
			req:  request{method: "DELETE", path: "/api/v1/wallets/w-1/session-key", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().RemoveSessionKey(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(nil)
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "unknown fields are rejected",
			req:        request{method: "POST", path: "/api/v1/wallets/w-1/session-key", headers: cookie, body: `{"owner":"0x01","privateKey":"0x02"}`},
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
