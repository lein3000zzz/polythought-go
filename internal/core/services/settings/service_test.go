package settings_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/settings"
)

var (
	now   = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	alice = auth.Principal{User: domain.User{ID: "alice", Role: domain.RoleUser}, SessionID: "s-1"}
	root  = auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}, SessionID: "s-2"}
)

const (
	keyRegistration = "auth.registration"
	keyMaxWorkflows = "workflows.max_per_user"
	keyLogFormat    = "log.format"
	keyToken        = "telegram.token"
	keyProxies      = "http.trusted_proxies"
	keyRetention    = "executions.retention"
)

func registry(t *testing.T) *settings.Registry {
	t.Helper()

	r := settings.NewRegistry()
	require.NoError(t, r.Register(
		settings.Spec{Key: keyRegistration, Label: "Registration", Kind: settings.KindEnum, Options: []string{"open", "invite", "closed"}, Default: "invite", Env: "POLYTHOUGHT_REGISTRATION"},
		settings.Spec{Key: keyMaxWorkflows, Label: "Workflows per user", Kind: settings.KindInt, Default: 100, Check: func(v any) error {
			if v.(int) < 1 {
				return errors.New("must be at least 1")
			}

			return nil
		}},
		settings.Spec{Key: keyLogFormat, Label: "Log format", Kind: settings.KindEnum, Options: []string{"text", "json"}, Default: "text", Apply: settings.ApplyRestart},
		settings.Spec{Key: keyToken, Label: "Bot token", Kind: settings.KindString, Secret: true, Default: ""},
		settings.Spec{Key: keyProxies, Label: "Trusted proxies", Kind: settings.KindList, Default: []string{}},
		settings.Spec{Key: keyRetention, Label: "Retention", Kind: settings.KindDuration, Default: 720 * time.Hour},
	))

	return r
}

// memRepo keeps settings in memory and records what was applied.
type memRepo struct {
	rows    map[string]ports.StoredSetting
	applied [][]ports.SettingChange
	actor   domain.UserID
	at      time.Time
	err     error
}

func newRepo(rows ...ports.StoredSetting) *memRepo {
	r := &memRepo{rows: make(map[string]ports.StoredSetting)}
	for _, row := range rows {
		r.rows[row.Key] = row
	}

	return r
}

func (r *memRepo) All(context.Context) ([]ports.StoredSetting, error) {
	if r.err != nil {
		return nil, r.err
	}

	return slices.Collect(maps.Values(r.rows)), nil
}

func (r *memRepo) Apply(_ context.Context, changes []ports.SettingChange, actor domain.UserID, at time.Time) error {
	if r.err != nil {
		return r.err
	}

	for _, c := range changes {
		if c.Reset {
			delete(r.rows, c.Key)
		} else {
			r.rows[c.Key] = ports.StoredSetting{Key: c.Key, Value: c.Value, Sealed: c.Sealed}
		}
	}

	r.applied = append(r.applied, changes)
	r.actor, r.at = actor, at

	return nil
}

func (r *memRepo) History(context.Context, ports.PageFilter) (ports.Page[ports.SettingAudit], error) {
	return ports.Page[ports.SettingAudit]{Items: []ports.SettingAudit{{Key: keyRegistration}}}, nil
}

// box "encrypts" by prefixing the associated data, so a wrong key or row shows.
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

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func load(t *testing.T, repo *memRepo, vars map[string]string, opts ...settings.Option) *settings.Service {
	t.Helper()

	s := settings.New(registry(t), repo, box{}, append([]settings.Option{settings.WithClock(func() time.Time { return now })}, opts...)...)

	warnings, err := s.Load(context.Background(), env(vars))
	require.NoError(t, err)
	require.Empty(t, warnings)

	return s
}

func saved(key, value string) ports.StoredSetting {
	return ports.StoredSetting{Key: key, Value: json.RawMessage(value)}
}

func sealed(key, plaintext string) ports.StoredSetting {
	return ports.StoredSetting{Key: key, Sealed: []byte("setting:" + key + "|" + plaintext)}
}

