package plugins_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
	"github.com/lein3000zzz/polythought-go/internal/core/services/plugins"
)

var (
	admin = auth.Principal{User: domain.User{ID: "root", Role: domain.RoleAdmin}}
	alice = auth.Principal{User: domain.User{ID: "alice"}}
	at    = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
)

// repo keeps plugins in memory, as Postgres would.
type repo struct {
	mu      sync.Mutex
	plugins map[string]*domain.InstalledPlugin
	audit   []domain.PluginAudit
}

func newRepo() *repo { return &repo{plugins: map[string]*domain.InstalledPlugin{}} }

func (r *repo) List(context.Context) ([]domain.InstalledPlugin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []domain.InstalledPlugin
	for _, id := range slices.Sorted(func(yield func(string) bool) {
		for k := range r.plugins {
			if !yield(k) {
				return
			}
		}
	}) {
		out = append(out, *r.plugins[id])
	}

	return out, nil
}

func (r *repo) Get(_ context.Context, id string) (domain.InstalledPlugin, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[id]
	if !ok {
		return domain.InstalledPlugin{}, domain.ErrNotFound
	}

	return *p, nil
}

func (r *repo) AddVersion(_ context.Context, v domain.PluginVersion, a domain.PluginAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[v.PluginID]
	if !ok {
		p = &domain.InstalledPlugin{ID: v.PluginID, Enabled: true, Everyone: true}
		r.plugins[v.PluginID] = p
	}

	if _, dup := p.Find(v.Version); dup {
		return domain.ErrPluginInstalled
	}

	p.Versions = append([]domain.PluginVersion{v}, p.Versions...)
	r.audit = append(r.audit, a)

	return nil
}

func (r *repo) change(id string, a domain.PluginAudit, fn func(p *domain.InstalledPlugin) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	p, ok := r.plugins[id]
	if !ok {
		return domain.ErrNotFound
	}

	if err := fn(p); err != nil {
		return err
	}

	r.audit = append(r.audit, a)

	return nil
}

func (r *repo) Approve(_ context.Context, a domain.PluginAudit) error {
	return r.change(a.PluginID, a, func(p *domain.InstalledPlugin) error {
		for i := range p.Versions {
			if p.Versions[i].Version == a.Version {
				p.Versions[i].ApprovedAt, p.Versions[i].ApprovedBy = a.At, a.By
				p.Version = a.Version

				return nil
			}
		}

		return domain.ErrNotFound
	})
}

func (r *repo) RemoveVersion(_ context.Context, a domain.PluginAudit) error {
	return r.change(a.PluginID, a, func(p *domain.InstalledPlugin) error {
		p.Versions = slices.DeleteFunc(p.Versions, func(v domain.PluginVersion) bool { return v.Version == a.Version })
		return nil
	})
}

func (r *repo) SetEnabled(_ context.Context, enabled bool, a domain.PluginAudit) error {
	return r.change(a.PluginID, a, func(p *domain.InstalledPlugin) error { p.Enabled = enabled; return nil })
}

func (r *repo) SetUsers(_ context.Context, everyone bool, users []domain.UserID, a domain.PluginAudit) error {
	return r.change(a.PluginID, a, func(p *domain.InstalledPlugin) error { p.Everyone, p.Users = everyone, users; return nil })
}

func (r *repo) Delete(_ context.Context, a domain.PluginAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.plugins[a.PluginID]; !ok {
		return domain.ErrNotFound
	}

	delete(r.plugins, a.PluginID)
	r.audit = append(r.audit, a)

	return nil
}

func (r *repo) Record(_ context.Context, a domain.PluginAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.audit = append(r.audit, a)

	return nil
}

func (r *repo) History(_ context.Context, id string, _ int) ([]domain.PluginAudit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []domain.PluginAudit
	for _, a := range slices.Backward(r.audit) {
		if a.PluginID == id {
			out = append(out, a)
		}
	}

	return out, nil
}

// files are versions on disk: an archive is the text "id version perm,perm".
type files struct {
	mu       sync.Mutex
	versions map[ports.PluginFound]ports.PluginFile
	removed  []ports.PluginFound
	tampered map[ports.PluginFound]bool
}

func newFiles() *files {
	return &files{versions: map[ports.PluginFound]ports.PluginFile{}, tampered: map[ports.PluginFound]bool{}}
}

