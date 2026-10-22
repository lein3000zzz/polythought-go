// Package plugins is what admins do with plugins (5c): install them from an archive,
// uploaded or fetched, approve what they ask for, enable, disable and uninstall them, and
// say who may use them (D282); and, as the server starts, load those that run (D317).
//
// What admins decide is kept in the database (D321), the versions' files on disk (D315).
// A change applies on the next start, but for who may use a plugin, which applies at once.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// historyShown is how much of a plugin's history it shows.
const historyShown = 100

// DefaultMaxArchive is how large an archive may be, unless an admin says.
const DefaultMaxArchive = 100 << 20

// Workflows pauses the active workflows a change to plugins leaves unable to run (D319).
type Workflows interface {
	Pause(ctx context.Context, why func(w domain.Workflow) string) (int, error)
}

// Limits bound what admins install.
type Limits struct {
	MaxArchive int64 // bytes of an archive
}

type Service struct {
	repo      ports.PluginRepository
	files     ports.PluginFiles
	runtime   ports.PluginRuntime
	workflows Workflows
	log       logging.Logger
	now       func() time.Time
	fetcher   ports.ArchiveFetcher // optional: without it, nothing installs from a URL
	notifier  ports.PluginNotifier // optional: without it, admins hear of no plugin that would not start
	limits    func() Limits

	mu      sync.RWMutex
	known   map[string]domain.InstalledPlugin // as admins last decided, for the gate
	loaded  map[string]string                 // the version of each plugin loaded at start
	failed  map[string]string                 // why a plugin did not load at start
	refresh error                             // why known could not be read, if it could not
}

type Option func(*Service)

// WithFetcher installs archives from URLs too.
func WithFetcher(f ports.ArchiveFetcher) Option {
	return func(s *Service) { s.fetcher = f }
}

// WithNotifier tells admins of plugins that would not start.
func WithNotifier(n ports.PluginNotifier) Option {
	return func(s *Service) { s.notifier = n }
}

// WithLimits reads the limits as each archive comes.
func WithLimits(l func() Limits) Option {
	return func(s *Service) { s.limits = l }
}

// WithClock replaces time.Now, for tests.
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

