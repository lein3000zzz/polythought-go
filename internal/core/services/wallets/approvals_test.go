package wallets_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/wallets"
	portsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/ports"
)

type approvalMocks struct {
	repo      *portsmocks.MockWalletRepository
	setups    *portsmocks.MockApprovalSetupRepository
	approvals *portsmocks.MockTradingApprovals
}

func approvalService(t *testing.T, builder bool) (*wallets.Service, approvalMocks, *clock) {
	t.Helper()

	m := approvalMocks{
		repo:      portsmocks.NewMockWalletRepository(t),
		setups:    portsmocks.NewMockApprovalSetupRepository(t),
		approvals: portsmocks.NewMockTradingApprovals(t),
	}
	m.approvals.EXPECT().Available().Return(builder).Maybe()

	c := &clock{at: now}
	svc := wallets.New(m.repo, fakeBox(t), portsmocks.NewMockCredentialsVerifier(t), logging.Noop(),
		wallets.WithClock(c.now), wallets.WithApprovals(m.setups, m.approvals))

	return svc, m, c
}

var (
	pusdToExchange   = domain.Approval{Token: "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB", Spender: "0xE111180000d2663C0091e4f400237545B87B996B", Standard: "ERC20", Name: "pUSD to the CTF Exchange"}
	tokensToExchange = domain.Approval{Token: "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045", Spender: "0xE111180000d2663C0091e4f400237545B87B996B", Standard: "ERC1155", Name: "Outcome tokens to the CTF Exchange"}
	missingTwo       = []domain.Approval{pusdToExchange, tokensToExchange}
)

func depositBatch() ports.ApprovalBatch {
	return ports.ApprovalBatch{
		Kind: domain.SignatureDepositWallet, Wallet: walletAddr, Owner: ownerAddr, Approvals: missingTwo,
		Nonce: "7", Deadline: now.Add(10 * time.Minute),
	}
}

func storedSetup(state domain.ApprovalSetupState) ports.StoredApprovalSetup {
	b := depositBatch()

	st := ports.StoredApprovalSetup{
		Info: domain.ApprovalSetup{
			WalletID: wallet.ID, Owner: ownerAddr, Kind: domain.SignatureDepositWallet, Granting: 2,
			State: state, CreatedAt: now, UpdatedAt: now,
		},
		Approvals: b.Approvals, Nonce: b.Nonce, Deadline: b.Deadline,
	}

	if state == domain.ApprovalSetupSubmitted {
		st.TransactionID = "tx-1"
	}

	return st
}

func TestApprovalStatus(t *testing.T) {
	checked := now.Add(-time.Minute)

	tests := []struct {
		name    string
		wallet  domain.Wallet
		builder bool
		creds   domain.CredentialsInfo
		want    wallets.SetUpWay
		why     error
	}{
		{"no Builder key", wallet, false, domain.CredentialsInfo{}, wallets.SetUpNot, domain.ErrNoBuilderKey},
		{"no credentials: in the browser", wallet, true, domain.CredentialsInfo{}, wallets.SetUpBrowser, nil},
		{"an imported key", withCreds(true, false), true, domain.CredentialsInfo{SignatureType: domain.SignatureGnosisSafe}, wallets.SetUpKey, nil},
		{"a session key: its owner in the browser", withCreds(true, true), true, domain.CredentialsInfo{SignatureType: domain.SignatureDepositWallet}, wallets.SetUpBrowser, nil},
		{"an EOA: not here (D162)", withCreds(true, false), true, domain.CredentialsInfo{SignatureType: domain.SignatureEOA}, wallets.SetUpNot, domain.ErrEOAApprovals},
		{"a proxy wallet without a key: not here (D161)", withCreds(false, false), true, domain.CredentialsInfo{SignatureType: domain.SignaturePolyProxy}, wallets.SetUpNot, domain.ErrNeedsImportedKey},
		{"pasted credentials of a Safe: in the browser", withCreds(false, false), true, domain.CredentialsInfo{SignatureType: domain.SignatureGnosisSafe}, wallets.SetUpBrowser, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, m, _ := approvalService(t, tt.builder)

			m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(tt.wallet, nil)
			m.approvals.EXPECT().Status(mock.Anything, walletAddr).Return(missingTwo, checked, nil)
			m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(storedSetup(domain.ApprovalSetupDone), nil)
			if tt.wallet.HasCredentials && tt.builder {
				m.repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{Info: tt.creds}, nil)
			}

			got, err := svc.Approvals(t.Context(), token, wallet.ID)
			require.NoError(t, err)
			assert.Equal(t, missingTwo, got.Missing)
			assert.Equal(t, checked, got.CheckedAt)
			require.NotNil(t, got.Setup)
			assert.Equal(t, domain.ApprovalSetupDone, got.Setup.State)
			assert.Equal(t, tt.want, got.Way)
			if tt.why == nil {
				assert.Empty(t, got.Why)
			} else {
				assert.Equal(t, tt.why.Error(), got.Why)
			}
		})
	}
}