func file(id, version string, perms ...string) ports.PluginFile {
	m, _ := json.Marshal(map[string]any{"id": id, "version": version, "permissions": perms})

	return ports.PluginFile{ID: id, Version: version, Name: id, Permissions: perms, Manifest: m, Program: "/plugins/" + id + "/" + version}
}

func archive(id, version string, perms ...string) io.Reader {
	return strings.NewReader(id + " " + version + " " + strings.Join(perms, ","))
}

func (f *files) Install(r io.Reader, _ int64) (ports.PluginFile, error) {
	raw, _ := io.ReadAll(r)

	parts := strings.Split(string(raw), " ")
	if len(parts) != 3 {
		return ports.PluginFile{}, errors.New("not a plugin archive")
	}

	var perms []string
	if parts[2] != "" {
		perms = strings.Split(parts[2], ",")
	}

	pf := file(parts[0], parts[1], perms...)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.versions[ports.PluginFound{ID: pf.ID, Version: pf.Version}] = pf

	return pf, nil
}

func (f *files) Open(id, version string) (ports.PluginFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	k := ports.PluginFound{ID: id, Version: version}
	if f.tampered[k] {
		return ports.PluginFile{}, errors.New("its files do not match what was installed")
	}

	pf, ok := f.versions[k]
	if !ok {
		return ports.PluginFile{}, domain.ErrNotFound
	}

	return pf, nil
}

func (f *files) Remove(id, version string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	k := ports.PluginFound{ID: id, Version: version}
	delete(f.versions, k)
	f.removed = append(f.removed, k)

	return nil
}

func (f *files) List() ([]ports.PluginFound, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []ports.PluginFound
	for k := range f.versions {
		out = append(out, k)
	}

	slices.SortFunc(out, func(a, b ports.PluginFound) int { return strings.Compare(a.ID+a.Version, b.ID+b.Version) })

	return out, nil
}

// runtime starts what it is told, failing those it is told to.
type runtime struct {
	mu     sync.Mutex
	loaded []ports.PluginFile
	fail   map[string]error
}

func (r *runtime) Load(_ context.Context, f ports.PluginFile) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.fail[f.ID]; err != nil {
		return err
	}

	r.loaded = append(r.loaded, f)

	return nil
}

func (r *runtime) Status(id string) (ports.PluginStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, f := range r.loaded {
		if f.ID == id {
			return ports.PluginStatus{Version: f.Version, State: "running"}, true
		}
	}

	return ports.PluginStatus{}, false
}

func (r *runtime) Start(id string) error {
	if _, ok := r.Status(id); !ok {
		return errors.New("not loaded")
	}

	return nil
}

// workflows are the active ones, paused as they are told.
type workflows struct {
	active []domain.Workflow
	paused map[domain.WorkflowID]string
}

func (w *workflows) Pause(_ context.Context, why func(domain.Workflow) string) (int, error) {
	n := 0

	for _, wf := range w.active {
		if reason := why(wf); reason != "" {
			w.paused[wf.ID] = reason
			n++
		}
	}

	w.active = slices.DeleteFunc(w.active, func(wf domain.Workflow) bool { _, ok := w.paused[wf.ID]; return ok })

	return n, nil
}

// notices are what admins were told.
type notices struct {
	crashed []string
}

func (n *notices) WorkflowPaused(domain.UserID, domain.Workflow, string) {}
func (n *notices) PluginCrashed(plugin, why string)                      { n.crashed = append(n.crashed, plugin+": "+why) }

type fixture struct {
	repo      *repo
	files     *files
	runtime   *runtime
	workflows *workflows
	notices   *notices
	s         *plugins.Service
}

func newFixture() fixture {
	f := fixture{
		repo: newRepo(), files: newFiles(), runtime: &runtime{fail: map[string]error{}},
		workflows: &workflows{paused: map[domain.WorkflowID]string{}}, notices: &notices{},
	}

	f.s = plugins.New(f.repo, f.files, f.runtime, f.workflows, logging.Noop(),
		plugins.WithClock(func() time.Time { return at }), plugins.WithNotifier(f.notices))

	return f
}

func using(id domain.WorkflowID, owner domain.UserID, types ...string) domain.Workflow {
	w := domain.Workflow{ID: id, OwnerID: owner}
	for i, typ := range types {
		w.Nodes = append(w.Nodes, domain.NodeSpec{ID: domain.NodeID(rune('a' + i)), Type: typ})
	}

	return w
}