func New(repo ports.PluginRepository, files ports.PluginFiles, runtime ports.PluginRuntime, workflows Workflows,
	log logging.Logger, opts ...Option,
) *Service {
	s := &Service{
		repo: repo, files: files, runtime: runtime, workflows: workflows, log: log.With("component", "plugins"),
		now: time.Now, limits: func() Limits { return Limits{MaxArchive: DefaultMaxArchive} },
		known: map[string]domain.InstalledPlugin{}, loaded: map[string]string{}, failed: map[string]string{},
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Info is a plugin as admins see it: what they decided, how it runs, and whether a
// restart would change that (D317).
type Info struct {
	domain.InstalledPlugin

	Status  *ports.PluginStatus // as it was loaded at start; nil when it was not
	Restart bool                // what runs is not what admins decided: a restart applies it
}

// List is every plugin installed, and the versions on disk no admin installed, to adopt
// (D321).
func (s *Service) List(ctx context.Context, p auth.Principal) ([]Info, []ports.PluginFound, error) {
	if !p.User.IsAdmin() {
		return nil, nil, domain.ErrForbidden
	}

	installed, err := s.repo.List(ctx)
	if err != nil {
		return nil, nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	known := map[ports.PluginFound]bool{}
	out := make([]Info, len(installed))

	for i, ip := range installed {
		out[i] = Info{InstalledPlugin: ip, Status: s.status(ip.ID)}

		want := ""
		if ip.Enabled {
			want = ip.Version
		}

		out[i].Restart = want != s.loaded[ip.ID]

		for _, v := range ip.Versions {
			known[ports.PluginFound{ID: ip.ID, Version: v.Version}] = true
		}
	}

	onDisk, err := s.files.List()
	if err != nil {
		return nil, nil, err
	}

	found := slices.DeleteFunc(onDisk, func(f ports.PluginFound) bool { return known[f] })

	return out, found, nil
}

// status is how a plugin loaded at start is: as it runs, or why it did not load. Under
// s.mu.
func (s *Service) status(id string) *ports.PluginStatus {
	if st, ok := s.runtime.Status(id); ok {
		return &st
	}

	if why, ok := s.failed[id]; ok {
		return &ports.PluginStatus{Version: s.loaded[id], State: "failed", LastError: why}
	}

	return nil
}

// Installed is a version an admin installed: to approve, with the permissions it asks for
// that what runs was not approved for (D318), all of them for a plugin new here.
type Installed struct {
	File           ports.PluginFile
	NewPermissions []string
	Upgrade        bool // of a plugin installed before
}

// Install installs an archive an admin uploaded, refused whole when anything is wrong
// with it. It runs once an admin approves it, from the next start.
func (s *Service) Install(ctx context.Context, p auth.Principal, archive io.Reader) (Installed, error) {
	if !p.User.IsAdmin() {
		return Installed{}, domain.ErrForbidden
	}

	f, err := s.files.Install(archive, s.limits().MaxArchive)
	if err != nil {
		return Installed{}, err
	}

	in, err := s.record(ctx, p, f, "")
	if err != nil {
		// What the database does not know is not installed.
		if rmErr := s.files.Remove(f.ID, f.Version); rmErr != nil {
			s.log.Warn(ctx, "Plugin files not removed", "plugin", f.ID, "version", f.Version, logging.Err(rmErr))
		}
	}

	return in, err
}

// InstallFrom installs the archive at url.
func (s *Service) InstallFrom(ctx context.Context, p auth.Principal, url string) (Installed, error) {
	if !p.User.IsAdmin() {
		return Installed{}, domain.ErrForbidden
	}

	if s.fetcher == nil {
		return Installed{}, fmt.Errorf("%w: archives cannot be fetched here", domain.ErrInvalidInput)
	}

	body, err := s.fetcher.Fetch(ctx, url, s.limits().MaxArchive)
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = body.Close() }()

	return s.Install(ctx, p, body)
}

// Adopt installs a version found on disk that no admin installed (D321), checked as an
// archive would be.
func (s *Service) Adopt(ctx context.Context, p auth.Principal, id, version string) (Installed, error) {
	if !p.User.IsAdmin() {
		return Installed{}, domain.ErrForbidden
	}

	f, err := s.files.Open(id, version)
	if err != nil {
		return Installed{}, err
	}

	return s.record(ctx, p, f, "found on disk")
}

// record records a version installed, and says what it asks for that is new.
func (s *Service) record(ctx context.Context, p auth.Principal, f ports.PluginFile, detail string) (Installed, error) {
	before, err := s.repo.Get(ctx, f.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Installed{}, err
	}

	at := s.now()

	if detail == "" {
		detail = signedBy(f)
	}

	err = s.repo.AddVersion(ctx, domain.PluginVersion{
		PluginID: f.ID, Version: f.Version, Manifest: f.Manifest, Signer: f.Signer, KeyID: f.KeyID,
		InstalledBy: p.User.ID, InstalledAt: at,
	}, domain.PluginAudit{PluginID: f.ID, Version: f.Version, Action: domain.PluginInstalled, Detail: detail, By: p.User.ID, At: at})
	if err != nil {
		return Installed{}, err
	}

	s.log.Info(ctx, "Plugin installed", "plugin", f.ID, "version", f.Version, "by", p.User.ID, "signer", f.Signer)
	s.reload(ctx)

	in := Installed{File: f, NewPermissions: slices.Clone(f.Permissions), Upgrade: before.ID != ""}

	if runs, ok := before.Find(before.Version); ok {
		was, err := permissionsOf(runs)
		if err != nil {
			return Installed{}, err
		}

		in.NewPermissions = slices.DeleteFunc(in.NewPermissions, func(perm string) bool { return slices.Contains(was, perm) })
	}

	return in, nil
}

func signedBy(f ports.PluginFile) string {
	switch {
	case f.Signer != "":
		return "signed by " + f.Signer
	case f.Signed:
		return "signed by a key not trusted, " + f.KeyID
	default:
		return "not signed"
	}
}

// Approve approves a version an admin installed, and the permissions it asks for, which
// the admin names: approving others is refused, so nobody approves what they did not see.
// It becomes the version the plugin runs from the next start.
func (s *Service) Approve(ctx context.Context, p auth.Principal, id, version string, permissions []string) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	v, ok := ip.Find(version)

	switch {
	case !ok:
		return fmt.Errorf("%w: %s has no version %s", domain.ErrNotFound, id, version)
	case v.Approved():
		return fmt.Errorf("%w: %s %s is approved already", domain.ErrInvalidInput, id, version)
	}

	// Its files as they are now: what runs is what was approved.
	f, err := s.files.Open(id, version)
	if err != nil {
		return err
	}

	asked, err := permissionsOf(v)
	if err != nil {
		return err
	}

	if !sameSet(asked, f.Permissions) {
		return fmt.Errorf("%w: its files ask for other permissions than when it was installed", domain.ErrInvalidInput)
	}

	if !sameSet(asked, permissions) {
		return fmt.Errorf("%w: it asks for %s: approve those, and only those", domain.ErrInvalidInput, listOf(asked))
	}

	err = s.repo.Approve(ctx, domain.PluginAudit{
		PluginID: id, Version: version, Action: domain.PluginApproved, Detail: listOf(asked), By: p.User.ID, At: s.now(),
	})
	if err != nil {
		return err
	}

	s.log.Info(ctx, "Plugin approved", "plugin", id, "version", version, "by", p.User.ID, "permissions", listOf(asked))
	s.reload(ctx)

	return nil
}

// RemoveVersion removes a version not approved, an upgrade an admin will not have.
func (s *Service) RemoveVersion(ctx context.Context, p auth.Principal, id, version string) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	if version == ip.Version {
		return fmt.Errorf("%w: %s is the version that runs: uninstall the plugin instead", domain.ErrInvalidInput, version)
	}

	if err := s.repo.RemoveVersion(ctx, domain.PluginAudit{PluginID: id, Version: version, Action: domain.PluginRemoved, By: p.User.ID, At: s.now()}); err != nil {
		return err
	}

	s.removeFiles(ctx, id, version)
	s.reload(ctx)

	return nil
}

