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

	t.Run("a notice claimed once, again only at other caps", func(t *testing.T) {
		at := at.Add(time.Hour)
		carol := seedUser(t, db, "carol@example.com", domain.RoleUser)

		got, err := usage.ClaimNotice(ctx, carol.ID, october, domain.NoticeCapped, "", "total=100", at)
		require.NoError(t, err)
		assert.True(t, got, "the month's row made with it, no spend yet")

		got, err = usage.ClaimNotice(ctx, carol.ID, october, domain.NoticeCapped, "", "total=100", at)
		require.NoError(t, err)
		assert.False(t, got, "claimed already")

		got, err = usage.ClaimNotice(ctx, carol.ID, october, domain.NoticeWarned, "", "total=100", at)
		require.NoError(t, err)
		assert.True(t, got, "each notice its own")

		got, err = usage.ClaimNotice(ctx, carol.ID, october, domain.NoticeCapped, "total=100", "total=200", at)
		require.NoError(t, err)
		assert.True(t, got, "raised since")

		month, err := usage.Month(ctx, carol.ID, october)
		require.NoError(t, err)
		assert.Equal(t, "total=200", month.Capped)
		assert.Equal(t, "total=100", month.Warned)
		assert.Zero(t, month.Calls)

		total, err := usage.Add(ctx, carol.ID, call(5, 5, "0", 0), at)
		require.NoError(t, err)
		assert.Equal(t, int64(5), total.InputTokens, "spend adds to the row the notice made")
		assert.Equal(t, "total=200", total.Capped, "and keeps its marks")

		t.Run("racing for it, one wins", func(t *testing.T) {
			var (
				wg   sync.WaitGroup
				mu   sync.Mutex
				wins int
			)

			for range 10 {
				wg.Go(func() {
					won, err := usage.ClaimNotice(ctx, carol.ID, october, domain.NoticeWarned, "total=100", "total=300", at)
					assert.NoError(t, err)

					if won {
						mu.Lock()
						wins++
						mu.Unlock()
					}
				})
			}

			wg.Wait()
			assert.Equal(t, 1, wins)
		})
	})

	t.Run("an id that is no user's", func(t *testing.T) {
		_, err := usage.Month(ctx, "nope", october)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestUserAgentCaps(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	users := postgres.NewUsers(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

	u, err := users.ByID(ctx, alice.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AgentCaps{}, u.AgentCaps, "the instance's")
	assert.False(t, u.KillStopsAgents, "off unless chosen (D276)")

	dollars, total, none := decimal.RequireFromString("12.50"), int64(5_000_000), int64(0)
	u.AgentCaps = domain.AgentCaps{Dollars: &dollars, TotalTokens: &total, InputTokens: &none}
	require.NoError(t, users.Update(ctx, u))

	got, err := users.ByID(ctx, alice.ID)
	require.NoError(t, err)
	assert.Equal(t, "12.5", got.AgentCaps.Dollars.String())
	assert.Equal(t, int64(5_000_000), *got.AgentCaps.TotalTokens)
	assert.Equal(t, int64(0), *got.AgentCaps.InputTokens, "0 is none, not the instance's")
	assert.Nil(t, got.AgentCaps.OutputTokens)

	require.NoError(t, users.SetKillStopsAgents(ctx, alice.ID, true))
	got.AgentCaps = domain.AgentCaps{}
	require.NoError(t, users.Update(ctx, got), "an update leaves the choice be")

	got, err = users.ByID(ctx, alice.ID)
	require.NoError(t, err)
	assert.True(t, got.KillStopsAgents)
	assert.Equal(t, domain.AgentCaps{}, got.AgentCaps, "the instance's again")

	assert.ErrorIs(t, users.SetKillStopsAgents(ctx, domain.UserID(newID()), true), domain.ErrNotFound)
}