func TestLoadLayers(t *testing.T) {
	t.Parallel()

	repo := newRepo(
		saved(keyRegistration, `"open"`),
		saved(keyMaxWorkflows, `5`),
		saved(keyProxies, `["10.0.0.0/8"]`),
		sealed(keyToken, "123:abc"),
	)

	s := load(t, repo, map[string]string{
		"POLYTHOUGHT_REGISTRATION":   "closed",
		"POLYTHOUGHT_LOG_FORMAT":     " ", // empty means unset
		"POLYTHOUGHT_AUTH_REGISTRAT": "ignored",
	})

	cur := s.Current()

	assert.Equal(t, "closed", cur.String(keyRegistration), "the environment beats a saved value")
	assert.Equal(t, settings.SourceEnvironment, cur.Source(keyRegistration))
	assert.Equal(t, 5, cur.Int(keyMaxWorkflows), "a saved value beats the default")
	assert.Equal(t, settings.SourceDatabase, cur.Source(keyMaxWorkflows))
	assert.Equal(t, "text", cur.String(keyLogFormat))
	assert.Equal(t, settings.SourceDefault, cur.Source(keyLogFormat))
	assert.Equal(t, []string{"10.0.0.0/8"}, cur.Strings(keyProxies))
	assert.Equal(t, "123:abc", cur.String(keyToken))
	assert.Equal(t, 720*time.Hour, cur.Duration(keyRetention))

	assert.Panics(t, func() { cur.Bool("no.such") })
}

func TestLoadEnvironmentErrors(t *testing.T) {
	t.Parallel()

	s := settings.New(registry(t), newRepo(), box{})

	_, err := s.Load(context.Background(), env(map[string]string{
		"POLYTHOUGHT_REGISTRATION":           "sometimes",
		"POLYTHOUGHT_WORKFLOWS_MAX_PER_USER": "0",
	}))
	require.Error(t, err)
	assert.ErrorContains(t, err, `POLYTHOUGHT_REGISTRATION: "sometimes" is not one of open, invite, closed`)
	assert.ErrorContains(t, err, "POLYTHOUGHT_WORKFLOWS_MAX_PER_USER: must be at least 1")
}

func TestLoadRepositoryError(t *testing.T) {
	t.Parallel()

	repo := newRepo()
	repo.err = errors.New("down")

	_, err := settings.New(registry(t), repo, box{}).Load(context.Background(), env(nil))
	assert.ErrorContains(t, err, "down")
}

