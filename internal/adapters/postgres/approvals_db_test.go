//go:build db

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func approvalSetup(w domain.Wallet, state domain.ApprovalSetupState, at time.Time) ports.StoredApprovalSetup {
	approvals := []domain.Approval{
		{Token: "0xC011a7E12a19f7B1f670d46F03B03f3342E82DFB", Spender: "0xE111180000d2663C0091e4f400237545B87B996B", Standard: "ERC20", Name: "pUSD to the CTF Exchange"},
		{Token: "0x4D97DCd97eC945f40cF65F87097ACe5EA0476045", Spender: "0xE111180000d2663C0091e4f400237545B87B996B", Standard: "ERC1155", Name: "Outcome tokens to the CTF Exchange"},
	}

	return ports.StoredApprovalSetup{
		Info: domain.ApprovalSetup{
			WalletID: w.ID, Owner: ownerA, Kind: domain.SignatureDepositWallet, Granting: len(approvals),
			State: state, CreatedAt: at, UpdatedAt: at,
		},
		Approvals: approvals, Nonce: "7", Deadline: at.Add(10 * time.Minute),
	}
}

func TestApprovalSetups(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets)
	}{
		{"round trip, replaced", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)

			_, err := wallets.ApprovalSetup(ctx, w.ID)
			require.ErrorIs(t, err, domain.ErrNotFound)

			s := approvalSetup(w, domain.ApprovalSetupSigning, at)
			require.NoError(t, wallets.SaveApprovalSetup(ctx, s))

			got, err := wallets.ApprovalSetup(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, s, got)

			s.Info.State, s.TransactionID, s.Info.UpdatedAt = domain.ApprovalSetupSubmitted, "tx-1", at.Add(time.Minute)
			require.NoError(t, wallets.SaveApprovalSetup(ctx, s))

			got, err = wallets.ApprovalSetup(ctx, w.ID)
			require.NoError(t, err)
			assert.Equal(t, s, got)
		}},
		{"a Safe's has no deadline", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			w := seedWallet(t, db, seedUser(t, db, "a@x.io", domain.RoleUser), addrA)
			s := approvalSetup(w, domain.ApprovalSetupSigning, at)
			s.Info.Kind, s.Deadline = domain.SignatureGnosisSafe, time.Time{}
			require.NoError(t, wallets.SaveApprovalSetup(ctx, s))

			got, err := wallets.ApprovalSetup(ctx, w.ID)
			require.NoError(t, err)
			assert.True(t, got.Deadline.IsZero())
		}},
		{"submitted ones, of every wallet", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			a := seedWallet(t, db, u, addrA)
			b := seedWallet(t, db, u, "0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359")
			c := seedWallet(t, db, u, "0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB")

			require.NoError(t, wallets.SaveApprovalSetup(ctx, approvalSetup(a, domain.ApprovalSetupSubmitted, at.Add(time.Hour))))
			require.NoError(t, wallets.SaveApprovalSetup(ctx, approvalSetup(b, domain.ApprovalSetupSubmitted, at)))
			require.NoError(t, wallets.SaveApprovalSetup(ctx, approvalSetup(c, domain.ApprovalSetupDone, at)))

			got, err := wallets.SubmittedApprovalSetups(ctx)
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, b.ID, got[0].Info.WalletID, "the oldest first")
			assert.Equal(t, a.ID, got[1].Info.WalletID)
		}},
		{"unlinking removes it", func(t *testing.T, db *postgres.DB, wallets *postgres.Wallets) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			w := seedWallet(t, db, u, addrA)
			require.NoError(t, wallets.SaveApprovalSetup(ctx, approvalSetup(w, domain.ApprovalSetupDone, at)))
			require.NoError(t, wallets.Delete(ctx, u.ID, w.ID))

			_, err := wallets.ApprovalSetup(ctx, w.ID)
			require.ErrorIs(t, err, domain.ErrNotFound)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewWallets(db))
		})
	}
}