func TestInstallAndApprove(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	_, err := f.s.Install(ctx, alice, archive("weather", "1.0.0"))
	require.ErrorIs(t, err, domain.ErrForbidden, "admins only")

	in, err := f.s.Install(ctx, admin, archive("weather", "1.0.0", "fetch:api.weather.example"))
	require.NoError(t, err)
	assert.Equal(t, []string{"fetch:api.weather.example"}, in.NewPermissions, "all of a new plugin's")
	assert.False(t, in.Upgrade)

	list, found, err := f.s.List(ctx, admin)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, found)
	assert.False(t, list[0].Restart, "nothing to run before an approval")
	assert.Nil(t, list[0].Status)

	t.Run("approved for exactly what it asks", func(t *testing.T) {
		err := f.s.Approve(ctx, admin, "weather", "1.0.0", []string{"fetch:*"})
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "it asks for fetch:api.weather.example: approve those, and only those")

		require.NoError(t, f.s.Approve(ctx, admin, "weather", "1.0.0", []string{"fetch:api.weather.example"}))
		require.ErrorIs(t, f.s.Approve(ctx, admin, "weather", "1.0.0", []string{"fetch:api.weather.example"}), domain.ErrInvalidInput, "once")

		list, _, _ := f.s.List(ctx, admin)
		assert.True(t, list[0].Restart, "a restart runs it (D317)")
	})

	t.Run("not when its files changed since", func(t *testing.T) {
		_, err := f.s.Install(ctx, admin, archive("news", "1.0.0"))
		require.NoError(t, err)

		f.files.tampered[ports.PluginFound{ID: "news", Version: "1.0.0"}] = true
		require.Error(t, f.s.Approve(ctx, admin, "news", "1.0.0", nil))
	})

	t.Run("an upgrade says what it asks for that is new (D318)", func(t *testing.T) {
		in, err := f.s.Install(ctx, admin, archive("weather", "1.1.0", "fetch:api.weather.example", "telegram"))
		require.NoError(t, err)
		assert.True(t, in.Upgrade)
		assert.Equal(t, []string{"telegram"}, in.NewPermissions)

		ip, _ := f.repo.Get(ctx, "weather")
		assert.Equal(t, "1.0.0", ip.Version, "the approved one runs meanwhile")
	})

	t.Run("a version twice: its files not kept", func(t *testing.T) {
		_, err := f.s.Install(ctx, admin, archive("weather", "1.1.0"))
		require.ErrorIs(t, err, domain.ErrPluginInstalled)
		assert.Contains(t, f.files.removed, ports.PluginFound{ID: "weather", Version: "1.1.0"})
	})

	t.Run("the version that runs is uninstalled, not removed", func(t *testing.T) {
		require.ErrorIs(t, f.s.RemoveVersion(ctx, admin, "weather", "1.0.0"), domain.ErrInvalidInput)
	})
}

func TestAdopt(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	f.files.versions[ports.PluginFound{ID: "copied", Version: "2.0.0"}] = file("copied", "2.0.0")

	_, found, err := f.s.List(ctx, admin)
	require.NoError(t, err)
	assert.Equal(t, []ports.PluginFound{{ID: "copied", Version: "2.0.0"}}, found, "on disk, no admin installed it (D321)")

	_, err = f.s.Adopt(ctx, admin, "copied", "2.0.0")
	require.NoError(t, err)

	_, found, _ = f.s.List(ctx, admin)
	assert.Empty(t, found)

	history, err := f.s.History(ctx, admin, "copied")
	require.NoError(t, err)
	assert.Equal(t, "found on disk", history[0].Detail)
}

// installed installs and approves a version of a plugin, as an admin.
func installed(t *testing.T, f fixture, id, version string, perms ...string) {
	t.Helper()

	_, err := f.s.Install(context.Background(), admin, archive(id, version, perms...))
	require.NoError(t, err)
	require.NoError(t, f.s.Approve(context.Background(), admin, id, version, perms))
}