func TestLoadIgnoresBadSavedValues(t *testing.T) {
	t.Parallel()

	repo := newRepo(
		saved("gone.setting", `1`),
		saved(keyMaxWorkflows, `0`),
		saved(keyRegistration, `{`),
		ports.StoredSetting{Key: keyRetention},
		ports.StoredSetting{Key: keyToken, Value: json.RawMessage(`"plain"`)},
		saved(keyLogFormat, `"json"`),
	)
	repo.rows[keyProxies] = ports.StoredSetting{Key: keyProxies, Sealed: []byte("setting:other|x")}

	s := settings.New(registry(t), repo, box{})

	warnings, err := s.Load(context.Background(), env(nil))
	require.NoError(t, err)
	assert.Len(t, warnings, 6)

	cur := s.Current()
	assert.Equal(t, 100, cur.Int(keyMaxWorkflows))
	assert.Equal(t, "invite", cur.String(keyRegistration))
	assert.Empty(t, cur.String(keyToken))
	assert.Equal(t, "json", cur.String(keyLogFormat), "valid rows still load")
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	t.Run("saves, audits and notifies", func(t *testing.T) {
		t.Parallel()

		repo := newRepo(saved(keyMaxWorkflows, `5`))
		s := load(t, repo, nil)

		var got []*settings.Snapshot
		s.Subscribe(func(snap *settings.Snapshot) { got = append(got, snap) })

		err := s.Update(context.Background(), root, map[string]json.RawMessage{
			keyRegistration: json.RawMessage(`"open"`),
			keyMaxWorkflows: json.RawMessage(`null`),
			keyRetention:    json.RawMessage(`"24h"`),
			keyProxies:      json.RawMessage(`["10.0.0.0/8", " "]`),
		})
		require.NoError(t, err)

		cur := s.Current()
		assert.Equal(t, "open", cur.String(keyRegistration))
		assert.Equal(t, settings.SourceDatabase, cur.Source(keyRegistration))
		assert.Equal(t, 100, cur.Int(keyMaxWorkflows))
		assert.Equal(t, settings.SourceDefault, cur.Source(keyMaxWorkflows))
		assert.Equal(t, 24*time.Hour, cur.Duration(keyRetention))
		assert.Equal(t, []string{"10.0.0.0/8"}, cur.Strings(keyProxies))

		require.Len(t, got, 1)
		assert.Same(t, cur, got[0])

		require.Len(t, repo.applied, 1)
		assert.Equal(t, domain.UserID("root"), repo.actor)
		assert.Equal(t, now, repo.at)

		byKey := make(map[string]ports.SettingChange)
		for _, c := range repo.applied[0] {
			byKey[c.Key] = c
		}

		assert.JSONEq(t, `"invite"`, string(byKey[keyRegistration].OldValue))
		assert.JSONEq(t, `"open"`, string(byKey[keyRegistration].NewValue))
		assert.JSONEq(t, `"open"`, string(byKey[keyRegistration].Value))
		assert.True(t, byKey[keyMaxWorkflows].Reset)
		assert.Nil(t, byKey[keyMaxWorkflows].Value)
		assert.JSONEq(t, `5`, string(byKey[keyMaxWorkflows].OldValue))
		assert.JSONEq(t, `100`, string(byKey[keyMaxWorkflows].NewValue))
		assert.JSONEq(t, `"24h"`, string(byKey[keyRetention].Value))

		// What was saved loads back the same.
		again := load(t, repo, nil)
		assert.Equal(t, 24*time.Hour, again.Current().Duration(keyRetention))
		assert.Equal(t, "open", again.Current().String(keyRegistration))
	})

	t.Run("reports every problem and saves nothing", func(t *testing.T) {
		t.Parallel()

		repo := newRepo()
		s := load(t, repo, map[string]string{"POLYTHOUGHT_REGISTRATION": "open"})
		before := s.Current()

		err := s.Update(context.Background(), root, map[string]json.RawMessage{
			keyRegistration: json.RawMessage(`"closed"`),
			keyMaxWorkflows: json.RawMessage(`0`),
			"no.such":       json.RawMessage(`1`),
			keyRetention:    json.RawMessage(`"24h"`),
		})

		var v *domain.ValidationError
		require.ErrorAs(t, err, &v)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.NotErrorIs(t, err, domain.ErrInvalidWorkflow)

		params := make(map[string]string)
		for _, p := range v.Problems {
			params[p.Param] = p.Message
		}

		assert.Equal(t, map[string]string{
			keyRegistration: "set by the environment variable POLYTHOUGHT_REGISTRATION; change it there",
			keyMaxWorkflows: "must be at least 1",
			"no.such":       "no such setting",
		}, params)

		assert.Empty(t, repo.applied)
		assert.Same(t, before, s.Current())
	})

	t.Run("skips changes that change nothing", func(t *testing.T) {
		t.Parallel()

		repo := newRepo(saved(keyMaxWorkflows, `5`))
		s := load(t, repo, nil)

		err := s.Update(context.Background(), root, map[string]json.RawMessage{
			keyMaxWorkflows: json.RawMessage(`5`),
			keyRetention:    json.RawMessage(`null`),
		})
		require.NoError(t, err)
		assert.Empty(t, repo.applied)
	})

	t.Run("seals secrets and keeps them out of the audit", func(t *testing.T) {
		t.Parallel()

		repo := newRepo()
		s := load(t, repo, nil)

		require.NoError(t, s.Update(context.Background(), root, map[string]json.RawMessage{keyToken: json.RawMessage(`" 123:abc "`)}))
		assert.Equal(t, " 123:abc ", s.Current().String(keyToken))

		c := repo.applied[0][0]
		assert.True(t, c.Secret)
		assert.Nil(t, c.Value)
		assert.Nil(t, c.OldValue)
		assert.Nil(t, c.NewValue)
		assert.Equal(t, []byte("setting:telegram.token| 123:abc "), c.Sealed)

		require.NoError(t, s.Update(context.Background(), root, map[string]json.RawMessage{keyToken: json.RawMessage(`""`)}))
		assert.Empty(t, s.Current().String(keyToken), "an empty secret resets it")
		assert.True(t, repo.applied[1][0].Reset)
	})

	t.Run("admins only", func(t *testing.T) {
		t.Parallel()

		repo := newRepo()
		s := load(t, repo, nil)

		err := s.Update(context.Background(), alice, map[string]json.RawMessage{keyMaxWorkflows: json.RawMessage(`5`)})
		require.ErrorIs(t, err, domain.ErrForbidden)
		assert.Empty(t, repo.applied)
	})

	t.Run("a failed save changes nothing", func(t *testing.T) {
		t.Parallel()

		repo := newRepo()
		s := load(t, repo, nil)
		repo.err = errors.New("down")

		err := s.Update(context.Background(), root, map[string]json.RawMessage{keyMaxWorkflows: json.RawMessage(`5`)})
		require.ErrorContains(t, err, "down")
		assert.Equal(t, 100, s.Current().Int(keyMaxWorkflows))

		repo.err = nil
		require.NoError(t, s.Update(context.Background(), root, map[string]json.RawMessage{keyMaxWorkflows: json.RawMessage(`5`)}))
		assert.Equal(t, 5, s.Current().Int(keyMaxWorkflows))
	})
}

