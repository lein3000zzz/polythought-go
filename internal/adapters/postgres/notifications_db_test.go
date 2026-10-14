//go:build db

package postgres_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

func TestNotificationPrefs(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewNotificationPrefs(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	prefs, err := repo.Prefs(ctx, alice.ID)
	require.NoError(t, err)
	assert.Empty(t, prefs, "nothing changed: every kind at its default")

	require.NoError(t, repo.SetPref(ctx, alice.ID, "workflow_failed", false))
	require.NoError(t, repo.SetPref(ctx, alice.ID, "other", true))
	require.NoError(t, repo.SetPref(ctx, alice.ID, "other", false))

	prefs, err = repo.Prefs(ctx, alice.ID)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"workflow_failed": false, "other": false}, prefs)

	prefs, err = repo.Prefs(ctx, bob.ID)
	require.NoError(t, err)
	assert.Empty(t, prefs)
}
