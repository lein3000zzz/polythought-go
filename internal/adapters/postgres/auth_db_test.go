//go:build db

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/ratelimit"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

func TestUsers(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, users *postgres.Users)
	}{
		{"round trip", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			u := seedUser(t, db, "Bob@Example.com", domain.RoleUser)

			got, err := users.ByID(ctx, u.ID)
			require.NoError(t, err)
			assert.Equal(t, u, got)
		}},
		{"email lookup ignores case and returns the hash", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			u := seedUser(t, db, "Bob@Example.com", domain.RoleUser)

			got, hash, err := users.ByEmail(ctx, "  bob@EXAMPLE.com ")
			require.NoError(t, err)
			assert.Equal(t, u.ID, got.ID)
			assert.Equal(t, "$hash-Bob@Example.com", hash)
		}},
		{"emails are unique regardless of case", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			seedUser(t, db, "bob@example.com", domain.RoleUser)

			err := users.Create(ctx, domain.User{ID: domain.UserID(newID()), Email: "BOB@example.com", Role: domain.RoleUser, CreatedAt: at, UpdatedAt: at}, "$x")
			assert.ErrorIs(t, err, domain.ErrEmailTaken)
		}},
		{"unknown and malformed ids are not found", func(t *testing.T, _ *postgres.DB, users *postgres.Users) {
			_, err := users.ByID(ctx, domain.UserID(newID()))
			assert.ErrorIs(t, err, domain.ErrNotFound)

			_, err = users.ByID(ctx, "not-a-uuid")
			assert.ErrorIs(t, err, domain.ErrNotFound)

			_, _, err = users.ByEmail(ctx, "nobody@example.com")
			assert.ErrorIs(t, err, domain.ErrNotFound)
		}},
		{"password hash can be replaced", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			u := seedUser(t, db, "bob@example.com", domain.RoleUser)

			require.NoError(t, users.SetPasswordHash(ctx, u.ID, "$new", at.Add(time.Hour)))

			hash, err := users.PasswordHash(ctx, u.ID)
			require.NoError(t, err)
			assert.Equal(t, "$new", hash)

			assert.ErrorIs(t, users.SetPasswordHash(ctx, domain.UserID(newID()), "$x", at), domain.ErrNotFound)
		}},
		{"update writes role, disabled state and code access", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			u := seedUser(t, db, "bob@example.com", domain.RoleUser)
			assert.False(t, u.CodeChosen)

			u.Role = domain.RoleAdmin
			u.DisabledAt = at.Add(time.Minute)
			u.CodeChosen = true
			u.UpdatedAt = at.Add(time.Minute)

			require.NoError(t, users.Update(ctx, u))

			got, err := users.ByID(ctx, u.ID)
			require.NoError(t, err)
			assert.Equal(t, u, got)
		}},
		{"active admins exclude disabled ones", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			seedUser(t, db, "a@example.com", domain.RoleAdmin)
			seedUser(t, db, "u@example.com", domain.RoleUser)

			off := seedUser(t, db, "b@example.com", domain.RoleAdmin)
			off.DisabledAt = at
			require.NoError(t, users.Update(ctx, off))

			n, err := users.CountActiveAdmins(ctx)
			require.NoError(t, err)
			assert.Equal(t, 1, n)

			admins, err := users.Admins(ctx)
			require.NoError(t, err)
			require.Len(t, admins, 1, "told what only admins are")
			assert.Equal(t, "a@example.com", admins[0].Email)
		}},
		{"list pages in creation order", func(t *testing.T, db *postgres.DB, users *postgres.Users) {
			var ids []domain.UserID
			for _, e := range []string{"a@x.io", "b@x.io", "c@x.io"} {
				ids = append(ids, seedUser(t, db, e, domain.RoleUser).ID)
			}

			first, err := users.List(ctx, ports.PageFilter{Limit: 2})
			require.NoError(t, err)
			require.Len(t, first.Items, 2)
			assert.Equal(t, ids[:2], []domain.UserID{first.Items[0].ID, first.Items[1].ID})
			require.NotEmpty(t, first.NextCursor)

			second, err := users.List(ctx, ports.PageFilter{Limit: 2, Cursor: first.NextCursor})
			require.NoError(t, err)
			require.Len(t, second.Items, 1)
			assert.Equal(t, ids[2], second.Items[0].ID)
			assert.Empty(t, second.NextCursor)

			_, err = users.List(ctx, ports.PageFilter{Cursor: "garbage"})
			assert.ErrorIs(t, err, domain.ErrInvalidInput)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewUsers(db))
		})
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()

	newSession := func(u domain.User, expires, absolute time.Duration) domain.Session {
		return domain.Session{
			ID: domain.SessionID(newID()), UserID: u.ID, UserAgent: "ua", IP: "1.2.3.4",
			CreatedAt: at, LastSeenAt: at, ExpiresAt: at.Add(expires), AbsoluteExpiresAt: at.Add(absolute),
		}
	}

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, sessions *postgres.Sessions)
	}{
		{"round trip by token hash", func(t *testing.T, db *postgres.DB, sessions *postgres.Sessions) {
			s := newSession(seedUser(t, db, "a@x.io", domain.RoleUser), time.Hour, 24*time.Hour)
			require.NoError(t, sessions.Create(ctx, s, []byte("hash-1")))

			got, err := sessions.ByTokenHash(ctx, []byte("hash-1"))
			require.NoError(t, err)
			assert.Equal(t, s, got)

			_, err = sessions.ByTokenHash(ctx, []byte("other"))
			assert.ErrorIs(t, err, domain.ErrNotFound)
		}},
		{"touch slides the expiry", func(t *testing.T, db *postgres.DB, sessions *postgres.Sessions) {
			s := newSession(seedUser(t, db, "a@x.io", domain.RoleUser), time.Hour, 24*time.Hour)
			require.NoError(t, sessions.Create(ctx, s, []byte("h")))
			require.NoError(t, sessions.Touch(ctx, s.ID, at.Add(time.Minute), at.Add(2*time.Hour)))

			got, err := sessions.ByTokenHash(ctx, []byte("h"))
			require.NoError(t, err)
			assert.Equal(t, at.Add(time.Minute), got.LastSeenAt)
			assert.Equal(t, at.Add(2*time.Hour), got.ExpiresAt)
		}},
		{"delete for user keeps the current one", func(t *testing.T, db *postgres.DB, sessions *postgres.Sessions) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			keep, drop := newSession(u, time.Hour, time.Hour), newSession(u, time.Hour, time.Hour)
			other := newSession(seedUser(t, db, "b@x.io", domain.RoleUser), time.Hour, time.Hour)

			require.NoError(t, sessions.Create(ctx, keep, []byte("keep")))
			require.NoError(t, sessions.Create(ctx, drop, []byte("drop")))
			require.NoError(t, sessions.Create(ctx, other, []byte("other")))
			require.NoError(t, sessions.DeleteForUser(ctx, u.ID, keep.ID))

			for hash, want := range map[string]error{"keep": nil, "drop": domain.ErrNotFound, "other": nil} {
				_, err := sessions.ByTokenHash(ctx, []byte(hash))
				assert.ErrorIs(t, err, want, hash)
			}
		}},
		{"expired sessions are pruned by either deadline", func(t *testing.T, db *postgres.DB, sessions *postgres.Sessions) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			require.NoError(t, sessions.Create(ctx, newSession(u, time.Hour, 24*time.Hour), []byte("live")))
			require.NoError(t, sessions.Create(ctx, newSession(u, -time.Minute, 24*time.Hour), []byte("idle")))
			require.NoError(t, sessions.Create(ctx, newSession(u, time.Hour, -time.Minute), []byte("absolute")))

			n, err := sessions.DeleteExpired(ctx, at)
			require.NoError(t, err)
			assert.Equal(t, int64(2), n)

			_, err = sessions.ByTokenHash(ctx, []byte("live"))
			assert.NoError(t, err)
		}},
		{"delete", func(t *testing.T, db *postgres.DB, sessions *postgres.Sessions) {
			s := newSession(seedUser(t, db, "a@x.io", domain.RoleUser), time.Hour, time.Hour)
			require.NoError(t, sessions.Create(ctx, s, []byte("h")))
			require.NoError(t, sessions.Delete(ctx, s.ID))
			assert.ErrorIs(t, sessions.Delete(ctx, s.ID), domain.ErrNotFound)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewSessions(db))
		})
	}
}

