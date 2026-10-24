package credentials_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/credentials"
)

var (
	now   = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "alice"}, SessionID: "s-1"}
	bob   = auth.Principal{User: domain.User{ID: "bob"}, SessionID: "s-2"}
)

// apiKey has a value shown in the clear, a required secret and an optional one.
var apiKey = engine.CredentialType{
	Name:  "test.api",
	Label: "Test API",
	Fields: []engine.CredentialField{
		{Name: "account", Label: "Account", Required: true},
		{Name: "key", Label: "Key", Required: true, Secret: true},
		{Name: "extra", Label: "Extra", Secret: true},
	},
	Check: func(v map[string]string) error {
		if strings.HasPrefix(v["key"], "bad") {
			return errors.New("that key is malformed")
		}

		return nil
	},
	Test: func(ctx context.Context, v map[string]string) error {
		if v["key"] != "good-key" {
			return errors.New("the service refused the key")
		}

		return nil
	},
}

var untestable = engine.CredentialType{
	Name: "test.plain", Label: "Plain", Fields: []engine.CredentialField{{Name: "token", Label: "Token", Secret: true}},
}

// memRepo keeps credentials in memory, scoped by owner like the real one.
type memRepo struct {
	rows map[domain.CredentialID]ports.SealedCredential
	err  error
}

func (r *memRepo) Create(_ context.Context, c ports.SealedCredential) error {
	if r.err != nil {
		return r.err
	}

	for _, row := range r.rows {
		if row.Credential.OwnerID == c.Credential.OwnerID && row.Credential.Name == c.Credential.Name {
			return domain.ErrCredentialExists
		}
	}

	r.rows[c.Credential.ID] = c

	return nil
}

func (r *memRepo) Update(_ context.Context, c ports.SealedCredential) error {
	if _, err := r.Get(context.Background(), c.Credential.OwnerID, c.Credential.ID); err != nil {
		return err
	}

	r.rows[c.Credential.ID] = c

	return nil
}

func (r *memRepo) Get(_ context.Context, owner domain.UserID, id domain.CredentialID) (ports.SealedCredential, error) {
	c, ok := r.rows[id]
	if !ok || c.Credential.OwnerID != owner {
		return ports.SealedCredential{}, domain.ErrNotFound
	}

	return c, nil
}

func (r *memRepo) List(_ context.Context, owner domain.UserID) ([]domain.Credential, error) {
	var out []domain.Credential

	for _, row := range r.rows {
		if row.Credential.OwnerID == owner {
			out = append(out, row.Credential)
		}
	}

	slices.SortFunc(out, func(a, b domain.Credential) int { return strings.Compare(a.Name, b.Name) })

	return out, nil
}

func (r *memRepo) CountForOwner(ctx context.Context, owner domain.UserID) (int, error) {
	list, err := r.List(ctx, owner)
	return len(list), err
}

func (r *memRepo) Delete(_ context.Context, owner domain.UserID, id domain.CredentialID) error {
	if c, ok := r.rows[id]; !ok || c.Credential.OwnerID != owner {
		return domain.ErrNotFound
	}

	delete(r.rows, id)

	return nil
}

func (r *memRepo) RecordTest(_ context.Context, owner domain.UserID, id domain.CredentialID, at time.Time, testErr string) error {
	c, ok := r.rows[id]
	if !ok || c.Credential.OwnerID != owner {
		return domain.ErrNotFound
	}

	c.Credential.TestedAt, c.Credential.TestError = at, testErr
	r.rows[id] = c

	return nil
}

// box "encrypts" by prefixing the associated data, so a wrong credential shows.
type box struct{}

func (box) Seal(_ context.Context, plaintext, aad []byte) ([]byte, error) {
	return append(append(slices.Clone(aad), '|'), plaintext...), nil
}

func (box) Open(_ context.Context, sealed, aad []byte) ([]byte, error) {
	rest, ok := bytes.CutPrefix(sealed, append(slices.Clone(aad), '|'))
	if !ok {
		return nil, errors.New("cannot decrypt")
	}

	return rest, nil
}

