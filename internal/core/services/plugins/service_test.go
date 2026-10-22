package plugins_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
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

// repo keeps plugins in memory, as Postgres would, and the bytes their nodes stored.
type repo struct {
	mu      sync.Mutex
	plugins map[string]*domain.InstalledPlugin
	audit   []domain.PluginAudit
	stores  map[string]int64
	keys    map[string]domain.PluginKey
}

func newRepo() *repo {
	return &repo{plugins: map[string]*domain.InstalledPlugin{}, stores: map[string]int64{}, keys: map[string]domain.PluginKey{}}
}

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

// version changes a.Version of a.PluginID.
func (r *repo) version(a domain.PluginAudit, fn func(v *domain.PluginVersion) error) error {
	return r.change(a.PluginID, a, func(p *domain.InstalledPlugin) error {
		for i := range p.Versions {
			if p.Versions[i].Version == a.Version {
				return fn(&p.Versions[i])
			}
		}

		return domain.ErrNotFound
	})
}

func (r *repo) SetDigest(_ context.Context, digest string, a domain.PluginAudit) error {
	return r.version(a, func(v *domain.PluginVersion) error {
		if v.Digest != "" {
			return domain.ErrNotFound
		}

		v.Digest = digest

		return nil
	})
}

func (r *repo) SetSigner(_ context.Context, signer, keyID string, a domain.PluginAudit) error {
	return r.version(a, func(v *domain.PluginVersion) error { v.Signer, v.KeyID = signer, keyID; return nil })
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

func (r *repo) SetCaps(_ context.Context, caps domain.PluginCaps, a domain.PluginAudit) error {
	return r.change(a.PluginID, a, func(p *domain.InstalledPlugin) error { p.Caps = caps; return nil })
}

func (r *repo) Delete(_ context.Context, a domain.PluginAudit, stores bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.plugins[a.PluginID]; !ok {
		return domain.ErrNotFound
	}

	delete(r.plugins, a.PluginID)

	if stores {
		delete(r.stores, a.PluginID)
		delete(r.keys, a.PluginID)
	}

	r.audit = append(r.audit, a)

	return nil
}

func (r *repo) StoreBytes(context.Context) (map[string]int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return maps.Clone(r.stores), nil
}

func (r *repo) ForgetStores(_ context.Context, a domain.PluginAudit) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.plugins[a.PluginID]; !ok {
		delete(r.stores, a.PluginID)
		delete(r.keys, a.PluginID)
	}

	r.audit = append(r.audit, a)

	return nil
}

func (r *repo) Key(_ context.Context, id string) (domain.PluginKey, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	k, ok := r.keys[id]

	return k, ok, nil
}

func (r *repo) Bind(_ context.Context, k domain.PluginKey) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.keys[k.PluginID]; !ok {
		r.keys[k.PluginID] = k
	}

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

// files are versions on disk, and plugins' lasting directories, by the bytes they take:
// an archive is the text "id version perm,perm".
type files struct {
	mu       sync.Mutex
	versions map[ports.PluginFound]ports.PluginFile
	removed  []ports.PluginFound
	tampered map[ports.PluginFound]bool
	data     map[string]int64
}

func newFiles() *files {
	return &files{versions: map[ports.PluginFound]ports.PluginFile{}, tampered: map[ports.PluginFound]bool{}, data: map[string]int64{}}
}

func file(id, version string, perms ...string) ports.PluginFile {
	m, _ := json.Marshal(map[string]any{"id": id, "version": version, "permissions": perms})

	return ports.PluginFile{
		ID: id, Version: version, Name: id, Permissions: perms, Manifest: m, Program: "/plugins/" + id + "/" + version,
		Digest: digestOf(string(m)),
	}
}

// digestOf stands for the digest of a version's checksums.txt.
func digestOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func archive(id, version string, perms ...string) io.Reader {
	return strings.NewReader(id + " " + version + " " + strings.Join(perms, ","))
}

// signedArchive is an archive a trusted key named signer, of ID keyID, signed; with no
// signer, one signed by a key not trusted.
func signedArchive(id, version, signer, keyID string) io.Reader {
	return strings.NewReader(id + " " + version + "  " + signer + ":" + keyID)
}

