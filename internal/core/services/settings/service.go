package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/v2"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// Service loads the settings, holds the current ones and lets admins change them.
type Service struct {
	registry *Registry
	repo     ports.SettingsRepository
	box      ports.SecretBox
	now      func() time.Time
	restart  func(by domain.User)

	defaults map[string]any
	file     map[string]any // the config file's, read once like the environment
	env      map[string]any // read once: the environment cannot change while the server runs

	mu    sync.Mutex // serialises changes and guards saved and subs
	saved map[string]any
	subs  []func(*Snapshot)

	boot    *Snapshot // what the server started with, to tell which changes wait for a restart
	current atomic.Pointer[Snapshot]
}

type Option func(*Service)

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// WithRestarter lets admins restart the server. restart should return at once and
// restart shortly after, so the request that asked for it can still be answered.
func WithRestarter(restart func(by domain.User)) Option {
	return func(s *Service) { s.restart = restart }
}

func New(registry *Registry, repo ports.SettingsRepository, box ports.SecretBox, opts ...Option) *Service {
	s := &Service{
		registry: registry,
		repo:     repo,
		box:      box,
		now:      func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
		defaults: make(map[string]any),
		file:     make(map[string]any),
		env:      make(map[string]any),
		saved:    make(map[string]any),
	}

	for _, opt := range opts {
		opt(s)
	}

	for _, spec := range registry.Specs() {
		s.defaults[spec.Key], _ = spec.Parse(spec.Default) // Register checked it parses
	}

	return s
}

// Load reads the environment through getenv, normally os.Getenv, file, the config file's
// values by key (D69), and the saved values. Call it once, before anything reads the
// settings. The environment wins, then the file, then what is saved, then the default.
//
// A bad environment variable or file value is an error, so the server does not start
// with a typo. A
// saved value that no longer passes validation, say after an upgrade made a rule
// stricter, is ignored and reported in warnings: it must not keep the server from
// starting, and an admin can fix it on the Config page.
func (s *Service) Load(ctx context.Context, getenv func(string) string, file map[string]any) (warnings []error, err error) {
	var errs []error

	for _, key := range slices.Sorted(maps.Keys(file)) {
		spec, ok := s.registry.Spec(key)
		if !ok {
			errs = append(errs, fmt.Errorf("config file: no setting %s", key))
			continue
		}

		v, err := spec.Parse(file[key])
		if err != nil {
			errs = append(errs, fmt.Errorf("config file: %s: %w", key, err))
			continue
		}

		s.file[key] = v
	}

	for _, spec := range s.registry.Specs() {
		raw := getenv(spec.EnvName())
		if strings.TrimSpace(raw) == "" {
			continue // unset or empty: compose files pass optional variables through empty
		}

		v, err := spec.Parse(raw)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", spec.EnvName(), err))
			continue
		}

		s.env[spec.Key] = v
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	rows, err := s.repo.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}

	for _, row := range rows {
		spec, ok := s.registry.Spec(row.Key)
		if !ok {
			warnings = append(warnings, fmt.Errorf("saved setting %q is unknown and was ignored", row.Key))
			continue
		}

		v, err := s.decode(ctx, spec, row)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("saved setting %q was ignored: %w", row.Key, err))
			continue
		}

		s.saved[row.Key] = v
	}

	s.boot = s.build()
	s.current.Store(s.boot)

	return warnings, nil
}

func (s *Service) decode(ctx context.Context, spec Spec, row ports.StoredSetting) (any, error) {
	if spec.Secret {
		if row.Sealed == nil {
			return nil, errors.New("a secret must be stored encrypted")
		}

		plain, err := s.box.Open(ctx, row.Sealed, aad(spec.Key))
		if err != nil {
			return nil, err
		}

		return spec.Parse(string(plain))
	}

	if row.Value == nil {
		return nil, errors.New("no value")
	}

	return decodeJSON(spec, row.Value)
}

func decodeJSON(spec Spec, value json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(value))
	dec.UseNumber()

	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, errors.New("not valid JSON")
	}

	return spec.Parse(raw)
}

// aad binds a sealed setting to its key.
func aad(key string) []byte {
	return []byte("setting:" + key)
}

// build layers the defaults, the saved values and the environment, later layers winning.
// The caller holds mu or is Load.
func (s *Service) build() *Snapshot {
	return s.buildFrom(s.saved)
}

// buildFrom is the settings as they would be with saved.
func (s *Service) buildFrom(saved map[string]any) *Snapshot {
	k := koanf.New(".")

	for _, layer := range []map[string]any{s.defaults, saved, s.file, s.env} {
		_ = k.Load(confmap.Provider(layer, "."), nil) // a map provider cannot fail
	}

	snap := &Snapshot{values: make(map[string]any), sources: make(map[string]Source)}

	for _, spec := range s.registry.Specs() {
		snap.values[spec.Key] = k.Get(spec.Key)

		switch {
		case has(s.env, spec.Key):
			snap.sources[spec.Key] = SourceEnvironment
		case has(s.file, spec.Key):
			snap.sources[spec.Key] = SourceFile
		case has(saved, spec.Key):
			snap.sources[spec.Key] = SourceDatabase
		default:
			snap.sources[spec.Key] = SourceDefault
		}
	}

	return snap
}