func TestLoad(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	installed(t, f, "weather", "1.0.0")
	installed(t, f, "weather", "1.1.0") // an upgrade, approved: 1.0.0 goes once 1.1.0 runs
	installed(t, f, "news", "1.0.0")
	installed(t, f, "broken", "1.0.0")
	installed(t, f, "gone", "1.0.0")
	installed(t, f, "private", "1.0.0")

	_, err := f.s.Install(ctx, admin, archive("pending", "1.0.0")) // never approved
	require.NoError(t, err)

	require.NoError(t, f.s.SetEnabled(ctx, admin, "news", false))
	require.NoError(t, f.s.Uninstall(ctx, admin, "gone"))
	require.NoError(t, f.s.SetUsers(ctx, admin, "private", false, []domain.UserID{"bob"}))
	f.runtime.fail["broken"] = errors.New("exit status 3")

	f.workflows.active = []domain.Workflow{
		using("w-weather", "alice", "trigger.manual", "plugin.weather.forecast"),
		using("w-news", "alice", "plugin.news.headlines"),
		using("w-gone", "alice", "plugin.gone.x"),
		using("w-private", "alice", "plugin.private.x"),
		using("w-bob", "bob", "plugin.private.x"),
		using("w-broken", "alice", "plugin.broken.x"),
	}

	require.NoError(t, f.s.Load(ctx))
	require.NoError(t, f.s.PauseGone(ctx))

	t.Run("each enabled plugin's approved version, its files checked again", func(t *testing.T) {
		var loaded []string
		for _, pf := range f.runtime.loaded {
			loaded = append(loaded, pf.ID+" "+pf.Version)
		}

		assert.ElementsMatch(t, []string{"weather 1.1.0", "private 1.0.0"}, loaded)
	})

	t.Run("the version an upgrade replaced removed, the one waiting kept", func(t *testing.T) {
		ip, _ := f.repo.Get(ctx, "weather")
		require.Len(t, ip.Versions, 1)
		assert.Equal(t, "1.1.0", ip.Versions[0].Version)
		assert.Contains(t, f.files.removed, ports.PluginFound{ID: "weather", Version: "1.0.0"})

		pending, _ := f.repo.Get(ctx, "pending")
		assert.Len(t, pending.Versions, 1)
	})

	t.Run("one that will not start left out, the admins told", func(t *testing.T) {
		list, _, _ := f.s.List(ctx, admin)

		var broken plugins.Info
		for _, i := range list {
			if i.ID == "broken" {
				broken = i
			}
		}

		require.NotNil(t, broken.Status)
		assert.Equal(t, "failed", broken.Status.State)
		assert.Equal(t, "exit status 3", broken.Status.LastError)
		assert.Equal(t, []string{"broken: it would not start: exit status 3"}, f.notices.crashed)
	})

	t.Run("workflows of plugins disabled, uninstalled or forbidden paused (D319)", func(t *testing.T) {
		assert.Equal(t, map[domain.WorkflowID]string{
			"w-news":    "the plugin news is disabled",
			"w-gone":    "the plugin gone is uninstalled",
			"w-private": "you may no longer use the plugin private",
		}, f.workflows.paused)
		// w-private was paused already, as its users were set; w-broken waits for its plugin to be fixed.
	})

	t.Run("the gate answers from what admins decided", func(t *testing.T) {
		for plugin, want := range map[string]bool{"weather": true, "private": false, "gone": false, "none": false} {
			ok, err := f.s.PluginAllowed(ctx, "alice", plugin)
			require.NoError(t, err)
			assert.Equal(t, want, ok, plugin)
		}

		ok, _ := f.s.PluginAllowed(ctx, "bob", "private")
		assert.True(t, ok, "bob was chosen")
	})
}

func TestSetUsersPausesAtOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	installed(t, f, "private", "1.0.0")
	installed(t, f, "news", "1.0.0")
	require.NoError(t, f.s.SetEnabled(ctx, admin, "news", false))

	f.workflows.active = []domain.Workflow{
		using("w-alice", "alice", "plugin.private.x"),
		using("w-bob", "bob", "plugin.private.x"),
		using("w-news", "alice", "plugin.news.x"),
	}

	require.NoError(t, f.s.SetUsers(ctx, admin, "private", false, []domain.UserID{"bob"}))

	assert.Equal(t, map[domain.WorkflowID]string{"w-alice": "you may no longer use the plugin private"}, f.workflows.paused,
		"those who may no longer use it, at once; a plugin disabled waits for the restart")
}

func TestStartAndHistory(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	installed(t, f, "weather", "1.0.0")
	require.NoError(t, f.s.Load(ctx))
	require.NoError(t, f.s.Start(ctx, admin, "weather"))
	require.ErrorIs(t, f.s.Start(ctx, admin, "none"), domain.ErrInvalidInput)

	history, err := f.s.History(ctx, admin, "weather")
	require.NoError(t, err)

	var actions []string
	for _, a := range history {
		actions = append(actions, a.Action)
	}

	assert.Equal(t, []string{domain.PluginStarted, domain.PluginApproved, domain.PluginInstalled}, actions)
	assert.Equal(t, "not signed", history[2].Detail)
}