func TestAPITokens(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *postgres.DB, tokens *postgres.APITokens)
	}{
		{"round trip and listing", func(t *testing.T, db *postgres.DB, tokens *postgres.APITokens) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			tok := domain.APIToken{ID: domain.APITokenID(newID()), UserID: u.ID, Name: "cli", Prefix: "pt_abcdefg", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
			require.NoError(t, tokens.Create(ctx, tok, []byte("h")))

			got, err := tokens.ByTokenHash(ctx, []byte("h"))
			require.NoError(t, err)
			assert.Equal(t, tok, got)

			list, err := tokens.ListForUser(ctx, u.ID)
			require.NoError(t, err)
			assert.Equal(t, []domain.APIToken{tok}, list)
		}},
		{"delete is scoped to the owner", func(t *testing.T, db *postgres.DB, tokens *postgres.APITokens) {
			owner := seedUser(t, db, "a@x.io", domain.RoleUser)
			stranger := seedUser(t, db, "b@x.io", domain.RoleUser)
			tok := domain.APIToken{ID: domain.APITokenID(newID()), UserID: owner.ID, Name: "cli", Prefix: "pt_x", CreatedAt: at}
			require.NoError(t, tokens.Create(ctx, tok, []byte("h")))

			assert.ErrorIs(t, tokens.Delete(ctx, stranger.ID, tok.ID), domain.ErrNotFound)
			assert.NoError(t, tokens.Delete(ctx, owner.ID, tok.ID))
			assert.ErrorIs(t, tokens.Delete(ctx, owner.ID, "not-a-uuid"), domain.ErrNotFound)
		}},
		{"last use is recorded", func(t *testing.T, db *postgres.DB, tokens *postgres.APITokens) {
			u := seedUser(t, db, "a@x.io", domain.RoleUser)
			tok := domain.APIToken{ID: domain.APITokenID(newID()), UserID: u.ID, Name: "cli", Prefix: "pt_x", CreatedAt: at}
			require.NoError(t, tokens.Create(ctx, tok, []byte("h")))
			require.NoError(t, tokens.TouchLastUsed(ctx, tok.ID, at.Add(time.Hour)))

			got, err := tokens.ByTokenHash(ctx, []byte("h"))
			require.NoError(t, err)
			assert.Equal(t, at.Add(time.Hour), got.LastUsedAt)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			tt.run(t, db, postgres.NewAPITokens(db))
		})
	}
}