// SetEnabled enables or disables a plugin, from the next start (D317): the workflows
// using one disabled are paused then (D319).
func (s *Service) SetEnabled(ctx context.Context, p auth.Principal, id string, enabled bool) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	action := domain.PluginDisabled
	if enabled {
		action = domain.PluginEnabled
	}

	if err := s.repo.SetEnabled(ctx, enabled, domain.PluginAudit{PluginID: id, Action: action, By: p.User.ID, At: s.now()}); err != nil {
		return err
	}

	s.log.Info(ctx, "Plugin "+action, "plugin", id, "by", p.User.ID)
	s.reload(ctx)

	return nil
}

// Uninstall removes a plugin and its files. It runs until the next start, when the
// workflows using it are paused (D319); its history is kept.
func (s *Service) Uninstall(ctx context.Context, p auth.Principal, id string) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, domain.PluginAudit{PluginID: id, Action: domain.PluginUninstalled, By: p.User.ID, At: s.now()}); err != nil {
		return err
	}

	for _, v := range ip.Versions {
		s.removeFiles(ctx, id, v.Version)
	}

	s.log.Info(ctx, "Plugin uninstalled", "plugin", id, "by", p.User.ID)
	s.reload(ctx)

	return nil
}

// SetUsers says who may use a plugin (D282): everyone, or the users given. It applies at
// once: active workflows of owners who may no longer use it are paused (D319).
func (s *Service) SetUsers(ctx context.Context, p auth.Principal, id string, everyone bool, users []domain.UserID) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	detail := "everyone"
	if !everyone {
		detail = fmt.Sprintf("%d chosen users", len(users))
	}

	if err := s.repo.SetUsers(ctx, everyone, users, domain.PluginAudit{PluginID: id, Action: domain.PluginUsers, Detail: detail, By: p.User.ID, At: s.now()}); err != nil {
		return err
	}

	s.reload(ctx)

	n, err := s.workflows.Pause(ctx, s.refusedBy(false))
	if err != nil {
		return fmt.Errorf("who may use it is saved, but its workflows were not all paused: %w", err)
	}

	s.log.Info(ctx, "Plugin's users set", "plugin", id, "by", p.User.ID, "users", detail, "paused", n)

	return nil
}

// Start starts again a plugin stopped as crashed (D320).
func (s *Service) Start(ctx context.Context, p auth.Principal, id string) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	if err := s.runtime.Start(id); err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}

	return s.repo.Record(ctx, domain.PluginAudit{PluginID: id, Action: domain.PluginStarted, By: p.User.ID, At: s.now()})
}

// History is what admins did with a plugin, newest first, kept after it is gone.
func (s *Service) History(ctx context.Context, p auth.Principal, id string) ([]domain.PluginAudit, error) {
	if !p.User.IsAdmin() {
		return nil, domain.ErrForbidden
	}

	return s.repo.History(ctx, id, historyShown)
}

// Load loads the plugins that run, as the server starts (D317): each enabled one's
// approved version, its files checked again. One that does not start is left out, saying
// why, and the admins are told. Versions an upgrade replaced are removed once it runs.
// PauseGone then pauses the workflows that cannot run.
func (s *Service) Load(ctx context.Context) error {
	installed, err := s.repo.List(ctx)
	if err != nil {
		return err
	}

	for _, ip := range installed {
		if !ip.Enabled || ip.Version == "" {
			continue
		}

		s.mu.Lock()
		s.loaded[ip.ID] = ip.Version
		s.mu.Unlock()

		f, err := s.files.Open(ip.ID, ip.Version)
		if err == nil {
			err = s.runtime.Load(ctx, f)
		}

		if err != nil {
			s.mu.Lock()
			s.failed[ip.ID] = err.Error()
			s.mu.Unlock()

			s.log.Error(ctx, "Plugin not loaded", "plugin", ip.ID, "version", ip.Version, logging.Err(err))

			if s.notifier != nil {
				s.notifier.PluginCrashed(ip.ID, "it would not start: "+err.Error())
			}

			continue
		}

		s.log.Info(ctx, "Plugin loaded", "plugin", ip.ID, "version", ip.Version)

		s.sweep(ctx, ip)
	}

	s.reload(ctx)

	return s.refreshErr()
}

