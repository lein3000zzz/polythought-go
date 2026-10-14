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

func newCredential(owner domain.UserID, name string) ports.SealedCredential {
	return ports.SealedCredential{
		Credential: domain.Credential{
			ID: domain.CredentialID(newID()), OwnerID: owner, Type: "http.header", Name: name,
			Fields: map[string]string{"header": "Authorization"}, Secrets: []string{"value"},
			CreatedAt: at, UpdatedAt: at,
		},
		Sealed: []byte{1, 2, 3},
	}
}

func TestCredentials(t *testing.T) {
	ctx := context.Background()

	t.Run("round trip, listed by name", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCredentials(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

		b := newCredential(alice.ID, "B relay")
		a := newCredential(alice.ID, "A relay")
		a.Sealed = nil
		a.Credential.Secrets = []string{}

		require.NoError(t, repo.Create(ctx, b))
		require.NoError(t, repo.Create(ctx, a))

		got, err := repo.Get(ctx, alice.ID, b.Credential.ID)
		require.NoError(t, err)
		assert.Equal(t, b, got)

		got, err = repo.Get(ctx, alice.ID, a.Credential.ID)
		require.NoError(t, err)
		assert.Nil(t, got.Sealed)

		list, err := repo.List(ctx, alice.ID)
		require.NoError(t, err)
		require.Len(t, list, 2)
		assert.Equal(t, "A relay", list[0].Name)
		assert.Equal(t, b.Credential, list[1])

		n, err := repo.CountForOwner(ctx, alice.ID)
		require.NoError(t, err)
		assert.Equal(t, 2, n)
	})

	t.Run("names are unique per owner", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCredentials(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

		require.NoError(t, repo.Create(ctx, newCredential(alice.ID, "Relay")))
		require.ErrorIs(t, repo.Create(ctx, newCredential(alice.ID, "Relay")), domain.ErrCredentialExists)
		require.NoError(t, repo.Create(ctx, newCredential(bob.ID, "Relay")))

		other := newCredential(alice.ID, "Other")
		require.NoError(t, repo.Create(ctx, other))
		other.Credential.Name = "Relay"
		require.ErrorIs(t, repo.Update(ctx, other), domain.ErrCredentialExists)
	})

	t.Run("update and test results", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCredentials(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)

		c := newCredential(alice.ID, "Relay")
		require.NoError(t, repo.Create(ctx, c))

		require.NoError(t, repo.RecordTest(ctx, alice.ID, c.Credential.ID, at.Add(time.Minute), "refused"))

		got, err := repo.Get(ctx, alice.ID, c.Credential.ID)
		require.NoError(t, err)
		assert.Equal(t, at.Add(time.Minute), got.Credential.TestedAt)
		assert.Equal(t, "refused", got.Credential.TestError)

		c.Credential.Name = "Renamed"
		c.Credential.Fields = map[string]string{"header": "X-Api-Key"}
		c.Credential.UpdatedAt = at.Add(time.Hour)
		c.Sealed = []byte{9}
		require.NoError(t, repo.Update(ctx, c), "clears the test result, as the service asked")

		got, err = repo.Get(ctx, alice.ID, c.Credential.ID)
		require.NoError(t, err)
		assert.Equal(t, c, got)
		assert.Equal(t, at, got.Credential.CreatedAt)
	})

	t.Run("another owner's credential is not found", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCredentials(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

		c := newCredential(alice.ID, "Relay")
		require.NoError(t, repo.Create(ctx, c))
		id := c.Credential.ID

		_, err := repo.Get(ctx, bob.ID, id)
		require.ErrorIs(t, err, domain.ErrNotFound)

		stolen := c
		stolen.Credential.OwnerID = bob.ID
		require.ErrorIs(t, repo.Update(ctx, stolen), domain.ErrNotFound)
		require.ErrorIs(t, repo.RecordTest(ctx, bob.ID, id, at, ""), domain.ErrNotFound)
		require.ErrorIs(t, repo.Delete(ctx, bob.ID, id), domain.ErrNotFound)

		_, err = repo.Get(ctx, alice.ID, "not-a-uuid")
		require.ErrorIs(t, err, domain.ErrNotFound)

		list, err := repo.List(ctx, bob.ID)
		require.NoError(t, err)
		assert.Empty(t, list)

		require.NoError(t, repo.Delete(ctx, alice.ID, id))
		_, err = repo.Get(ctx, alice.ID, id)
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("they go with their owner's account", func(t *testing.T) {
		db := fresh(t)
		repo := postgres.NewCredentials(db)
		alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
		require.NoError(t, repo.Create(ctx, newCredential(alice.ID, "Relay")))

		_, err := db.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, string(alice.ID))
		require.NoError(t, err)

		n, err := repo.CountForOwner(ctx, alice.ID)
		require.NoError(t, err)
		assert.Zero(t, n)
	})
}
