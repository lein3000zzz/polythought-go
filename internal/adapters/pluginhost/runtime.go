package pluginhost

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

var _ ports.PluginRuntime = (*Runtime)(nil)

// Runtime runs the plugins the server loads as it starts (D317): each started from its
// version's program, held to its manifest, registered, and supervised (D320).
type Runtime struct {
	ctx      context.Context // the server's: supervision ends with it
	deps     Deps
	registry *engine.Registry
	restarts Restarts

	// check checks a version's files again before each restart; crashed tells the admins
	// of one stopped as crashed.
	check   func(id, version string) error
	crashed func(id, why string)

	mu        sync.Mutex
	plugins   map[string]*loaded
	scenarios []domain.Template
}

type loaded struct {
	p       *Plugin
	version string
}

// RuntimeOption sets up a Runtime.
type RuntimeOption func(*Runtime)

// WithRestarts replaces DefaultRestarts, for tests.
func WithRestarts(r Restarts) RuntimeOption {
	return func(rt *Runtime) { rt.restarts = r }
}

// WithChecks checks a version's files before each restart, and tells of a plugin stopped
// as crashed.
func WithChecks(check func(id, version string) error, crashed func(id, why string)) RuntimeOption {
	return func(rt *Runtime) { rt.check, rt.crashed = check, crashed }
}

// NewRuntime runs plugins until ctx is done, serving them the host API from deps and
// registering them in registry.
func NewRuntime(ctx context.Context, deps Deps, registry *engine.Registry, opts ...RuntimeOption) *Runtime {
	r := &Runtime{ctx: ctx, deps: deps, registry: registry, restarts: DefaultRestarts, plugins: map[string]*loaded{}}

	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Load starts a version, its program describing itself as its manifest.json says, and
// registers its credential types, nodes and triggers, and its scenarios for Scenarios.
func (r *Runtime) Load(ctx context.Context, f ports.PluginFile) error {
	want, err := decodeManifest(f.Manifest)
	if err != nil {
		return fmt.Errorf("its manifest.json: %w", err)
	}

	r.mu.Lock()
	_, there := r.plugins[f.ID]
	r.mu.Unlock()

	if there {
		return fmt.Errorf("plugin %s is loaded already", f.ID)
	}

	p, err := Start(ctx, Program{ID: f.ID, Path: f.Program, Sum: f.ProgramSum, Dir: f.Dir, Data: f.Data}, r.deps)
	if err != nil {
		return err
	}

	fail := func(err error) error {
		p.Close()
		return err
	}

	if !Same(p.Manifest(), want) {
		return fail(errors.New("its program describes itself otherwise than its manifest.json"))
	}

	scenarios, err := p.Scenarios()
	if err != nil {
		return fail(err)
	}

	if err := p.Register(r.registry); err != nil {
		return fail(err)
	}

	p.Supervise(r.ctx, r.restarts, Watch{
		Check: func() error {
			if r.check == nil {
				return nil
			}

			return r.check(f.ID, f.Version)
		},
		Crashed: func(err error) {
			if r.crashed == nil {
				return
			}

			why := fmt.Sprintf("it crashed %d times in %s: %v", r.restarts.Crashes, r.restarts.Within, err)
			if errors.As(err, new(stopped)) {
				why = "it was stopped: " + err.Error()
			}

			r.crashed(f.ID, why)
		},
	})

	r.mu.Lock()
	defer r.mu.Unlock()

	r.plugins[f.ID] = &loaded{p: p, version: f.Version}
	r.scenarios = append(r.scenarios, scenarios...)

	return nil
}

// Scenarios are the scenarios of the plugins loaded, for the library.
func (r *Runtime) Scenarios() []domain.Template {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]domain.Template(nil), r.scenarios...)
}

func (r *Runtime) Status(id string) (ports.PluginStatus, bool) {
	r.mu.Lock()
	l, ok := r.plugins[id]
	r.mu.Unlock()

	if !ok {
		return ports.PluginStatus{}, false
	}

	st := l.p.Status()

	return ports.PluginStatus{
		Version: l.version, State: string(st.State), Since: st.Since, Crashes: st.Crashes, LastError: st.LastError, Log: st.Log,
		Sandboxed: st.Sandboxed, Memory: st.Memory, MemoryCap: st.Caps.Memory, Data: st.Data, DataCap: st.Caps.Data,
	}, true
}

func (r *Runtime) Sandbox() ports.PluginSandbox {
	sb := ports.PluginSandbox{Available: r.deps.Sandbox != nil, Unsandboxed: r.deps.Unsandboxed != nil && r.deps.Unsandboxed()}
	if !sb.Available {
		sb.Why = cmp.Or(r.deps.NoSandbox, sandbox.ErrUnavailable).Error()
	}

	return sb
}

func (r *Runtime) Start(id string) error {
	r.mu.Lock()
	l, ok := r.plugins[id]
	r.mu.Unlock()

	if !ok {
		return fmt.Errorf("plugin %s was not loaded as the server started", id)
	}

	return l.p.Start()
}

// Close stops every plugin, as the server stops.
func (r *Runtime) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, l := range r.plugins {
		l.p.Close()
	}
}

// decodeManifest reads a manifest.json as Describe's is read.
func decodeManifest(raw []byte) (plugin.Manifest, error) {
	var m plugin.Manifest

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(&m); err != nil {
		return m, err
	}

	return m, nil
}