func has(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// Current returns the settings now. Read them where they are used rather than keeping
// a copy, so a change applies at once.
func (s *Service) Current() *Snapshot {
	return s.current.Load()
}

// Subscribe calls fn with the new settings after every change, in order. fn should be
// quick and must not change settings itself.
func (s *Service) Subscribe(fn func(*Snapshot)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.subs = append(s.subs, fn)
}

// Entry is one setting as an admin sees it.
type Entry struct {
	Spec

	Value  any  // nil for a secret
	IsSet  bool // whether a secret has a value
	Source Source

	// Pending is true when the value changed since the server started and the change
	// takes effect only after a restart.
	Pending bool
}

// Overview is every setting, in the order they were registered.
type Overview struct {
	Entries        []Entry
	RestartPending bool
}

func (s *Service) List(_ context.Context, p auth.Principal) (Overview, error) {
	if !p.User.IsAdmin() {
		return Overview{}, domain.ErrForbidden
	}

	cur := s.Current()

	var out Overview

	for _, spec := range s.registry.Specs() {
		e := Entry{Spec: spec, Source: cur.sources[spec.Key], Pending: s.pending(spec, cur)}

		if spec.Secret {
			e.IsSet = cur.String(spec.Key) != ""
		} else {
			e.Value = cur.values[spec.Key]
		}

		out.Entries = append(out.Entries, e)
		out.RestartPending = out.RestartPending || e.Pending
	}

	return out, nil
}

func (s *Service) pending(spec Spec, cur *Snapshot) bool {
	if spec.Apply != ApplyRestart {
		return false
	}

	return !bytes.Equal(spec.Encode(cur.values[spec.Key]), spec.Encode(s.boot.values[spec.Key]))
}

// RestartPending reports whether a saved change waits for a restart.
func (s *Service) RestartPending() bool {
	cur := s.Current()

	return slices.ContainsFunc(s.registry.Specs(), func(spec Spec) bool { return s.pending(spec, cur) })
}

// Update saves changes, keyed by setting. A JSON null resets a setting to its default; so
// does an empty string for a secret. Every problem is reported at once, as a
// domain.ValidationError whose problems name the setting in Param, and nothing is saved
// unless every change is valid.
func (s *Service) Update(ctx context.Context, p auth.Principal, changes map[string]json.RawMessage) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.Current()
	saved := maps.Clone(s.saved)
	problems := domain.ValidationError{Base: domain.ErrInvalidInput}

	var batch []ports.SettingChange

	for _, key := range slices.Sorted(maps.Keys(changes)) {
		change, err := s.prepare(ctx, key, changes[key], cur, saved)
		if err != nil {
			problems.Add(domain.Problem{Param: key, Message: err.Error()})
			continue
		}

		if change != nil {
			batch = append(batch, *change)
		}
	}

	if err := problems.Err(); err != nil {
		return err
	}

	if len(batch) == 0 {
		return nil
	}

	// Checks across settings see them as they would be.
	next := s.buildFrom(saved)
	for _, rule := range s.registry.Rules() {
		if err := rule.Check(next); err != nil {
			problems.Add(domain.Problem{Param: rule.Key, Message: err.Error()})
		}
	}

	if err := problems.Err(); err != nil {
		return err
	}

	if err := s.repo.Apply(ctx, batch, p.User.ID, s.now()); err != nil {
		return err
	}

	s.saved = saved
	s.current.Store(next)

	for _, fn := range s.subs {
		fn(next)
	}

	return nil
}

// prepare checks one change and applies it to saved. It returns nil when the change
// would not change what is saved.
func (s *Service) prepare(ctx context.Context, key string, value json.RawMessage, cur *Snapshot, saved map[string]any) (*ports.SettingChange, error) {
	spec, ok := s.registry.Spec(key)
	if !ok {
		return nil, errors.New("no such setting")
	}

	switch cur.sources[key] {
	case SourceEnvironment:
		return nil, fmt.Errorf("set by the environment variable %s; change it there", spec.EnvName())
	case SourceFile:
		return nil, fmt.Errorf("set in the config file as %s; change it there", spec.Key)
	}

	v, reset, err := parseChange(spec, value)
	if err != nil {
		return nil, err
	}

	old, wasSaved := saved[key]

	switch {
	case reset && !wasSaved:
		return nil, nil //nolint:nilnil // nothing to change
	case reset:
		delete(saved, key)
		v = s.defaults[key]
	case wasSaved && bytes.Equal(spec.Encode(old), spec.Encode(v)):
		return nil, nil //nolint:nilnil // nothing to change
	default:
		saved[key] = v
	}

	change := ports.SettingChange{Key: key, Reset: reset, Secret: spec.Secret}

	if spec.Secret {
		if !reset {
			if change.Sealed, err = s.box.Seal(ctx, []byte(v.(string)), aad(key)); err != nil {
				return nil, err
			}
		}

		return &change, nil
	}

	if !reset {
		change.Value = spec.Encode(v)
	}

	change.OldValue = spec.Encode(cur.values[key])
	change.NewValue = spec.Encode(v)

	return &change, nil
}

// parseChange reads a changed value. reset is true for a JSON null or an empty secret.
func parseChange(spec Spec, value json.RawMessage) (v any, reset bool, err error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, true, nil
	}

	v, err = decodeJSON(spec, trimmed)
	if err != nil {
		return nil, false, err
	}

	if spec.Secret && v.(string) == "" {
		return nil, true, nil
	}

	return v, false, nil
}

// History lists setting changes, newest first.
func (s *Service) History(ctx context.Context, p auth.Principal, f ports.PageFilter) (ports.Page[ports.SettingAudit], error) {
	if !p.User.IsAdmin() {
		return ports.Page[ports.SettingAudit]{}, domain.ErrForbidden
	}

	return s.repo.History(ctx, f)
}

// Restart asks the server to restart, which applies the changes waiting for one.
func (s *Service) Restart(_ context.Context, p auth.Principal) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	if s.restart == nil {
		return errors.New("settings: this server cannot restart itself")
	}

	s.restart(p.User)

	return nil
}
