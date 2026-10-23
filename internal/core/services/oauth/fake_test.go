package oauth

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.OAuthRepository = (*fakeRepo)(nil)

// fakeRepo keeps the OAuth server's rows in memory as the Postgres repository does, so a
// test can go through a whole sign-in; the Postgres tests check the real one.
type fakeRepo struct {
	mu      sync.Mutex
	clients map[string]domain.OAuthClient
	grants  map[domain.GrantID]domain.Grant
	codes   map[string]domain.AuthCode
	tokens  map[string]domain.OAuthToken
	fail    error // every call's answer, when set
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		clients: map[string]domain.OAuthClient{},
		grants:  map[domain.GrantID]domain.Grant{},
		codes:   map[string]domain.AuthCode{},
		tokens:  map[string]domain.OAuthToken{},
	}
}

func (r *fakeRepo) SaveClient(_ context.Context, c domain.OAuthClient) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fail != nil {
		return r.fail
	}

	if kept, ok := r.clients[c.ID]; ok {
		c.CreatedAt, c.Kind = kept.CreatedAt, kept.Kind
	}

	r.clients[c.ID] = c

	return nil
}

func (r *fakeRepo) Client(_ context.Context, id string) (domain.OAuthClient, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fail != nil {
		return domain.OAuthClient{}, r.fail
	}

	c, ok := r.clients[id]
	if !ok {
		return domain.OAuthClient{}, domain.ErrNotFound
	}

	return c, nil
}

func (r *fakeRepo) PruneClients(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var n int64

	for id, c := range r.clients {
		used := false
		for _, g := range r.grants {
			used = used || g.Client.ID == id
		}

		if c.Kind == domain.ClientRegistered && c.CreatedAt.Before(before) && !used {
			delete(r.clients, id)
			n++
		}
	}

	return n, nil
}

func (r *fakeRepo) SaveGrant(_ context.Context, g domain.Grant) (domain.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for id, kept := range r.grants {
		if kept.UserID != g.UserID || kept.Client.ID != g.Client.ID {
			continue
		}

		if !kept.Active() {
			r.dropIssued(id)
		}

		kept.Scopes, kept.Resource, kept.UpdatedAt, kept.RevokedAt = g.Scopes, g.Resource, g.UpdatedAt, time.Time{}
		r.grants[id] = kept

		return r.withClient(kept), nil
	}

	r.grants[g.ID] = g

	return r.withClient(g), nil
}

func (r *fakeRepo) withClient(g domain.Grant) domain.Grant {
	g.Client = r.clients[g.Client.ID]
	g.Scopes = slices.Clone(g.Scopes)

	return g
}

func (r *fakeRepo) Grant(_ context.Context, id domain.GrantID) (domain.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.grants[id]
	if !ok {
		return domain.Grant{}, domain.ErrNotFound
	}

	return r.withClient(g), nil
}

func (r *fakeRepo) GrantOf(_ context.Context, user domain.UserID, client string) (domain.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.fail != nil {
		return domain.Grant{}, r.fail
	}

	for _, g := range r.grants {
		if g.UserID == user && g.Client.ID == client {
			return r.withClient(g), nil
		}
	}

	return domain.Grant{}, domain.ErrNotFound
}

func (r *fakeRepo) Grants(_ context.Context, user domain.UserID) ([]domain.Grant, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []domain.Grant

	for _, g := range r.grants {
		if g.UserID == user {
			out = append(out, r.withClient(g))
		}
	}

	slices.SortFunc(out, func(a, b domain.Grant) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return out, nil
}

func (r *fakeRepo) RevokeGrant(_ context.Context, user domain.UserID, id domain.GrantID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	g, ok := r.grants[id]
	if !ok || (user != "" && g.UserID != user) {
		return domain.ErrNotFound
	}

	if g.RevokedAt.IsZero() {
		g.RevokedAt = at
	}

	r.grants[id] = g
	r.dropIssued(id)

	return nil
}

func (r *fakeRepo) dropIssued(id domain.GrantID) {
	for h, c := range r.codes {
		if c.GrantID == id {
			delete(r.codes, h)
		}
	}

	for h, t := range r.tokens {
		if t.GrantID == id {
			delete(r.tokens, h)
		}
	}
}

func (r *fakeRepo) TouchGrant(_ context.Context, id domain.GrantID, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	g := r.grants[id]
	g.LastUsedAt = at
	r.grants[id] = g

	return nil
}

func (r *fakeRepo) SaveCode(_ context.Context, hash []byte, code domain.AuthCode) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.codes[string(hash)] = code

	return nil
}

func (r *fakeRepo) TakeCode(_ context.Context, hash []byte, at time.Time) (domain.AuthCode, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	c, ok := r.codes[string(hash)]
	if !ok {
		return domain.AuthCode{}, domain.ErrNotFound
	}

	if c.UsedAt.IsZero() {
		used := c
		used.UsedAt = at
		r.codes[string(hash)] = used
	}

	return c, nil
}

func (r *fakeRepo) SaveToken(_ context.Context, hash []byte, t domain.OAuthToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tokens[string(hash)] = t

	return nil
}

func (r *fakeRepo) TakeRefresh(_ context.Context, hash []byte, at time.Time) (domain.OAuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.tokens[string(hash)]
	if !ok || t.Kind != domain.RefreshToken {
		return domain.OAuthToken{}, domain.ErrNotFound
	}

	if t.UsedAt.IsZero() {
		used := t
		used.UsedAt = at
		r.tokens[string(hash)] = used
	}

	return t, nil
}

func (r *fakeRepo) Token(_ context.Context, hash []byte) (domain.OAuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	t, ok := r.tokens[string(hash)]
	if !ok {
		return domain.OAuthToken{}, domain.ErrNotFound
	}

	return t, nil
}

func (r *fakeRepo) PruneTokens(_ context.Context, before time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var n int64

	for h, c := range r.codes {
		if c.ExpiresAt.Before(before) {
			delete(r.codes, h)
			n++
		}
	}

	for h, t := range r.tokens {
		if t.ExpiresAt.Before(before) {
			delete(r.tokens, h)
			n++
		}
	}

	return n, nil
}

// tokensOf counts a grant's tokens of a kind.
func (r *fakeRepo) tokensOf(id domain.GrantID, kind domain.OAuthTokenKind) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0

	for _, t := range r.tokens {
		if t.GrantID == id && t.Kind == kind {
			n++
		}
	}

	return n
}

var errDown = errors.New("database down")
