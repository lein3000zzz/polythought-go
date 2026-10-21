//go:build db

package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestAgentUsage(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	usage := postgres.NewAgentUsage(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	call := func(in, out int64, dollars string, costed int) domain.AgentUsage {
		return domain.AgentUsage{
			Month: october, InputTokens: in, OutputTokens: out, Dollars: decimal.RequireFromString(dollars), Calls: 1, CostedCalls: costed,
		}
	}

	t.Run("nothing spent is no row", func(t *testing.T) {
		got, err := usage.Month(ctx, alice.ID, october)
		require.NoError(t, err)
		assert.Equal(t, domain.AgentUsage{Month: october, Dollars: decimal.Zero}, got)
	})

	t.Run("calls add up, dollars exactly", func(t *testing.T) {
		first, err := usage.Add(ctx, alice.ID, call(100, 20, "0", 0), at)
		require.NoError(t, err)
		assert.Equal(t, int64(100), first.InputTokens)

		total, err := usage.Add(ctx, alice.ID, call(50, 5, "0.0000012", 1), at.Add(time.Minute))
		require.NoError(t, err)
		assert.Equal(t, int64(150), total.InputTokens)
		assert.Equal(t, int64(25), total.OutputTokens)
		assert.Equal(t, "0.0000012", total.Dollars.String())
		assert.Equal(t, 2, total.Calls)
		assert.Equal(t, 1, total.CostedCalls)
		assert.Equal(t, october, total.Month)

		got, err := usage.Month(ctx, alice.ID, october.Add(20*24*time.Hour))
		require.NoError(t, err)
		assert.Equal(t, total, got, "any time in the month reads it")
	})

	t.Run("calls at once each count", func(t *testing.T) {
		var wg sync.WaitGroup

		for range 20 {
			wg.Go(func() {
				_, err := usage.Add(ctx, bob.ID, call(10, 1, "0.01", 1), at)
				assert.NoError(t, err)
			})
		}

		wg.Wait()

		got, err := usage.Month(ctx, bob.ID, october)
		require.NoError(t, err)
		assert.Equal(t, int64(200), got.InputTokens)
		assert.Equal(t, "0.2", got.Dollars.String())
		assert.Equal(t, 20, got.Calls)
	})

	t.Run("each month and user its own", func(t *testing.T) {
		next := call(1, 1, "0", 0)
		next.Month = october.AddDate(0, 1, 0)
		_, err := usage.Add(ctx, alice.ID, next, at.AddDate(0, 1, 0))
		require.NoError(t, err)

		got, err := usage.Month(ctx, alice.ID, october)
		require.NoError(t, err)
		assert.Equal(t, int64(150), got.InputTokens, "October unchanged")
	})

	t.Run("a user's rows go with them", func(t *testing.T) {
		_, err := db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, string(bob.ID))
		require.NoError(t, err)

		var left int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_usage WHERE user_id = $1`, string(bob.ID)).Scan(&left))
		assert.Zero(t, left)

		_, err = usage.Add(ctx, bob.ID, call(1, 1, "0", 0), at)
		require.Error(t, err, "no such user")
	})

	t.Run("an id that is no user's", func(t *testing.T) {
		_, err := usage.Month(ctx, "nope", october)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}