func (f *files) Install(r io.Reader, _ int64) (ports.PluginFile, error) {
	raw, _ := io.ReadAll(r)

	parts := strings.Split(string(raw), " ")
	if len(parts) != 3 && len(parts) != 4 {
		return ports.PluginFile{}, errors.New("not a plugin archive")
	}

	var perms []string
	if parts[2] != "" {
		perms = strings.Split(parts[2], ",")
	}

	pf := file(parts[0], parts[1], perms...)

	if len(parts) == 4 {
		signer, keyID, _ := strings.Cut(parts[3], ":")
		pf.Signed, pf.Signer, pf.KeyID = true, signer, keyID
	}

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

func (f *files) Data() ([]ports.PluginData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []ports.PluginData
	for _, id := range slices.Sorted(maps.Keys(f.data)) {
		out = append(out, ports.PluginData{ID: id, Bytes: f.data[id]})
	}

	return out, nil
}

func (f *files) RemoveData(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.data, id)

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

func (r *runtime) Sandbox() ports.PluginSandbox {
	return ports.PluginSandbox{Why: "nothing can be sandboxed here: only Linux has the sandbox"}
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
	assert.Equal(t, "found on disk; not signed; digest "+file("copied", "2.0.0").Digest, history[0].Detail)
}

// installed installs and approves a version of a plugin, as an admin.
func installed(t *testing.T, f fixture, id, version string, perms ...string) {
	t.Helper()

	_, err := f.s.Install(context.Background(), admin, archive(id, version, perms...))
	require.NoError(t, err)
	require.NoError(t, f.s.Approve(context.Background(), admin, id, version, perms))
}

// Uninstalled with its data, nothing of a plugin is left; without, it is left behind, for a
// reinstall to find or an admin to remove (D342, D348).
func TestUninstallData(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	for _, id := range []string{"weather", "news"} {
		installed(t, f, id, "1.0.0")
		f.files.data[id] = 1 << 20
		f.repo.stores[id] = 512
	}

	left, err := f.s.LeftBehind(ctx, admin)
	require.NoError(t, err)
	assert.Empty(t, left, "nothing of plugins installed")

	require.NoError(t, f.s.Uninstall(ctx, admin, "weather", true))
	require.NoError(t, f.s.Uninstall(ctx, admin, "news", false))

	left, err = f.s.LeftBehind(ctx, admin)
	require.NoError(t, err)
	assert.Equal(t, []plugins.LeftData{{ID: "news", Files: 1 << 20, Stored: 512}}, left)

	history, _ := f.s.History(ctx, admin, "weather")
	assert.Equal(t, "its data removed", history[0].Detail)

	history, _ = f.s.History(ctx, admin, "news")
	assert.Equal(t, "its data kept", history[0].Detail)

	_, err = f.s.LeftBehind(ctx, alice)
	require.ErrorIs(t, err, domain.ErrForbidden, "admins only")

	t.Run("removed by an admin, never an installed plugin's", func(t *testing.T) {
		installed(t, f, "kept", "1.0.0")
		f.files.data["kept"] = 10

		err := f.s.RemoveLeftBehind(ctx, admin, "kept")
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), "kept is installed: uninstall it, removing its data")
		assert.Contains(t, f.files.data, "kept")

		require.ErrorIs(t, f.s.RemoveLeftBehind(ctx, alice, "news"), domain.ErrForbidden)

		// Where the filesystem ignores case, Kept would be kept's (P27).
		require.ErrorIs(t, f.s.RemoveLeftBehind(ctx, admin, "Kept"), domain.ErrInvalidInput)
		assert.Contains(t, f.files.data, "kept")

		require.NoError(t, f.s.RemoveLeftBehind(ctx, admin, "news"))

		left, _ := f.s.LeftBehind(ctx, admin)
		assert.Empty(t, left)

		history, _ := f.s.History(ctx, admin, "news")
		assert.Equal(t, domain.PluginDataRemoved, history[0].Action)
	})
}

