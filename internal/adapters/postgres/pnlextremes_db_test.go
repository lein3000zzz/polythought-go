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
)

// Each workflow's highest and lowest, kept as samples come (D430).
func TestPnLExtremes(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	store := postgres.NewPnLExtremes(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	paper, bobs := newWorkflow(alice.ID, "paper"), newWorkflow(bob.ID, "bob's")
	for _, w := range []domain.Workflow{paper, bobs} {
		require.NoError(t, postgres.NewWorkflows(db).Create(ctx, w))
	}

	reset := at.Add(24 * time.Hour)
	sample := func(w domain.Workflow, mode domain.TradingMode, since time.Time, profit string, when time.Duration) domain.PnLSample {
		return domain.PnLSample{WorkflowID: w.ID, Mode: mode, Since: since, Profit: num(profit), At: at.Add(when)}
	}

	record := func(samples ...domain.PnLSample) {
		t.Helper()
		require.NoError(t, store.Record(ctx, samples))
	}

	kept := func(owner domain.UserID) map[domain.TradingMode]domain.PnLExtremes {
		t.Helper()

		all, err := store.Owner(ctx, owner)
		require.NoError(t, err)

		by := map[domain.TradingMode]domain.PnLExtremes{}
		for _, e := range all {
			by[e.Mode] = e
		}

		return by
	}

	record(sample(paper, domain.ModePaper, at, "5", 0), sample(paper, domain.ModeLive, time.Time{}, "-1", 0), sample(bobs, domain.ModePaper, at, "3", 0))

	first := kept(alice.ID)[domain.ModePaper]
	assert.Equal(t, "5", first.High.String(), "the first is both")
	assert.Equal(t, "5", first.Low.String())
	assert.True(t, first.Since.Equal(at))

	record(sample(paper, domain.ModePaper, at, "9", time.Hour))
	record(sample(paper, domain.ModePaper, at, "7", 2*time.Hour))
	record(sample(paper, domain.ModePaper, at, "-2", 3*time.Hour))

	e := kept(alice.ID)[domain.ModePaper]
	assert.Equal(t, "9", e.High.String(), "a higher one replaced it, a lower did not")
	assert.True(t, e.HighAt.Equal(at.Add(time.Hour)))
	assert.Equal(t, "-2", e.Low.String())
	assert.True(t, e.LowAt.Equal(at.Add(3*time.Hour)))

	live := kept(alice.ID)[domain.ModeLive]
	assert.Equal(t, "-1", live.High.String(), "live kept apart")
	assert.True(t, live.Since.IsZero(), "live counts from nothing")

	record(sample(paper, domain.ModePaper, reset, "1", 25*time.Hour))

	e = kept(alice.ID)[domain.ModePaper]
	assert.True(t, e.Since.Equal(reset))
	assert.Equal(t, "1", e.High.String(), "a reset starts over")
	assert.Equal(t, "1", e.Low.String())
	assert.True(t, e.HighAt.Equal(at.Add(25*time.Hour)))

	record(sample(paper, domain.ModePaper, at, "50", 26*time.Hour))
	assert.Equal(t, "1", kept(alice.ID)[domain.ModePaper].High.String(), "a sample from before the reset changes nothing")

	require.Len(t, kept(bob.ID), 1, "bob's alone")
	assert.Equal(t, "3", kept(bob.ID)[domain.ModePaper].High.String())

	require.NoError(t, postgres.NewWorkflows(db).Delete(ctx, alice.ID, paper.ID))
	assert.Empty(t, kept(alice.ID), "gone with the workflow")
}
