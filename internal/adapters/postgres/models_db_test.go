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

// Users' models: unique names per owner, a URL model's address and credential, what each
// last did, and worker keys found by their hash alone, gone with their model.
func TestModels(t *testing.T) {
	ctx := context.Background()

	db := fresh(t)
	store := postgres.NewModels(db)
	alice := seedUser(t, db, "alice@example.com", domain.RoleUser)
	bob := seedUser(t, db, "bob@example.com", domain.RoleUser)

	cred := newCredential(alice.ID, "Model key")
	require.NoError(t, postgres.NewCredentials(db).Create(ctx, cred))

	temps := domain.Model{ID: domain.ModelID(newID()), OwnerID: alice.ID, Name: "temps", Kind: domain.ModelWorker, CreatedAt: at, UpdatedAt: at}
	hosted := domain.Model{
		ID: domain.ModelID(newID()), OwnerID: alice.ID, Name: "hosted", Kind: domain.ModelURL, URL: "https://models.example/predict",
		Credential: cred.Credential.ID, CreatedAt: at, UpdatedAt: at,
	}

	require.NoError(t, store.Create(ctx, temps))
	require.NoError(t, store.Create(ctx, hosted))
	require.ErrorIs(t, store.Create(ctx, domain.Model{ID: domain.ModelID(newID()), OwnerID: alice.ID, Name: "temps", Kind: domain.ModelWorker, CreatedAt: at}),
		domain.ErrModelExists)
	require.NoError(t, store.Create(ctx, domain.Model{ID: domain.ModelID(newID()), OwnerID: bob.ID, Name: "temps", Kind: domain.ModelWorker, CreatedAt: at}),
		"another user's may share the name")

	list, err := store.List(ctx, alice.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, "hosted", list[0].Name, "by name")
	assert.Equal(t, cred.Credential.ID, list[0].Credential)

	got, err := store.Get(ctx, alice.ID, "temps")
	require.NoError(t, err)
	assert.Equal(t, temps.ID, got.ID)
	assert.True(t, got.LastAnswerAt.IsZero())

	_, err = store.Get(ctx, bob.ID, "hosted")
	require.ErrorIs(t, err, domain.ErrNotFound)

	// What it last did.
	require.NoError(t, store.Answered(ctx, temps.ID, at.Add(time.Minute)))
	require.NoError(t, store.Failed(ctx, temps.ID, "no answer within 30s", at.Add(2*time.Minute)))

	got, err = store.ByID(ctx, temps.ID)
	require.NoError(t, err)
	assert.True(t, got.LastAnswerAt.Equal(at.Add(time.Minute)))
	assert.Equal(t, "no answer within 30s", got.LastError)
	assert.True(t, got.LastErrorAt.Equal(at.Add(2*time.Minute)))

	// A URL model's new address, without its credential.
	require.NoError(t, store.SetURL(ctx, hosted.ID, "https://models.example/v2", "", at.Add(time.Hour)))

	got, err = store.ByID(ctx, hosted.ID)
	require.NoError(t, err)
	assert.Equal(t, "https://models.example/v2", got.URL)
	assert.Empty(t, got.Credential)

	// Worker keys, found by hash alone.
	key := domain.WorkerKey{ID: domain.WorkerKeyID(newID()), ModelID: temps.ID, Name: "gpu box", Prefix: "pw_abcdefg", CreatedAt: at}
	require.NoError(t, store.CreateKey(ctx, key, []byte("hash-1")))
	require.NoError(t, store.CreateKey(ctx, domain.WorkerKey{ID: domain.WorkerKeyID(newID()), ModelID: temps.ID, Name: "laptop", Prefix: "pw_hijklmn", CreatedAt: at.Add(time.Hour)}, []byte("hash-2")))

	keys, err := store.Keys(ctx, temps.ID)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, "laptop", keys[0].Name, "the newest first")

	k, m, err := store.KeyByHash(ctx, []byte("hash-1"))
	require.NoError(t, err)
	assert.Equal(t, key.ID, k.ID)
	assert.Equal(t, temps.ID, m.ID)

	_, _, err = store.KeyByHash(ctx, []byte("nope"))
	require.ErrorIs(t, err, domain.ErrNotFound)

	require.NoError(t, store.KeyUsed(ctx, key.ID, at.Add(3*time.Hour)))
	k, _, err = store.KeyByHash(ctx, []byte("hash-1"))
	require.NoError(t, err)
	assert.True(t, k.LastUsedAt.Equal(at.Add(3*time.Hour)))

	require.ErrorIs(t, store.DeleteKey(ctx, hosted.ID, key.ID), domain.ErrNotFound, "another model's key")
	require.NoError(t, store.DeleteKey(ctx, temps.ID, key.ID))

	_, _, err = store.KeyByHash(ctx, []byte("hash-1"))
	require.ErrorIs(t, err, domain.ErrNotFound, "revoked")

	// Deleted, its keys go with it.
	require.NoError(t, store.Delete(ctx, temps.ID))

	_, _, err = store.KeyByHash(ctx, []byte("hash-2"))
	require.ErrorIs(t, err, domain.ErrNotFound)

	_, err = store.Get(ctx, alice.ID, "temps")
	require.ErrorIs(t, err, domain.ErrNotFound)
}