// A plugin's own caps apply at once, checked as the Config page's are (D346).
func TestCaps(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	installed(t, f, "weather", "1.0.0")

	require.ErrorIs(t, f.s.SetCaps(ctx, alice, "weather", domain.PluginCaps{DataMiB: 10}), domain.ErrForbidden)

	err := f.s.SetCaps(ctx, admin, "weather", domain.PluginCaps{DataMiB: -1, MemoryMiB: 8})

	var problems *domain.ValidationError
	require.ErrorAs(t, err, &problems)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
	require.Len(t, problems.Problems, 2)
	assert.Equal(t, "dataCap", problems.Problems[0].Param)
	assert.Equal(t, "A memory cap is 16 to 65536 MiB.", problems.Problems[1].Message)

	require.NoError(t, f.s.SetCaps(ctx, admin, "weather", domain.PluginCaps{DataMiB: 2048}))
	assert.Equal(t, domain.PluginCaps{DataMiB: 2048}, f.s.CapsOf("weather"), "at once")

	history, _ := f.s.History(ctx, admin, "weather")
	assert.Equal(t, "data: 2048 MiB; memory: the Config page's", history[0].Detail)

	require.ErrorIs(t, f.s.SetCaps(ctx, admin, "none", domain.PluginCaps{}), domain.ErrNotFound)
	assert.Zero(t, f.s.CapsOf("none"))

	sb, err := f.s.Sandbox(admin)
	require.NoError(t, err)
	assert.False(t, sb.Available)

	_, err = f.s.Sandbox(alice)
	require.ErrorIs(t, err, domain.ErrForbidden)
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
	require.NoError(t, f.s.Uninstall(ctx, admin, "gone", false))
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

		history, _ := f.s.History(ctx, admin, "broken")
		assert.Equal(t, domain.PluginAudit{PluginID: "broken", Version: "1.0.0", Action: domain.PluginNotLoaded, Detail: "exit status 3", At: at}, history[0],
			"in its history, by the server (P29)")
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

	digest := file("weather", "1.0.0").Digest
	assert.Equal(t, "no permissions; digest "+digest, history[1].Detail, "the bytes approved (P29)")
	assert.Equal(t, "uploaded; not signed; digest "+digest, history[2].Detail, "where from, who signed it, the bytes installed (P29)")
}

// historyOf is a plugin's history, its actions and details.
func historyOf(t *testing.T, f fixture, id string) []string {
	t.Helper()

	history, err := f.s.History(context.Background(), admin, id)
	require.NoError(t, err)

	out := make([]string, len(history))
	for i, a := range history {
		out[i] = a.Action + ": " + a.Detail
	}

	return out
}

// An approval holds to the files installed (P16): a version replaced whole on disk, as a
// new checksums.txt would let it be, is neither approved, loaded nor started again, and its
// history says so (P29).
func TestApprovalHoldsToTheFiles(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	installed(t, f, "weather", "1.0.0")
	_, err := f.s.Install(ctx, admin, archive("weather", "1.1.0"))
	require.NoError(t, err)

	replaced := func(version string) {
		pf := file("weather", version)
		pf.Digest = digestOf("other files")
		f.files.versions[ports.PluginFound{ID: "weather", Version: version}] = pf
	}

	replaced("1.0.0")
	replaced("1.1.0")

	err = f.s.Approve(ctx, admin, "weather", "1.1.0", nil)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
	assert.Contains(t, err.Error(), "its files are not those installed")

	require.NoError(t, f.s.Load(ctx))
	assert.Empty(t, f.runtime.loaded, "not loaded")
	assert.Contains(t, historyOf(t, f, "weather")[0], domain.PluginNotLoaded+": invalid input: weather 1.0.0: its files are not those installed")

	err = f.s.CheckFiles(ctx, "weather", "1.0.0")
	require.ErrorIs(t, err, domain.ErrInvalidInput, "nor started again")
	assert.Contains(t, historyOf(t, f, "weather")[0], domain.PluginNotLoaded+": as it started again: invalid input: weather 1.0.0: its files are not those installed")

	f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.0"}] = file("weather", "1.0.0")
	require.NoError(t, f.s.CheckFiles(ctx, "weather", "1.0.0"), "as installed again")
}