func withCreds(key, session bool) domain.Wallet {
	w := wallet
	w.HasCredentials, w.HasKey, w.HasSessionKey = true, key, session

	return w
}

func TestSetUpApprovalsWithTheImportedKey(t *testing.T) {
	keyed := withCreds(true, false)
	sealedKey := func(m approvalMocks, typ domain.SignatureType) {
		m.repo.EXPECT().Credentials(mock.Anything, wallet.ID).Return(ports.SealedCredentials{
			Info:       domain.CredentialsInfo{Signer: ownerAddr, SignatureType: typ},
			PrivateKey: []byte("polymarket-credentials/w-1/private_key|" + privateKey),
		}, nil)
	}

	t.Run("signed by the server and submitted", func(t *testing.T) {
		svc, m, _ := approvalService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(keyed, nil)
		sealedKey(m, domain.SignatureDepositWallet)
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(ports.StoredApprovalSetup{}, domain.ErrNotFound)
		m.approvals.EXPECT().Missing(mock.Anything, walletAddr).Return(missingTwo, nil)
		m.approvals.EXPECT().Deployed(mock.Anything, walletAddr, domain.SignatureDepositWallet).Return(true, nil)
		m.approvals.EXPECT().Prepare(mock.Anything, domain.SignatureDepositWallet, ownerAddr, walletAddr, missingTwo).Return(depositBatch(), nil)
		m.approvals.EXPECT().Sign(depositBatch(), privateKey).Return(signature, nil)
		m.approvals.EXPECT().Submit(mock.Anything, depositBatch(), signature).Return("tx-1", nil)
		m.setups.EXPECT().SaveApprovalSetup(mock.Anything, storedSetup(domain.ApprovalSetupSubmitted)).Return(nil)

		got, err := svc.SetUpApprovals(t.Context(), session, wallet.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ApprovalSetupSubmitted, got.State)
		assert.Equal(t, 2, got.Granting)
	})

	t.Run("nothing missing: nothing submitted", func(t *testing.T) {
		svc, m, _ := approvalService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(keyed, nil)
		sealedKey(m, domain.SignatureGnosisSafe)
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(ports.StoredApprovalSetup{}, domain.ErrNotFound)
		m.approvals.EXPECT().Missing(mock.Anything, walletAddr).Return(nil, nil)

		got, err := svc.SetUpApprovals(t.Context(), session, wallet.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ApprovalSetupDone, got.State)
		assert.Zero(t, got.Granting)
	})

	t.Run("refused", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			w      domain.Wallet
			typ    domain.SignatureType
			wantFn func(m approvalMocks)
			want   error
		}{
			{"no key", wallet, 0, nil, domain.ErrNotFound},
			{"a session key", withCreds(true, true), 0, nil, domain.ErrNotFound},
			{"an EOA", keyed, domain.SignatureEOA, nil, domain.ErrEOAApprovals},
			{"not deployed", keyed, domain.SignatureGnosisSafe, func(m approvalMocks) {
				m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(ports.StoredApprovalSetup{}, domain.ErrNotFound)
				m.approvals.EXPECT().Missing(mock.Anything, walletAddr).Return(missingTwo, nil)
				m.approvals.EXPECT().Deployed(mock.Anything, walletAddr, domain.SignatureGnosisSafe).Return(false, nil)
			}, domain.ErrNotDeployed},
			{"one being set up", keyed, domain.SignatureGnosisSafe, func(m approvalMocks) {
				m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(storedSetup(domain.ApprovalSetupSubmitted), nil)
			}, domain.ErrSetupPending},
		} {
			t.Run(tc.name, func(t *testing.T) {
				svc, m, _ := approvalService(t, true)
				m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(tc.w, nil)
				if tc.w.HasKey && !tc.w.HasSessionKey {
					sealedKey(m, tc.typ)
				}
				if tc.wantFn != nil {
					tc.wantFn(m)
				}

				_, err := svc.SetUpApprovals(t.Context(), session, wallet.ID)
				require.ErrorIs(t, err, tc.want)
			})
		}
	})

	t.Run("from a browser session, with a Builder key", func(t *testing.T) {
		svc, _, _ := approvalService(t, true)
		_, err := svc.SetUpApprovals(t.Context(), token, wallet.ID)
		require.ErrorIs(t, err, domain.ErrForbidden)

		off, _, _ := approvalService(t, false)
		_, err = off.SetUpApprovals(t.Context(), session, wallet.ID)
		require.ErrorIs(t, err, domain.ErrNoBuilderKey)
	})

	t.Run("refused by the Relayer: failed", func(t *testing.T) {
		svc, m, _ := approvalService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(keyed, nil)
		sealedKey(m, domain.SignatureDepositWallet)
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(ports.StoredApprovalSetup{}, domain.ErrNotFound)
		m.approvals.EXPECT().Missing(mock.Anything, walletAddr).Return(missingTwo, nil)
		m.approvals.EXPECT().Deployed(mock.Anything, walletAddr, domain.SignatureDepositWallet).Return(true, nil)
		m.approvals.EXPECT().Prepare(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(depositBatch(), nil)
		m.approvals.EXPECT().Sign(depositBatch(), privateKey).Return(signature, nil)
		m.approvals.EXPECT().Submit(mock.Anything, depositBatch(), signature).Return("", domain.ErrRelayerRefused)
		m.setups.EXPECT().SaveApprovalSetup(mock.Anything, mock.MatchedBy(func(st ports.StoredApprovalSetup) bool {
			return st.Info.State == domain.ApprovalSetupFailed && st.Info.Error == domain.ErrRelayerRefused.Error()
		})).Return(nil)

		_, err := svc.SetUpApprovals(t.Context(), session, wallet.ID)
		require.ErrorIs(t, err, domain.ErrRelayerRefused)
	})
}

