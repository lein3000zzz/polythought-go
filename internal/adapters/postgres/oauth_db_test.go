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

const mcpURL = "https://pt.example/mcp"

func oauthClient(id string, kind domain.OAuthClientKind, created time.Time) domain.OAuthClient {
	return domain.OAuthClient{
		ID: id, Kind: kind, Name: "Agent " + id, RedirectURIs: []string{"http://127.0.0.1/callback"}, CreatedAt: created,
	}
}

func oauthGrant(user domain.UserID, c domain.OAuthClient, created time.Time, scopes ...domain.Scope) domain.Grant {
	return domain.Grant{
		ID: domain.GrantID(newID()), UserID: user, Client: c, Scopes: scopes, Resource: mcpURL,
		CreatedAt: created, UpdatedAt: created,
	}
}

// Clients are kept by their IDs; a registered one no grant came of goes after a while,
// a document never does (D397).
func TestOAuthClients(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewOAuth(db)
	u := seedUser(t, db, "a@x.io", domain.RoleUser)

	_, err := repo.Client(ctx, "pc_none")
	require.ErrorIs(t, err, domain.ErrNotFound)

	doc := oauthClient("https://agent.example/client.json", domain.ClientDocument, at)
	doc.FetchedAt, doc.ClientURI = at, "https://agent.example"
	require.NoError(t, repo.SaveClient(ctx, doc))

	got, err := repo.Client(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, doc, got)

	// Fetched again: its name and redirects are replaced, the day it came is not.
	again := doc
	again.Name, again.RedirectURIs, again.FetchedAt, again.CreatedAt = "Renamed", []string{"https://agent.example/cb"}, at.Add(time.Hour), at.Add(time.Hour)
	require.NoError(t, repo.SaveClient(ctx, again))

	got, err = repo.Client(ctx, doc.ID)
	require.NoError(t, err)
	assert.Equal(t, "Renamed", got.Name)
	assert.Equal(t, []string{"https://agent.example/cb"}, got.RedirectURIs)
	assert.Equal(t, at.Add(time.Hour), got.FetchedAt)
	assert.Equal(t, at, got.CreatedAt)

	unused := oauthClient("pc_unused", domain.ClientRegistered, at)
	used := oauthClient("pc_used", domain.ClientRegistered, at)
	recent := oauthClient("pc_recent", domain.ClientRegistered, at.Add(2*time.Hour))
	stale := oauthClient("https://stale.example/client.json", domain.ClientDocument, at)
	staleUsed := oauthClient("https://used.example/client.json", domain.ClientDocument, at)
	stale.FetchedAt, staleUsed.FetchedAt = at, at

	for _, c := range []domain.OAuthClient{unused, used, recent, stale, staleUsed} {
		require.NoError(t, repo.SaveClient(ctx, c))
	}

	_, err = repo.SaveGrant(ctx, oauthGrant(u.ID, used, at, domain.ScopeRead))
	require.NoError(t, err)
	_, err = repo.SaveGrant(ctx, oauthGrant(u.ID, staleUsed, at, domain.ScopeRead))
	require.NoError(t, err)

	n, err := repo.PruneClients(ctx, at.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	_, err = repo.Client(ctx, unused.ID)
	require.ErrorIs(t, err, domain.ErrNotFound, "registered long ago, no grant")
	_, err = repo.Client(ctx, stale.ID)
	require.ErrorIs(t, err, domain.ErrNotFound, "fetched long ago, no grant (A13)")

	for _, id := range []string{used.ID, recent.ID, doc.ID, staleUsed.ID} {
		_, err = repo.Client(ctx, id)
		require.NoError(t, err, id)
	}
}

// A user has one grant to a client: allowing again replaces its scopes, revoking ends it
// for that user only and drops its codes and tokens.
func TestOAuthGrants(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewOAuth(db)
	u := seedUser(t, db, "a@x.io", domain.RoleUser)
	other := seedUser(t, db, "b@x.io", domain.RoleUser)

	first := oauthClient("pc_first", domain.ClientRegistered, at)
	second := oauthClient("pc_second", domain.ClientRegistered, at)
	require.NoError(t, repo.SaveClient(ctx, first))
	require.NoError(t, repo.SaveClient(ctx, second))

	_, err := repo.GrantOf(ctx, u.ID, first.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)

	g, err := repo.SaveGrant(ctx, oauthGrant(u.ID, first, at, domain.ScopeRead, domain.ScopeBuild))
	require.NoError(t, err)
	assert.Equal(t, first.Name, g.Client.Name)
	assert.Equal(t, []domain.Scope{domain.ScopeRead, domain.ScopeBuild}, g.Scopes)
	assert.Equal(t, mcpURL, g.Resource)
	assert.True(t, g.Active())

	// Allowed again: the same grant, its scopes replaced.
	again, err := repo.SaveGrant(ctx, oauthGrant(u.ID, first, at.Add(time.Hour), domain.ScopePaper))
	require.NoError(t, err)
	assert.Equal(t, g.ID, again.ID)
	assert.Equal(t, []domain.Scope{domain.ScopePaper}, again.Scopes)
	assert.Equal(t, at, again.CreatedAt)
	assert.Equal(t, at.Add(time.Hour), again.UpdatedAt)

	newer, err := repo.SaveGrant(ctx, oauthGrant(u.ID, second, at.Add(time.Minute), domain.ScopeRead))
	require.NoError(t, err)

	_, err = repo.SaveGrant(ctx, oauthGrant(other.ID, first, at, domain.ScopeRead))
	require.NoError(t, err)

	list, err := repo.Grants(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, list, 2, "the user's only")
	assert.Equal(t, []domain.GrantID{newer.ID, g.ID}, []domain.GrantID{list[0].ID, list[1].ID}, "newest first")

	require.NoError(t, repo.TouchGrant(ctx, g.ID, at.Add(2*time.Hour)))

	got, err := repo.Grant(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, at.Add(2*time.Hour), got.LastUsedAt)

	code := domain.AuthCode{GrantID: g.ID, RedirectURI: "http://127.0.0.1/callback", Challenge: "c", Scopes: g.Scopes, Resource: mcpURL, ExpiresAt: at.Add(time.Hour)}
	require.NoError(t, repo.SaveCode(ctx, []byte("code"), code))
	require.NoError(t, repo.SaveToken(ctx, []byte("access"), domain.OAuthToken{GrantID: g.ID, Kind: domain.AccessToken, Scopes: g.Scopes, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}))

	require.ErrorIs(t, repo.RevokeGrant(ctx, other.ID, g.ID, at.Add(3*time.Hour)), domain.ErrNotFound, "another user's")
	require.ErrorIs(t, repo.RevokeGrant(ctx, u.ID, domain.GrantID(newID()), at), domain.ErrNotFound)

	require.NoError(t, repo.RevokeGrant(ctx, u.ID, g.ID, at.Add(3*time.Hour)))
	require.NoError(t, repo.RevokeGrant(ctx, "", g.ID, at.Add(4*time.Hour)), "again, by anyone")

	got, err = repo.Grant(ctx, g.ID)
	require.NoError(t, err)
	assert.False(t, got.Active())
	assert.Equal(t, at.Add(3*time.Hour), got.RevokedAt, "revoked when first revoked")

	_, err = repo.Token(ctx, []byte("access"))
	require.ErrorIs(t, err, domain.ErrNotFound, "its tokens went with it")

	_, err = repo.TakeCode(ctx, []byte("code"), at)
	require.ErrorIs(t, err, domain.ErrNotFound, "its codes too")

	// A token given just as the grant was revoked does not come back with it.
	require.NoError(t, repo.SaveToken(ctx, []byte("late"), domain.OAuthToken{GrantID: g.ID, Kind: domain.AccessToken, Scopes: g.Scopes, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}))

	revived, err := repo.SaveGrant(ctx, oauthGrant(u.ID, first, at.Add(5*time.Hour), domain.ScopeRead))
	require.NoError(t, err)
	assert.Equal(t, g.ID, revived.ID)
	assert.True(t, revived.Active())

	_, err = repo.Token(ctx, []byte("late"))
	require.ErrorIs(t, err, domain.ErrNotFound)

	// Allowing an active grant again keeps its tokens: another of the user's machines
	// may be using them.
	require.NoError(t, repo.SaveToken(ctx, []byte("kept"), domain.OAuthToken{GrantID: g.ID, Kind: domain.AccessToken, Scopes: g.Scopes, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}))
	_, err = repo.SaveGrant(ctx, oauthGrant(u.ID, first, at.Add(6*time.Hour), domain.ScopeRead, domain.ScopeBuild))
	require.NoError(t, err)

	_, err = repo.Token(ctx, []byte("kept"))
	require.NoError(t, err)
}

// A code and a refresh token are taken once; a second take finds them used, which is how
// a copy is caught.
func TestOAuthCodesAndTokens(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewOAuth(db)
	u := seedUser(t, db, "a@x.io", domain.RoleUser)

	c := oauthClient("pc_agent", domain.ClientRegistered, at)
	require.NoError(t, repo.SaveClient(ctx, c))

	g, err := repo.SaveGrant(ctx, oauthGrant(u.ID, c, at, domain.ScopeRead))
	require.NoError(t, err)

	code := domain.AuthCode{
		GrantID: g.ID, RedirectURI: "http://127.0.0.1:4000/callback", Challenge: "challenge",
		Scopes: []domain.Scope{domain.ScopeRead}, Resource: mcpURL, ExpiresAt: at.Add(10 * time.Minute),
	}
	require.NoError(t, repo.SaveCode(ctx, []byte("code"), code))

	_, err = repo.TakeCode(ctx, []byte("other"), at)
	require.ErrorIs(t, err, domain.ErrNotFound)

	got, err := repo.TakeCode(ctx, []byte("code"), at.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, code, got, "unused until now")

	got, err = repo.TakeCode(ctx, []byte("code"), at.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, at.Add(time.Minute), got.UsedAt, "used, when first")

	access := domain.OAuthToken{GrantID: g.ID, Kind: domain.AccessToken, Scopes: g.Scopes, CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	refresh := domain.OAuthToken{GrantID: g.ID, Kind: domain.RefreshToken, Scopes: g.Scopes, CreatedAt: at, ExpiresAt: at.Add(30 * 24 * time.Hour)}
	require.NoError(t, repo.SaveToken(ctx, []byte("access"), access))
	require.NoError(t, repo.SaveToken(ctx, []byte("refresh"), refresh))

	tok, err := repo.Token(ctx, []byte("access"))
	require.NoError(t, err)
	assert.Equal(t, access, tok)

	_, err = repo.TakeRefresh(ctx, []byte("access"), at)
	require.ErrorIs(t, err, domain.ErrNotFound, "an access token is not a refresh token")

	tok, err = repo.TakeRefresh(ctx, []byte("refresh"), at.Add(time.Minute))
	require.NoError(t, err)
	assert.Equal(t, refresh, tok)

	tok, err = repo.TakeRefresh(ctx, []byte("refresh"), at.Add(2*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, at.Add(time.Minute), tok.UsedAt)

	// The code and the access token have ended an hour on; the refresh token has not.
	n, err := repo.PruneTokens(ctx, at.Add(time.Hour+time.Second))
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	_, err = repo.Token(ctx, []byte("access"))
	require.ErrorIs(t, err, domain.ErrNotFound)

	_, err = repo.Token(ctx, []byte("refresh"))
	require.NoError(t, err)
}

// Of two exchanges of one code at once, the second waits for the first and finds the code
// used: both cannot get tokens.
func TestOAuthTakeCodeRace(t *testing.T) {
	ctx := context.Background()
	db := fresh(t)
	repo := postgres.NewOAuth(db)
	u := seedUser(t, db, "a@x.io", domain.RoleUser)

	c := oauthClient("pc_agent", domain.ClientRegistered, at)
	require.NoError(t, repo.SaveClient(ctx, c))

	g, err := repo.SaveGrant(ctx, oauthGrant(u.ID, c, at, domain.ScopeRead))
	require.NoError(t, err)

	require.NoError(t, repo.SaveCode(ctx, []byte("code"), domain.AuthCode{
		GrantID: g.ID, RedirectURI: "http://127.0.0.1/callback", Challenge: "c", Scopes: g.Scopes, Resource: mcpURL, ExpiresAt: at.Add(time.Hour),
	}))
	require.NoError(t, repo.SaveToken(ctx, []byte("refresh"), domain.OAuthToken{
		GrantID: g.ID, Kind: domain.RefreshToken, Scopes: g.Scopes, CreatedAt: at, ExpiresAt: at.Add(time.Hour),
	}))

	for name, take := range map[string]func(context.Context) (time.Time, error){
		"code": func(ctx context.Context) (time.Time, error) {
			code, err := repo.TakeCode(ctx, []byte("code"), at)
			return code.UsedAt, err
		},
		"refresh": func(ctx context.Context) (time.Time, error) {
			tok, err := repo.TakeRefresh(ctx, []byte("refresh"), at)
			return tok.UsedAt, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			second := make(chan time.Time, 1)

			require.NoError(t, db.WithinTx(ctx, func(ctx context.Context) error {
				used, err := take(ctx)
				require.NoError(t, err)
				assert.True(t, used.IsZero(), "the first finds it unused")

				// The second starts while the first holds the row, and waits for it.
				go func() {
					used, err := take(context.Background())
					assert.NoError(t, err)
					second <- used
				}()

				time.Sleep(200 * time.Millisecond)

				return nil
			}))

			select {
			case used := <-second:
				assert.Equal(t, at, used, "the second finds it used")
			case <-time.After(10 * time.Second):
				t.Fatal("the second take never returned")
			}
		})
	}
}