func TestList(t *testing.T) {
	t.Parallel()

	repo := newRepo(sealed(keyToken, "123:abc"))
	s := load(t, repo, map[string]string{"POLYTHOUGHT_REGISTRATION": "open"})

	_, err := s.List(context.Background(), alice)
	require.ErrorIs(t, err, domain.ErrForbidden)

	o, err := s.List(context.Background(), root)
	require.NoError(t, err)
	assert.False(t, o.RestartPending)

	keys := make([]string, len(o.Entries))
	for i, e := range o.Entries {
		keys[i] = e.Key
	}

	assert.Equal(t, []string{keyRegistration, keyMaxWorkflows, keyLogFormat, keyToken, keyProxies, keyRetention}, keys)

	assert.Equal(t, "open", o.Entries[0].Value)
	assert.Equal(t, settings.SourceEnvironment, o.Entries[0].Source)
	assert.Nil(t, o.Entries[3].Value, "secrets are never shown")
	assert.True(t, o.Entries[3].IsSet)
	assert.Equal(t, settings.SourceDatabase, o.Entries[3].Source)
	assert.Equal(t, 720*time.Hour, o.Entries[5].Value)
}

func TestRestartPending(t *testing.T) {
	t.Parallel()

	s := load(t, newRepo(), nil)
	ctx := context.Background()

	require.NoError(t, s.Update(ctx, root, map[string]json.RawMessage{keyMaxWorkflows: json.RawMessage(`5`)}))
	assert.False(t, s.RestartPending(), "a live setting applies at once")

	require.NoError(t, s.Update(ctx, root, map[string]json.RawMessage{keyLogFormat: json.RawMessage(`"json"`)}))
	assert.True(t, s.RestartPending())

	o, err := s.List(ctx, root)
	require.NoError(t, err)
	assert.True(t, o.RestartPending)
	assert.True(t, o.Entries[2].Pending)
	assert.Equal(t, "json", s.Current().String(keyLogFormat), "the new value is current, waiting to be used")

	require.NoError(t, s.Update(ctx, root, map[string]json.RawMessage{keyLogFormat: json.RawMessage(`"text"`)}))
	assert.False(t, s.RestartPending(), "back to what the server started with")
}

func TestRestart(t *testing.T) {
	t.Parallel()

	var by []domain.UserID

	s := load(t, newRepo(), nil, settings.WithRestarter(func(u domain.User) { by = append(by, u.ID) }))

	require.ErrorIs(t, s.Restart(context.Background(), alice), domain.ErrForbidden)
	require.NoError(t, s.Restart(context.Background(), root))
	assert.Equal(t, []domain.UserID{"root"}, by)

	require.Error(t, load(t, newRepo(), nil).Restart(context.Background(), root), "no restarter wired")
}

func TestHistory(t *testing.T) {
	t.Parallel()

	s := load(t, newRepo(), nil)

	_, err := s.History(context.Background(), alice, ports.PageFilter{})
	require.ErrorIs(t, err, domain.ErrForbidden)

	page, err := s.History(context.Background(), root, ports.PageFilter{})
	require.NoError(t, err)
	assert.Len(t, page.Items, 1)
}