// PauseGone pauses the active workflows using plugins disabled or uninstalled, or ones
// their owners may no longer use (D319), telling the owners: once the server has loaded
// its plugins, before the workflows start.
func (s *Service) PauseGone(ctx context.Context) error {
	if err := s.refreshErr(); err != nil {
		return err
	}

	n, err := s.workflows.Pause(ctx, s.refusedBy(true))
	if err != nil {
		return fmt.Errorf("the workflows of plugins gone: %w", err)
	}

	if n > 0 {
		s.log.Info(ctx, "Workflows of plugins gone paused", "count", n)
	}

	return nil
}

// sweep removes the versions of a plugin an approved upgrade replaced, now that it runs.
func (s *Service) sweep(ctx context.Context, ip domain.InstalledPlugin) {
	for _, v := range ip.Versions {
		if !v.Approved() || v.Version == ip.Version {
			continue
		}

		err := s.repo.RemoveVersion(ctx, domain.PluginAudit{
			PluginID: ip.ID, Version: v.Version, Action: domain.PluginRemoved, Detail: "replaced by " + ip.Version, At: s.now(),
		})
		if err != nil {
			s.log.Warn(ctx, "Plugin version replaced not removed", "plugin", ip.ID, "version", v.Version, logging.Err(err))
			continue
		}

		s.removeFiles(ctx, ip.ID, v.Version)
	}
}

// refusedBy is why a workflow cannot run its plugins' nodes: a plugin gone, when gone
// counts (as the server starts, a disabled one not loaded), or one its owner may no
// longer use; empty when it can.
func (s *Service) refusedBy(gone bool) func(w domain.Workflow) string {
	return func(w domain.Workflow) string {
		s.mu.RLock()
		defer s.mu.RUnlock()

		for _, id := range pluginsOf(w) {
			ip, ok := s.known[id]

			switch {
			case !ok && gone:
				return "the plugin " + id + " is uninstalled"
			case ok && !ip.Enabled && gone:
				return "the plugin " + id + " is disabled"
			case ok && !ip.Allows(w.OwnerID):
				return "you may no longer use the plugin " + id
			}
		}

		return ""
	}
}

// PluginAllowed says whether owner may use the plugin (D282): one installed, everyone's
// or theirs. It answers from what admins last decided, read as they decide it.
func (s *Service) PluginAllowed(_ context.Context, owner domain.UserID, plugin string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.refresh != nil {
		return false, s.refresh
	}

	ip, ok := s.known[plugin]

	return ok && ip.Allows(owner), nil
}

func (s *Service) refreshErr() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.refresh
}

// reload reads what admins decided again, for the gate.
func (s *Service) reload(ctx context.Context) {
	installed, err := s.repo.List(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.refresh = fmt.Errorf("the plugins installed: %w", err)
		s.log.Warn(ctx, "Plugins not read", logging.Err(err))

		return
	}

	s.refresh = nil
	s.known = make(map[string]domain.InstalledPlugin, len(installed))

	for _, ip := range installed {
		s.known[ip.ID] = ip
	}
}

func (s *Service) removeFiles(ctx context.Context, id, version string) {
	if err := s.files.Remove(id, version); err != nil {
		s.log.Warn(ctx, "Plugin files not removed", "plugin", id, "version", version, logging.Err(err))
	}
}

// pluginsOf are the plugins whose nodes w has, by their types' names (D285), whether
// they are loaded or not.
func pluginsOf(w domain.Workflow) []string {
	ids := map[string]bool{}

	for _, n := range w.Nodes {
		rest, ok := strings.CutPrefix(n.Type, "plugin.")
		if id, _, ok2 := strings.Cut(rest, "."); ok && ok2 && id != "" {
			ids[id] = true
		}
	}

	return slices.Sorted(maps.Keys(ids))
}

// permissionsOf are the permissions a version's manifest asks for.
func permissionsOf(v domain.PluginVersion) ([]string, error) {
	var m struct {
		Permissions []string `json:"permissions"`
	}

	if err := json.Unmarshal(v.Manifest, &m); err != nil {
		return nil, fmt.Errorf("the manifest of %s %s: %w", v.PluginID, v.Version, err)
	}

	return m.Permissions, nil
}

func sameSet(a, b []string) bool {
	return slices.Equal(slices.Compact(slices.Sorted(slices.Values(a))), slices.Compact(slices.Sorted(slices.Values(b))))
}

func listOf(perms []string) string {
	if len(perms) == 0 {
		return "no permissions"
	}

	return strings.Join(slices.Sorted(slices.Values(perms)), ", ")
}