// A version recorded before digests were has its digest recorded as it is first opened,
// held to from then; and its files' manifest must be the one recorded, as JSON reads it
// (P16).
func TestDigestRecordedFirstOpened(t *testing.T) {
	ctx := context.Background()

	recordedBefore := func(t *testing.T, manifest string) fixture {
		t.Helper()

		f := newFixture()
		installed(t, f, "weather", "1.0.0")

		v := &f.repo.plugins["weather"].Versions[0]
		v.Digest, v.Manifest = "", json.RawMessage(manifest)

		return f
	}

	t.Run("recorded, then held to", func(t *testing.T) {
		// As kept before P19: its bytes, empty members and numbers as written.
		f := recordedBefore(t, `{"version": "1.0.0", "id": "weather", "permissions": [], "description": "", "n": 1e2}`)
		pf := file("weather", "1.0.0")
		pf.Manifest = json.RawMessage(`{"id":"weather","version":"1.0.0","n":100}`)
		f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.0"}] = pf

		require.NoError(t, f.s.Load(ctx))
		require.Len(t, f.runtime.loaded, 1)

		ip, _ := f.repo.Get(ctx, "weather")
		assert.Equal(t, pf.Digest, ip.Versions[0].Digest)
		assert.Equal(t, domain.PluginDigest+": "+pf.Digest, historyOf(t, f, "weather")[0])

		pf.Digest = digestOf("other files")
		f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.0"}] = pf
		require.ErrorIs(t, f.s.CheckFiles(ctx, "weather", "1.0.0"), domain.ErrInvalidInput)
	})

	t.Run("refused when its manifest is another", func(t *testing.T) {
		f := recordedBefore(t, `{"id": "weather", "version": "1.0.0"}`)
		f.files.versions[ports.PluginFound{ID: "weather", Version: "1.0.0"}] = file("weather", "1.0.0", "orders")

		require.NoError(t, f.s.Load(ctx))
		assert.Empty(t, f.runtime.loaded)
		assert.Contains(t, historyOf(t, f, "weather")[0], "its files' manifest is not the one installed")

		ip, _ := f.repo.Get(ctx, "weather")
		assert.Empty(t, ip.Versions[0].Digest, "never recorded for files not as recorded")
	})
}

// Who signed a version is as the keys admins trust now say (P21): one whose key is no
// longer trusted is not approved as signed, and shows so; at load, the signer is recorded
// again.
func TestSignerAsTrustedNow(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	k := ports.PluginFound{ID: "weather", Version: "1.0.0"}
	signedBy := func(signer string) {
		pf := file("weather", "1.0.0")
		pf.Signed, pf.Signer, pf.KeyID = true, signer, "E7620F1842B4E81F"
		f.files.versions[k] = pf
	}

	signedBy("Alice")
	_, err := f.s.Adopt(ctx, admin, "weather", "1.0.0")
	require.NoError(t, err)

	signedBy("") // her key removed from those trusted

	err = f.s.Approve(ctx, admin, "weather", "1.0.0", nil)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
	assert.Contains(t, err.Error(), "Alice, which signed it, is no longer a key you trust")

	ip, _ := f.repo.Get(ctx, "weather")
	assert.Empty(t, ip.Versions[0].Signer, "the page warns again")
	assert.Equal(t, "E7620F1842B4E81F", ip.Versions[0].KeyID)
	assert.Equal(t, domain.PluginSigner+": signed by a key not trusted, E7620F1842B4E81F; was signed by Alice, key E7620F1842B4E81F",
		historyOf(t, f, "weather")[0])

	// Its ID is bound to her key (D365): not approved while no trusted key verifies it.
	err = f.s.Approve(ctx, admin, "weather", "1.0.0", nil)
	require.ErrorIs(t, err, domain.ErrInvalidInput)
	assert.Contains(t, err.Error(), "trust that key again if it signed this one")

	signedBy("Alice's company") // trusted again, by another name
	require.NoError(t, f.s.Approve(ctx, admin, "weather", "1.0.0", nil))
	require.NoError(t, f.s.Load(ctx))

	ip, _ = f.repo.Get(ctx, "weather")
	assert.Equal(t, "Alice's company", ip.Versions[0].Signer)
}

// fetcher fetches the same archive from any URL.
type fetcher struct{ archive string }

func (f fetcher) Fetch(context.Context, string, int64) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.archive)), nil
}

// The install's audit says where the archive came from, without what in its URL could be a
// secret (P29).
func TestInstalledFrom(t *testing.T) {
	ctx := context.Background()
	f := newFixture()
	f.s = plugins.New(f.repo, f.files, f.runtime, f.workflows, logging.Noop(),
		plugins.WithClock(func() time.Time { return at }), plugins.WithFetcher(fetcher{archive: "weather 1.0.0 "}))

	_, err := f.s.InstallFrom(ctx, admin, "https://bob:pw@example.com/weather.tgz?token=secret#top")
	require.NoError(t, err)

	assert.Equal(t, domain.PluginInstalled+": fetched from https://example.com/weather.tgz; not signed; digest "+file("weather", "1.0.0").Digest,
		historyOf(t, f, "weather")[0])
}