func TestInvites(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		expires time.Duration
		used    bool
		wantErr error
	}{
		{name: "fresh invite is consumed", expires: time.Hour},
		{name: "used invite is rejected", expires: time.Hour, used: true, wantErr: domain.ErrInvalidInvite},
		{name: "expired invite is rejected", expires: -time.Second, wantErr: domain.ErrInvalidInvite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)
			invites := postgres.NewInvites(db)
			admin := seedUser(t, db, "admin@x.io", domain.RoleAdmin)
			joiner := seedUser(t, db, "new@x.io", domain.RoleUser)

			inv := domain.Invite{ID: domain.InviteID(newID()), CreatedBy: admin.ID, CreatedAt: at.Add(-time.Hour), ExpiresAt: at.Add(tt.expires)}
			require.NoError(t, invites.Create(ctx, inv, []byte("code")))

			if tt.used {
				require.NoError(t, invites.Consume(ctx, []byte("code"), admin.ID, at))
			}

			err := invites.Consume(ctx, []byte("code"), joiner.ID, at)
			require.ErrorIs(t, err, tt.wantErr)

			if tt.wantErr == nil {
				list, err := invites.List(ctx)
				require.NoError(t, err)
				require.Len(t, list, 1)
				assert.Equal(t, joiner.ID, list[0].UsedBy)
				assert.Equal(t, at, list[0].UsedAt)
				assert.Equal(t, admin.ID, list[0].CreatedBy)
			}
		})
	}
}

