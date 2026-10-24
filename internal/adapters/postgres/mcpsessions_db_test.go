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

// The sessions open on /mcp, kept across restarts (D414).
func TestMCPSessions(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewMCPSessions(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	session := func(id string, user domain.UserID, holder, boot string, opened time.Time) ports.MCPSession {
		return ports.MCPSession{
			ID: id, UserID: user, Holder: holder, Name: "Claude Code", Client: "claude-code 2.1.300", Boot: boot, OpenedAt: opened, SeenAt: opened,
		}
	}

	before := session("s-before", alice.ID, "grant:g-1", "boot-1", at)
	require.NoError(t, repo.Open(ctx, before))
	require.NoError(t, repo.Open(ctx, session("s-token", alice.ID, "token:t-1", "boot-1", at.Add(time.Minute))))
	require.NoError(t, repo.Open(ctx, session("s-bob", bob.ID, "grant:g-9", "boot-1", at)))

	again := before
	again.Name = "renamed"
	require.NoError(t, repo.Open(ctx, again), "kept as it was")

	got, err := repo.Of(ctx, alice.ID, at.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 2, "alice's alone")
	assert.Equal(t, "s-token", got[0].ID, "the newest first")
	assert.Equal(t, before, got[1])

	require.NoError(t, repo.Seen(ctx, "s-token", at.Add(30*time.Minute)))

	got, err = repo.Of(ctx, alice.ID, at.Add(10*time.Minute))
	require.NoError(t, err)
	require.Len(t, got, 1, "seen since")
	assert.Equal(t, at.Add(30*time.Minute), got[0].SeenAt)

	// After a restart, the agent's new session replaces its old one; the token's stays.
	require.NoError(t, repo.Open(ctx, session("s-after", alice.ID, "grant:g-1", "boot-2", at.Add(time.Hour))))
	require.NoError(t, repo.Supersede(ctx, alice.ID, "grant:g-1", "boot-2"))

	got, err = repo.Of(ctx, alice.ID, at.Add(-time.Hour))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"s-after", "s-token"}, []string{got[0].ID, got[1].ID})

	require.NoError(t, repo.Close(ctx, "s-after"))
	require.NoError(t, repo.Prune(ctx, at.Add(10*time.Minute)))

	got, err = repo.Of(ctx, alice.ID, at.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "s-token", got[0].ID, "seen lately, so not pruned")

	left, err := repo.Of(ctx, bob.ID, at.Add(-time.Hour))
	require.NoError(t, err)
	assert.Empty(t, left, "bob's, not seen since the prune's time")
}