func TestSetUpApprovalsInTheBrowser(t *testing.T) {
	request := ports.SigningRequest{TypedData: json.RawMessage(`{"primaryType":"Batch"}`)}

	t.Run("signed by the owner", func(t *testing.T) {
		svc, m, c := approvalService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil).Times(2)
		m.approvals.EXPECT().Kind(ownerAddr, walletAddr).Return(domain.SignatureDepositWallet, true)
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(ports.StoredApprovalSetup{}, domain.ErrNotFound).Once()
		m.approvals.EXPECT().Missing(mock.Anything, walletAddr).Return(missingTwo, nil)
		m.approvals.EXPECT().Deployed(mock.Anything, walletAddr, domain.SignatureDepositWallet).Return(true, nil)
		m.approvals.EXPECT().Prepare(mock.Anything, domain.SignatureDepositWallet, ownerAddr, walletAddr, missingTwo).Return(depositBatch(), nil)
		m.approvals.EXPECT().SigningRequest(depositBatch()).Return(request, nil)
		m.setups.EXPECT().SaveApprovalSetup(mock.Anything, storedSetup(domain.ApprovalSetupSigning)).Return(nil)

		got, err := svc.StartApprovalSetup(t.Context(), session, wallet.ID, "0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266")
		require.NoError(t, err)
		require.NotNil(t, got.Request)
		assert.Equal(t, request, *got.Request)

		c.set(now.Add(time.Minute))
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(storedSetup(domain.ApprovalSetupSigning), nil).Once()
		m.approvals.EXPECT().Signer(depositBatch(), signature).Return(ownerAddr, nil)
		m.approvals.EXPECT().Submit(mock.Anything, depositBatch(), signature).Return("tx-1", nil)

		want := storedSetup(domain.ApprovalSetupSubmitted)
		want.Info.UpdatedAt = now.Add(time.Minute)
		m.setups.EXPECT().SaveApprovalSetup(mock.Anything, want).Return(nil)

		setup, err := svc.SignApprovalSetup(t.Context(), session, wallet.ID, " "+signature+" ")
		require.NoError(t, err)
		assert.Equal(t, domain.ApprovalSetupSubmitted, setup.State)
	})

	t.Run("not for wallets the account does not control, EOAs or proxy wallets", func(t *testing.T) {
		for kind, want := range map[domain.SignatureType]error{
			-1:                        domain.ErrNotOwner,
			domain.SignatureEOA:       domain.ErrEOAApprovals,
			domain.SignaturePolyProxy: domain.ErrNeedsImportedKey,
		} {
			svc, m, _ := approvalService(t, true)
			m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
			m.approvals.EXPECT().Kind(ownerAddr, walletAddr).Return(kind, kind >= 0)

			_, err := svc.StartApprovalSetup(t.Context(), session, wallet.ID, string(ownerAddr))
			require.ErrorIs(t, err, want)
		}
	})

	signing := func(m approvalMocks) {
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(storedSetup(domain.ApprovalSetupSigning), nil)
	}

	t.Run("someone else's signature", func(t *testing.T) {
		svc, m, _ := approvalService(t, true)
		signing(m)
		m.approvals.EXPECT().Signer(depositBatch(), signature).Return(keySigner, nil)

		_, err := svc.SignApprovalSetup(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrWrongSigner)
	})

	t.Run("too late", func(t *testing.T) {
		svc, m, c := approvalService(t, true)
		signing(m)
		c.set(now.Add(11 * time.Minute))

		_, err := svc.SignApprovalSetup(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrSignatureExpired)
	})

	t.Run("nothing to sign", func(t *testing.T) {
		svc, m, _ := approvalService(t, true)
		m.repo.EXPECT().Get(mock.Anything, owner.ID, wallet.ID).Return(wallet, nil)
		m.setups.EXPECT().ApprovalSetup(mock.Anything, wallet.ID).Return(storedSetup(domain.ApprovalSetupDone), nil)

		_, err := svc.SignApprovalSetup(t.Context(), session, wallet.ID, signature)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestConfirmApprovalSetups(t *testing.T) {
	t.Run("confirmed, failed, pending, late", func(t *testing.T) {
		svc, m, c := approvalService(t, true)
		c.set(now.Add(5 * time.Minute))

		ids := map[string]domain.WalletID{"tx-done": "w-1", "tx-failed": "w-2", "tx-pending": "w-3", "tx-late": "w-4"}
		var setups []ports.StoredApprovalSetup
		for _, tx := range []string{"tx-done", "tx-failed", "tx-pending", "tx-late"} {
			st := storedSetup(domain.ApprovalSetupSubmitted)
			st.TransactionID, st.Info.WalletID = tx, ids[tx]
			if tx == "tx-late" {
				st.Info.UpdatedAt = now.Add(-11 * time.Minute)
			}
			setups = append(setups, st)
		}

		m.setups.EXPECT().SubmittedApprovalSetups(mock.Anything).Return(setups, nil)
		m.approvals.EXPECT().Confirmed(mock.Anything, "tx-done").Return(true, nil)
		m.approvals.EXPECT().Confirmed(mock.Anything, "tx-failed").Return(false, domain.ErrRelayerRefused)
		m.approvals.EXPECT().Confirmed(mock.Anything, "tx-pending").Return(false, nil)
		m.approvals.EXPECT().Confirmed(mock.Anything, "tx-late").Return(false, domain.ErrUnavailable)

		saved := map[domain.WalletID]ports.StoredApprovalSetup{}
		m.setups.EXPECT().SaveApprovalSetup(mock.Anything, mock.Anything).RunAndReturn(func(_ context.Context, st ports.StoredApprovalSetup) error {
			saved[st.Info.WalletID] = st
			return nil
		})

		svc.ConfirmApprovalSetups(t.Context())

		require.Len(t, saved, 3, "the pending one is asked again")
		assert.Equal(t, domain.ApprovalSetupDone, saved["w-1"].Info.State)
		assert.Equal(t, domain.ApprovalSetupFailed, saved["w-2"].Info.State)
		assert.Equal(t, domain.ApprovalSetupFailed, saved["w-4"].Info.State)
		assert.Equal(t, "not confirmed in 15 minutes", saved["w-4"].Info.Error)
	})
}