// A plugin's ID is bound to the key a trusted key verified on its first signed version:
// another key's archive of it, or one not signed, is refused (D365), while its data is left
// behind too (D366).
func TestKeyBinding(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	refused := func(t *testing.T, archive io.Reader, why string) {
		t.Helper()

		_, err := f.s.Install(ctx, admin, archive)
		require.ErrorIs(t, err, domain.ErrInvalidInput)
		assert.Contains(t, err.Error(), why)
	}

	t.Run("bound by its first signed version, then held to its key", func(t *testing.T) {
		_, err := f.s.Install(ctx, admin, signedArchive("weather", "1.0.0", "Alice", "K1"))
		require.NoError(t, err)

		k, ok, _ := f.repo.Key(ctx, "weather")
		require.True(t, ok)
		assert.Equal(t, "K1", k.KeyID)
		assert.Equal(t, "Alice", k.Signer)

		refused(t, signedArchive("weather", "1.1.0", "Mallory", "K2"),
			"plugin weather is bound to the key K1, Alice's, which signed its first signed version, and this one is signed by Mallory, key K2")
		refused(t, archive("weather", "1.1.0"), "and this one is not signed")
		refused(t, signedArchive("weather", "1.1.0", "", "K1"), "and this one is signed by a key you do not trust, K1")
		assert.NotContains(t, f.files.versions, ports.PluginFound{ID: "weather", Version: "1.1.0"}, "its files gone with it")

		_, err = f.s.Install(ctx, admin, signedArchive("weather", "1.1.0", "Alice", "K1"))
		require.NoError(t, err, "her next version")
	})

	t.Run("one first installed unsigned is bound by its first signed version", func(t *testing.T) {
		_, err := f.s.Install(ctx, admin, archive("news", "1.0.0"))
		require.NoError(t, err)

		_, ok, _ := f.repo.Key(ctx, "news")
		assert.False(t, ok, "nothing to bind it to")

		_, err = f.s.Install(ctx, admin, signedArchive("news", "1.1.0", "Bob", "K3"))
		require.NoError(t, err)

		refused(t, archive("news", "1.2.0"), "bound to the key K3")

		err = f.s.Approve(ctx, admin, "news", "1.0.0", nil)
		require.ErrorIs(t, err, domain.ErrInvalidInput, "the unsigned one waiting, not approved now")
		assert.Contains(t, err.Error(), "bound to the key K3")
	})

	t.Run("the binding stays with data left behind, and goes with it", func(t *testing.T) {
		require.NoError(t, f.s.Uninstall(ctx, admin, "weather", false))
		refused(t, signedArchive("weather", "2.0.0", "Mallory", "K2"), "bound to the key K1")

		require.NoError(t, f.s.RemoveLeftBehind(ctx, admin, "weather"))
		_, err := f.s.Install(ctx, admin, signedArchive("weather", "2.0.0", "Mallory", "K2"))
		require.NoError(t, err, "another author's, once its data is gone")

		require.NoError(t, f.s.Uninstall(ctx, admin, "weather", true))
		_, ok, _ := f.repo.Key(ctx, "weather")
		assert.False(t, ok, "uninstalled with its data")
	})

	t.Run("one approved before IDs were bound is bound as it loads", func(t *testing.T) {
		_, err := f.s.Install(ctx, admin, signedArchive("rain", "1.0.0", "Carol", "K4"))
		require.NoError(t, err)
		require.NoError(t, f.s.Approve(ctx, admin, "rain", "1.0.0", nil))

		delete(f.repo.keys, "rain")
		require.NoError(t, f.s.Load(ctx))

		k, ok, _ := f.repo.Key(ctx, "rain")
		require.True(t, ok)
		assert.Equal(t, "K4", k.KeyID)
	})
}

// The list says what each plugin's data takes, its directory and what its nodes stored: one
// not approved yet has what its ID left behind (D366).
func TestListData(t *testing.T) {
	ctx := context.Background()
	f := newFixture()

	_, err := f.s.Install(ctx, admin, archive("weather", "1.0.0"))
	require.NoError(t, err)

	f.files.data["weather"] = 3 << 20
	f.repo.stores["weather"] = 512

	list, _, err := f.s.List(ctx, admin)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, plugins.DataTaken{Files: 3 << 20, Stored: 512}, list[0].Data)
}