func newService(t *testing.T) (*credentials.Service, *memRepo) {
	t.Helper()

	types := engine.NewRegistry()
	require.NoError(t, types.RegisterCredentialType(apiKey))
	require.NoError(t, types.RegisterCredentialType(untestable))

	repo := &memRepo{rows: make(map[domain.CredentialID]ports.SealedCredential)}

	return credentials.New(types, repo, box{}, logging.Noop(), credentials.WithClock(func() time.Time { return now })), repo
}

func create(t *testing.T, s *credentials.Service, p auth.Principal, name string, values map[string]string) domain.Credential {
	t.Helper()

	c, err := s.Create(context.Background(), p, credentials.Input{Type: "test.api", Name: name, Values: values})
	require.NoError(t, err)

	return c
}

func problems(t *testing.T, err error) map[string]string {
	t.Helper()

	var v *domain.ValidationError
	require.ErrorAs(t, err, &v)
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	out := make(map[string]string)
	for _, p := range v.Problems {
		out[p.Param] = p.Message
	}

	return out
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("keeps secrets sealed and the rest in the clear", func(t *testing.T) {
		s, repo := newService(t)

		c := create(t, s, alice, "  Main  ", map[string]string{"account": " acct-1 ", "key": " good-key\n"})
		assert.Equal(t, "Main", c.Name)
		assert.Equal(t, map[string]string{"account": "acct-1"}, c.Fields)
		assert.Equal(t, []string{"key"}, c.Secrets)
		assert.Equal(t, now, c.CreatedAt)

		stored := repo.rows[c.ID]
		assert.Equal(t, `credential:`+string(c.ID)+`|{"key":"good-key"}`, string(stored.Sealed))
		assert.NotContains(t, stored.Credential.Fields, "key")

		_, values, err := s.Values(ctx, "alice", c.ID, []string{"test.api"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"account": "acct-1", "key": "good-key"}, values)
	})

	t.Run("reports every problem", func(t *testing.T) {
		s, repo := newService(t)

		_, err := s.Create(ctx, alice, credentials.Input{Type: "test.api", Name: " ", Values: map[string]string{"color": "red"}})
		assert.Equal(t, map[string]string{
			"name":           "a name of 1 to 64 characters",
			"values.color":   `Test API has no field "color"`,
			"values.account": "Account is required",
			"values.key":     "Key is required",
		}, problems(t, err))

		_, err = s.Create(ctx, alice, credentials.Input{Type: "test.api", Name: "x", Values: map[string]string{"account": "a", "key": strings.Repeat("k", 4097)}})
		assert.Equal(t, map[string]string{"values.key": "Key is longer than 4096 characters"}, problems(t, err))

		_, err = s.Create(ctx, alice, credentials.Input{Type: "test.api", Name: "x", Values: map[string]string{"account": "a", "key": "bad-key"}})
		assert.Equal(t, map[string]string{"": "that key is malformed"}, problems(t, err), "the type's own check")

		assert.Empty(t, repo.rows)
	})

	t.Run("unknown types, taken names and the limit", func(t *testing.T) {
		s, repo := newService(t)

		_, err := s.Create(ctx, alice, credentials.Input{Type: "nope", Name: "x"})
		require.ErrorIs(t, err, domain.ErrInvalidInput)

		create(t, s, alice, "Main", map[string]string{"account": "a", "key": "k"})
		_, err = s.Create(ctx, alice, credentials.Input{Type: "test.api", Name: "Main", Values: map[string]string{"account": "a", "key": "k"}})
		require.ErrorIs(t, err, domain.ErrCredentialExists)

		create(t, s, bob, "Main", map[string]string{"account": "b", "key": "k"}) // names are per user

		for i := range 99 {
			repo.rows[domain.CredentialID(rune(i))] = ports.SealedCredential{Credential: domain.Credential{ID: domain.CredentialID(rune(i)), OwnerID: "alice", Name: string(rune('a' + i))}}
		}

		_, err = s.Create(ctx, alice, credentials.Input{Type: "test.api", Name: "One more", Values: map[string]string{"account": "a", "key": "k"}})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.ErrorContains(t, err, "at most 100 credentials per user")
	})
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("an empty secret keeps the saved one", func(t *testing.T) {
		s, _ := newService(t)
		c := create(t, s, alice, "Main", map[string]string{"account": "a", "key": "good-key", "extra": "x"})

		_, err := s.Test(ctx, alice, c.ID)
		require.NoError(t, err)

		updated, err := s.Update(ctx, alice, c.ID, credentials.Change{Name: "Renamed", Values: map[string]string{"key": " "}})
		require.NoError(t, err)
		assert.Equal(t, "Renamed", updated.Name)
		assert.Equal(t, []string{"key", "extra"}, updated.Secrets)
		assert.Equal(t, now, updated.TestedAt, "same values, so the test still holds")

		_, values, err := s.Values(ctx, "alice", c.ID, []string{"test.api"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"account": "a", "key": "good-key", "extra": "x"}, values)
	})

	t.Run("new values replace old ones and forget the last test", func(t *testing.T) {
		s, _ := newService(t)
		c := create(t, s, alice, "Main", map[string]string{"account": "a", "key": "good-key"})
		_, err := s.Test(ctx, alice, c.ID)
		require.NoError(t, err)

		updated, err := s.Update(ctx, alice, c.ID, credentials.Change{Name: "Main", Values: map[string]string{"account": "b", "key": "other-key"}})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"account": "b"}, updated.Fields)
		assert.True(t, updated.TestedAt.IsZero())

		_, values, err := s.Values(ctx, "alice", c.ID, []string{"test.api"})
		require.NoError(t, err)
		assert.Equal(t, "other-key", values["key"])
	})

	t.Run("a required value cannot be emptied", func(t *testing.T) {
		s, _ := newService(t)
		c := create(t, s, alice, "Main", map[string]string{"account": "a", "key": "k"})

		_, err := s.Update(ctx, alice, c.ID, credentials.Change{Name: "Main", Values: map[string]string{"account": ""}})
		assert.Equal(t, map[string]string{"values.account": "Account is required"}, problems(t, err))
	})

	t.Run("only the owner's", func(t *testing.T) {
		s, _ := newService(t)
		c := create(t, s, alice, "Main", map[string]string{"account": "a", "key": "k"})

		_, err := s.Update(ctx, bob, c.ID, credentials.Change{Name: "Mine now"})
		require.ErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestTest(t *testing.T) {
	ctx := context.Background()
	s, repo := newService(t)

	good := create(t, s, alice, "Good", map[string]string{"account": "a", "key": "good-key"})
	tested, err := s.Test(ctx, alice, good.ID)
	require.NoError(t, err)
	assert.Equal(t, now, tested.TestedAt)
	assert.Empty(t, tested.TestError)

	refused := create(t, s, alice, "Refused", map[string]string{"account": "a", "key": "wrong-key"})
	tested, err = s.Test(ctx, alice, refused.ID)
	require.NoError(t, err, "a failed test is a result, not an error")
	assert.Equal(t, "the service refused the key", tested.TestError)
	assert.Equal(t, "the service refused the key", repo.rows[refused.ID].Credential.TestError)

	plain, err := s.Create(ctx, alice, credentials.Input{Type: "test.plain", Name: "Plain", Values: map[string]string{"token": "t"}})
	require.NoError(t, err)
	_, err = s.Test(ctx, alice, plain.ID)
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	_, err = s.Test(ctx, bob, good.ID)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestValuesForNodes(t *testing.T) {
	ctx := context.Background()
	s, repo := newService(t)
	c := create(t, s, alice, "Main", map[string]string{"account": "a", "key": "k"})

	_, _, err := s.Values(ctx, "bob", c.ID, []string{"test.api"})
	require.ErrorIs(t, err, domain.ErrNotFound, "another user's credential does not exist for them")

	_, _, err = s.Values(ctx, "alice", c.ID, []string{"test.plain"})
	require.ErrorIs(t, err, domain.ErrInvalidInput)
	assert.ErrorContains(t, err, `"Main" is a test.api credential, not test.plain`)

	_, _, err = s.Values(ctx, "alice", c.ID, []string{"test.plain", "test.other"})
	assert.ErrorContains(t, err, "not test.plain or test.other")

	credType, values, err := s.Values(ctx, "alice", c.ID, []string{"test.plain", "test.api"})
	require.NoError(t, err)
	assert.Equal(t, "test.api", credType, "one of several, and which")
	assert.Equal(t, "k", values["key"])

	// Sealed values copied onto another credential cannot be opened there.
	other := create(t, s, alice, "Other", map[string]string{"account": "a", "key": "k2"})
	moved := repo.rows[other.ID]
	moved.Sealed = repo.rows[c.ID].Sealed
	repo.rows[other.ID] = moved

	_, _, err = s.Values(ctx, "alice", other.ID, []string{"test.api"})
	require.ErrorContains(t, err, "cannot decrypt")
}

func TestListAndDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)

	create(t, s, alice, "B", map[string]string{"account": "a", "key": "k"})
	a := create(t, s, alice, "A", map[string]string{"account": "a", "key": "k"})
	create(t, s, bob, "Bob's", map[string]string{"account": "b", "key": "k"})

	list, err := s.List(ctx, alice)
	require.NoError(t, err)
	assert.Equal(t, []string{"A", "B"}, names(list))

	require.ErrorIs(t, s.Delete(ctx, bob, a.ID), domain.ErrNotFound)
	require.NoError(t, s.Delete(ctx, alice, a.ID))

	list, err = s.List(ctx, alice)
	require.NoError(t, err)
	assert.Equal(t, []string{"B"}, names(list))

	var types []string
	for _, typ := range s.Types() {
		types = append(types, typ.Name)
	}

	assert.Equal(t, []string{"test.api", "test.plain"}, types)
}

func names(list []domain.Credential) []string {
	out := make([]string, len(list))
	for i, c := range list {
		out[i] = c.Name
	}

	return out
}

// An agent or a token deletes a credential, tests one or changes where its secret goes
// only where its user lets it (D404); naming one or giving it a new secret is theirs.
func TestAgentsNeedTheSwitches(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t)

	c := create(t, s, alice, "Main", map[string]string{"account": "a", "key": "good-key"})

	agent := auth.Principal{User: domain.User{ID: "alice"}, GrantID: "g-1", Scopes: domain.Scopes}

	require.ErrorIs(t, s.Delete(ctx, agent, c.ID), domain.ErrForbidden)

	_, err := s.Test(ctx, agent, c.ID)
	require.ErrorIs(t, err, domain.ErrForbidden)

	_, err = s.Update(ctx, agent, c.ID, credentials.Change{Name: "Main", Values: map[string]string{"account": "elsewhere"}})
	require.ErrorIs(t, err, domain.ErrForbidden)
	assert.Contains(t, err.Error(), "changing Account needs the user to let agents and tokens use credentials")

	renamed, err := s.Update(ctx, agent, c.ID, credentials.Change{Name: "Renamed", Values: map[string]string{"key": "good-key-2"}})
	require.NoError(t, err)
	assert.Equal(t, "Renamed", renamed.Name)

	agent.User.Agents = domain.AgentAccess{Delete: true, UseCredentials: true}

	_, err = s.Update(ctx, agent, c.ID, credentials.Change{Name: "Renamed", Values: map[string]string{"account": "elsewhere"}})
	require.NoError(t, err)

	tested, err := s.Test(ctx, agent, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "the service refused the key", tested.TestError)

	require.NoError(t, s.Delete(ctx, agent, c.ID))
}
