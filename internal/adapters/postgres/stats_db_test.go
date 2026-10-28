//go:build db

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// The reads across an owner's workflows, for the stats (D429).
func TestOwnerStats(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	ledger := postgres.NewLedger(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	paper, live, bobs := newWorkflow(alice.ID, "paper"), newWorkflow(alice.ID, "live"), newWorkflow(bob.ID, "bob's")
	for _, w := range []domain.Workflow{paper, live, bobs} {
		require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))
	}

	buy := func(w domain.Workflow, mode domain.TradingMode, when time.Time, shares string) {
		t.Helper()

		o := domain.PlacedOrder{
			OrderRequest: domain.OrderRequest{
				WorkflowID: w.ID, OwnerID: w.OwnerID, NodeID: "buy", Mode: mode, TokenID: domain.TokenID("t-" + string(mode)), MarketID: "0xm",
				Side: domain.SideBuy, Type: domain.OrderFAK, Price: num("0.5"), Shares: num(shares),
			},
			ID: domain.OrderID(newID()), Status: domain.OrderFilled, CreatedAt: when,
			Fills: []domain.OrderFill{{Price: num("0.5"), Shares: num(shares), Dollars: num(shares).Div(num("2")), Fee: num("0")}},
		}

		change := &ports.LedgerChange{Holding: domain.Holding{
			WorkflowID: w.ID, Mode: mode, TokenID: o.TokenID, MarketID: "0xm", Shares: num(shares),
			Cost: o.Fills[0].Dollars, Realized: decimal.Zero, UpdatedAt: when,
		}}
		if mode == domain.ModePaper {
			change.Cash = o.Fills[0].Dollars.Neg()
		}

		require.NoError(t, ledger.Record(ctx, o, change))
	}

	for _, w := range []domain.Workflow{paper, bobs} {
		_, err := ledger.OpenPaperAccount(ctx, domain.PaperAccount{WorkflowID: w.ID, OwnerID: w.OwnerID, Cash: num("100"), StartedWith: num("100"), StartedAt: at})
		require.NoError(t, err)
	}

	buy(paper, domain.ModePaper, at, "10")

	reset := at.Add(time.Hour)
	require.NoError(t, ledger.ResetPaperAccount(ctx, domain.PaperAccount{WorkflowID: paper.ID, Cash: num("100"), StartedWith: num("100"), StartedAt: reset, ResetAt: reset}))

	buy(paper, domain.ModePaper, reset.Add(time.Minute), "4")
	buy(live, domain.ModeLive, at, "6")
	buy(bobs, domain.ModePaper, at, "8")

	require.NoError(t, postgres.NewWorkflowTrading(db).SetTrading(ctx, domain.WorkflowTrading{WorkflowID: live.ID, Mode: domain.ModeLive, UpdatedAt: at}))

	t.Run("paper accounts, holdings and trading, alice's alone", func(t *testing.T) {
		accounts, err := ledger.OwnerPaperAccounts(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, accounts, 1)
		assert.Equal(t, paper.ID, accounts[0].WorkflowID)
		assert.Equal(t, "98", accounts[0].Cash.String(), "100 again at the reset, less the 2 spent since")

		holdings, err := ledger.OwnerHoldings(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, holdings, 2, "the reset emptied the first")

		for _, h := range holdings {
			assert.Equal(t, alice.ID, h.OwnerID)
		}

		trading, err := postgres.NewWorkflowTrading(db).OwnerTrading(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, trading, 1, "only the one that said")
		assert.Equal(t, domain.ModeLive, trading[0].Mode)
	})

	t.Run("orders by workflow and mode, on paper since the reset", func(t *testing.T) {
		totals, err := ledger.OwnerTraderTotals(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, totals, 2)

		by := map[domain.WorkflowID]domain.TraderTotal{}
		for _, tt := range totals {
			by[tt.WorkflowID] = tt
		}

		assert.Equal(t, 1, by[paper.ID].Orders, "the one before the reset is history")
		assert.Equal(t, "2", by[paper.ID].Volume.String())
		assert.Equal(t, domain.ModePaper, by[paper.ID].Mode)
		assert.Equal(t, 1, by[live.ID].Orders)
		assert.Equal(t, domain.ModeLive, by[live.ID].Mode)
	})

	t.Run("runs in the last day and week, and the latest", func(t *testing.T) {
		runs := postgres.NewExecutions(db)
		now := at.Add(30 * 24 * time.Hour)

		run := func(w domain.Workflow, ago time.Duration, status domain.ExecutionStatus) domain.ExecutionID {
			t.Helper()

			e := domain.Execution{
				ID: domain.ExecutionID(newID()), WorkflowID: w.ID, WorkflowVersion: 1, TriggeredBy: "manual",
				Status: status, StartedAt: now.Add(-ago), FinishedAt: now.Add(-ago).Add(time.Second),
			}
			require.NoError(t, runs.Save(ctx, e))

			return e.ID
		}

		// Oldest first, as their IDs grow with time.
		run(paper, 10*24*time.Hour, domain.ExecutionFailed)
		run(paper, 2*24*time.Hour, domain.ExecutionFailed)
		run(paper, 3*time.Hour, domain.ExecutionSucceeded)
		latest := run(paper, time.Hour, domain.ExecutionFailed)
		run(bobs, time.Hour, domain.ExecutionSucceeded)

		got, err := runs.RunStats(ctx, alice.ID, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
		require.NoError(t, err)
		require.Len(t, got, 1, "alice's workflow with runs")

		st := got[0]
		assert.Equal(t, paper.ID, st.WorkflowID)
		assert.Equal(t, domain.RunCounts{Runs: 2, Failed: 1}, st.Day)
		assert.Equal(t, domain.RunCounts{Runs: 3, Failed: 2}, st.Week, "the one 10 days ago is older")
		require.NotNil(t, st.Last)
		assert.Equal(t, latest, st.Last.ID)
		assert.Equal(t, domain.ExecutionFailed, st.Last.Status)
		assert.True(t, st.Last.StartedAt.Equal(now.Add(-time.Hour)))
		require.NotNil(t, st.Last.FinishedAt)
	})
}