func TestInviteByCode(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	invites := postgres.NewInvites(db)
	admin := seedUser(t, db, "admin@x.io", domain.RoleAdmin)

	mailed := domain.Invite{ID: domain.InviteID(newID()), CreatedBy: admin.ID, Email: "Joiner@x.io", CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	require.NoError(t, invites.Create(ctx, mailed, []byte("mailed")))

	got, err := invites.ByCode(ctx, []byte("mailed"), at)
	require.NoError(t, err)
	assert.Equal(t, mailed, got)

	list, err := invites.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Joiner@x.io", list[0].Email)

	_, err = invites.ByCode(ctx, []byte("mailed"), at.Add(2*time.Hour))
	require.ErrorIs(t, err, domain.ErrInvalidInvite, "expired")

	_, err = invites.ByCode(ctx, []byte("nope"), at)
	require.ErrorIs(t, err, domain.ErrInvalidInvite)

	require.NoError(t, invites.Consume(ctx, []byte("mailed"), admin.ID, at))
	_, err = invites.ByCode(ctx, []byte("mailed"), at)
	require.ErrorIs(t, err, domain.ErrInvalidInvite, "used")
}

func TestWithinTx(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("boom")

	tests := []struct {
		name      string
		fn        func(ctx context.Context, db *postgres.DB) error
		wantErr   error
		wantUsers int
	}{
		{
			name: "commit",
			fn: func(ctx context.Context, db *postgres.DB) error {
				return postgres.NewUsers(db).Create(ctx, domain.User{ID: domain.UserID(newID()), Email: "a@x.io", Role: domain.RoleUser, CreatedAt: at, UpdatedAt: at}, "$h")
			},
			wantUsers: 1,
		},
		{
			name: "error rolls back",
			fn: func(ctx context.Context, db *postgres.DB) error {
				err := postgres.NewUsers(db).Create(ctx, domain.User{ID: domain.UserID(newID()), Email: "a@x.io", Role: domain.RoleUser, CreatedAt: at, UpdatedAt: at}, "$h")
				require.NoError(t, err)

				return boom
			},
			wantErr: boom,
		},
		{
			name: "nested calls join the outer transaction",
			fn: func(ctx context.Context, db *postgres.DB) error {
				err := db.WithinTx(ctx, func(ctx context.Context) error {
					return postgres.NewUsers(db).Create(ctx, domain.User{ID: domain.UserID(newID()), Email: "a@x.io", Role: domain.RoleUser, CreatedAt: at, UpdatedAt: at}, "$h")
				})
				require.NoError(t, err)

				return boom // rolls back the inner insert too
			},
			wantErr: boom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := fresh(t)

			err := db.WithinTx(ctx, func(ctx context.Context) error { return tt.fn(ctx, db) })
			require.ErrorIs(t, err, tt.wantErr)

			page, err := postgres.NewUsers(db).List(ctx, ports.PageFilter{})
			require.NoError(t, err)
			assert.Len(t, page.Items, tt.wantUsers)
		})
	}
}

// TestLastAdminRace demotes the only two admins at the same moment, many times over.
// Without the row lock both demotions can pass the "another admin remains" check and
// leave no admin; with it exactly one wins each round.
func TestLastAdminRace(t *testing.T) {
	ctx := context.Background()

	for round := range 25 {
		db := fresh(t)

		svc, err := auth.New(auth.DefaultConfig(), auth.Repositories{
			Users:    postgres.NewUsers(db),
			Sessions: postgres.NewSessions(db),
			Tokens:   postgres.NewAPITokens(db),
			Invites:  postgres.NewInvites(db),
			Tx:       db,
		}, crypto.NewArgon2id(crypto.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}),
			ratelimit.NewMemory(time.Second, 100), logging.Noop())
		require.NoError(t, err)

		a := seedUser(t, db, "a@x.io", domain.RoleAdmin)
		b := seedUser(t, db, "b@x.io", domain.RoleAdmin)

		demote := domain.RoleUser
		errs := make(chan error, 2)

		var start sync.WaitGroup
		start.Add(1)

		for _, pair := range [][2]domain.User{{a, b}, {b, a}} {
			go func() {
				start.Wait()
				_, err := svc.UpdateUser(ctx, auth.Principal{User: pair[0], SessionID: "s"}, pair[1].ID, auth.UserUpdate{Role: &demote})
				errs <- err
			}()
		}

		start.Done()

		var ok, lastAdmin int
		for range 2 {
			switch err := <-errs; {
			case err == nil:
				ok++
			case errors.Is(err, domain.ErrLastAdmin):
				lastAdmin++
			default:
				require.NoError(t, err, "round %d", round)
			}
		}

		require.Equal(t, 1, ok, "round %d: exactly one demotion wins", round)
		require.Equal(t, 1, lastAdmin, "round %d", round)

		n, err := postgres.NewUsers(db).CountActiveAdmins(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, n, "round %d: an admin always remains", round)
	}
}
