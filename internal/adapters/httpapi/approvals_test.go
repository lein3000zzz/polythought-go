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
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	httpapimocks "github.com/lein3000zzz/polythought-go/internal/mocks/httpapi"
)

func TestApprovalRoutes(t *testing.T) {
	missing := []domain.Approval{{
		Token: "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB", Spender: "0xE111180000d2663C0091e4f400237545B87B996B",
		Standard: "ERC20", Name: "pUSD to the CTF Exchange",
	}}
	setup := domain.ApprovalSetup{
		WalletID: "w-1", Owner: "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266", Kind: domain.SignatureDepositWallet,
		Granting: 1, State: domain.ApprovalSetupSubmitted, CreatedAt: created, UpdatedAt: created,
	}

	tests := []struct {
		name       string
		req        request
		setup      func(w *httpapimocks.MockWalletService)
		wantStatus int
		wantBody   []string
		check      func(t *testing.T, w *ut.ResponseRecorder)
	}{
		{
			name: "status",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/approvals", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Approvals(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(wallets.ApprovalStatus{
					Missing: missing, CheckedAt: created, Setup: &setup, Way: wallets.SetUpBrowser,
				}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: []string{
				`"ready":false`, `"name":"pUSD to the CTF Exchange"`, `"standard":"ERC20"`, `"way":"browser"`,
				`"setup":{"owner":"0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266","kind":3,"granting":1,"state":"submitted"`,
			},
		},
		{
			name: "ready, nothing to set up here",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/approvals", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Approvals(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(wallets.ApprovalStatus{CheckedAt: created}, nil)
			},
			wantStatus: http.StatusOK,
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.JSONEq(t, `{"ready":true,"missing":[],"checkedAt":"`+created.Format(time.RFC3339)+`","way":"none"}`, w.Body.String())
			},
		},
		{
			name: "Polymarket unreachable",
			req:  request{method: "GET", path: "/api/v1/wallets/w-1/approvals", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().Approvals(mock.Anything, mock.Anything, mock.Anything).Return(wallets.ApprovalStatus{}, domain.ErrUnavailable)
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "with the imported key",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/setup", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetUpApprovals(mock.Anything, mock.Anything, domain.WalletID("w-1")).Return(setup, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"state":"submitted"`},
		},
		{
			name: "an EOA",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/setup", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetUpApprovals(mock.Anything, mock.Anything, mock.Anything).Return(domain.ApprovalSetup{}, domain.ErrEOAApprovals)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"eoa_approvals"`},
		},
		{
			name: "no Builder key",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/setup", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetUpApprovals(mock.Anything, mock.Anything, mock.Anything).Return(domain.ApprovalSetup{}, domain.ErrNoBuilderKey)
			},
			wantStatus: http.StatusForbidden,
			wantBody:   []string{`"code":"builder_key_missing"`},
		},
		{
			name: "being set up",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/setup", headers: cookie},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SetUpApprovals(mock.Anything, mock.Anything, mock.Anything).Return(domain.ApprovalSetup{}, domain.ErrSetupPending)
			},
			wantStatus: http.StatusConflict,
			wantBody:   []string{`"code":"setup_pending"`},
		},
		{
			name: "in the browser: typed data",
			req: request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signing", headers: cookie,
				body: `{"owner":"0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				signing := setup
				signing.State = domain.ApprovalSetupSigning
				w.EXPECT().StartApprovalSetup(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266").
					Return(wallets.ApprovalSigning{Setup: signing, Request: &ports.SigningRequest{TypedData: json.RawMessage(`{"domain":{"chainId":137}}`)}}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"typedData":{"domain":{"chainId":137}}`, `"state":"signing"`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), `"message"`)
			},
		},
		{
			name: "in the browser: a Safe's message",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signing", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartApprovalSetup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(wallets.ApprovalSigning{Setup: setup, Request: &ports.SigningRequest{Message: "0xabcd"}}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"message":"0xabcd"`},
		},
		{
			name: "in the browser: nothing missing",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signing", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				done := setup
				done.State, done.Granting = domain.ApprovalSetupDone, 0
				w.EXPECT().StartApprovalSetup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.ApprovalSigning{Setup: done}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"granting":0`, `"state":"done"`},
			check: func(t *testing.T, w *ut.ResponseRecorder) {
				assert.NotContains(t, w.Body.String(), "typedData")
			},
		},
		{
			name: "in the browser: not the owner",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signing", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartApprovalSetup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.ApprovalSigning{}, domain.ErrNotOwner)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"not_owner"`},
		},
		{
			name: "in the browser: a proxy wallet",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signing", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartApprovalSetup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.ApprovalSigning{}, domain.ErrNeedsImportedKey)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"needs_imported_key"`},
		},
		{
			name: "in the browser: not deployed",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signing", headers: cookie, body: `{"owner":"0x01"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().StartApprovalSetup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(wallets.ApprovalSigning{}, domain.ErrNotDeployed)
			},
			wantStatus: http.StatusBadRequest,
			wantBody:   []string{`"code":"not_deployed"`},
		},
		{
			name: "signed",
			req:  request{method: "POST", path: "/api/v1/wallets/w-1/approvals/signature", headers: cookie, body: `{"signature":"0x5ign"}`},
			setup: func(w *httpapimocks.MockWalletService) {
				w.EXPECT().SignApprovalSetup(mock.Anything, mock.Anything, domain.WalletID("w-1"), "0x5ign").Return(setup, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   []string{`"state":"submitted"`},
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
