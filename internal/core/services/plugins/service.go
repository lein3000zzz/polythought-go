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
	"net/url"
	"reflect"
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
	settings  ports.PluginSettings // optional: without it, plugins have no settings of their own
	box       ports.SecretBox

	mu          sync.RWMutex
	runSettings map[string]map[string]any         // what each plugin's runs get of its settings
	known       map[string]domain.InstalledPlugin // as admins last decided, for the gate
	loaded      map[string]string                 // the version of each plugin loaded at start
	failed      map[string]string                 // why a plugin did not load at start
	refresh     error                             // why known could not be read, if it could not
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

	return s.install(ctx, p, archive, "uploaded")
}

// install installs an archive from where its audit says (P29).
func (s *Service) install(ctx context.Context, p auth.Principal, archive io.Reader, from string) (Installed, error) {
	f, err := s.files.Install(archive, s.limits().MaxArchive)
	if err != nil {
		return Installed{}, err
	}

	in, err := s.record(ctx, p, f, from)
	if err != nil {
		// What the database does not know is not installed.
		if rmErr := s.files.Remove(f.ID, f.Version); rmErr != nil {
			s.log.Warn(ctx, "Plugin files not removed", "plugin", f.ID, "version", f.Version, logging.Err(rmErr))
		}
	}

	return in, err
}

// InstallFrom installs the archive at addr.
func (s *Service) InstallFrom(ctx context.Context, p auth.Principal, addr string) (Installed, error) {
	if !p.User.IsAdmin() {
		return Installed{}, domain.ErrForbidden
	}

	if s.fetcher == nil {
		return Installed{}, fmt.Errorf("%w: archives cannot be fetched here", domain.ErrInvalidInput)
	}

	body, err := s.fetcher.Fetch(ctx, addr, s.limits().MaxArchive)
	if err != nil {
		return Installed{}, err
	}
	defer func() { _ = body.Close() }()

	return s.install(ctx, p, body, fetchedFrom(addr))
}

// fetchedFrom is where an archive was fetched from, as its audit says (P29): without its
// query, fragment and user, which can hold a secret.
func fetchedFrom(addr string) string {
	u, err := url.Parse(addr)
	if err != nil {
		return "fetched from a URL"
	}

	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""

	return "fetched from " + u.String()
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

// record records a version installed from where from says, and says what it asks for that
// is new. Its audit says where it came from, who signed it and its digest (P29).
func (s *Service) record(ctx context.Context, p auth.Principal, f ports.PluginFile, from string) (Installed, error) {
	before, err := s.repo.Get(ctx, f.ID)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return Installed{}, err
	}

	at := s.now()
	detail := from + "; " + signedBy(f.Signer, f.KeyID) + "; digest " + f.Digest

	err = s.repo.AddVersion(ctx, domain.PluginVersion{
		PluginID: f.ID, Version: f.Version, Manifest: f.Manifest, Signer: f.Signer, KeyID: f.KeyID, Digest: f.Digest,
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

// signedBy is who signed a version, as its audit says: a key's ID is there when it is
// signed.
func signedBy(signer, keyID string) string {
	switch {
	case signer != "":
		return "signed by " + signer + ", key " + keyID
	case keyID != "":
		return "signed by a key not trusted, " + keyID
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
	f, err := s.opened(ctx, v)
	if err != nil {
		return err
	}

	// The page said a key the admins trusted signed it; it was told so when it was opened (P21).
	if v.Signer != "" && f.Signer == "" {
		s.reload(ctx)

		return fmt.Errorf("%w: %s, which signed it, is no longer a key you trust: look at it again before you approve it",
			domain.ErrInvalidInput, v.Signer)
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
		PluginID: id, Version: version, Action: domain.PluginApproved, Detail: listOf(asked) + "; digest " + f.Digest,
		By: p.User.ID, At: s.now(),
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
// workflows using it are paused (D319); its history is kept. Its data, its directory and
// what its nodes stored, goes with it when removeData says (D348), and is otherwise left
// behind for a reinstall to find (D342).
func (s *Service) Uninstall(ctx context.Context, p auth.Principal, id string, removeData bool) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	detail := "its data kept"
	if removeData {
		detail = "its data removed"
	}

	audit := domain.PluginAudit{PluginID: id, Action: domain.PluginUninstalled, Detail: detail, By: p.User.ID, At: s.now()}
	if err := s.repo.Delete(ctx, audit, removeData); err != nil {
		return err
	}

	for _, v := range ip.Versions {
		s.removeFiles(ctx, id, v.Version)
	}

	if removeData {
		// Left behind, it is listed as such, to remove again.
		if err := s.files.RemoveData(id); err != nil {
			s.log.Warn(ctx, "Plugin data not removed", "plugin", id, logging.Err(err))
		}
	}

	s.log.Info(ctx, "Plugin uninstalled", "plugin", id, "by", p.User.ID, "data_removed", removeData)
	s.reload(ctx)

	return nil
}

// SetCaps sets a plugin's own caps (D346), zero for the Config page's. They apply as it is
// next measured; its memory's soft limit from its next start.
func (s *Service) SetCaps(ctx context.Context, p auth.Principal, id string, caps domain.PluginCaps) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	problems := domain.ValidationError{Base: domain.ErrInvalidInput}

	if caps.DataMiB < 0 || caps.DataMiB > domain.MaxPluginDataMiB {
		problems.Add(domain.Problem{Param: "dataCap", Message: fmt.Sprintf("A data cap is 1 to %d MiB.", domain.MaxPluginDataMiB)})
	}

	if caps.MemoryMiB != 0 && (caps.MemoryMiB < domain.MinPluginMemoryMiB || caps.MemoryMiB > domain.MaxPluginMemoryMiB) {
		problems.Add(domain.Problem{
			Param: "memoryCap", Message: fmt.Sprintf("A memory cap is %d to %d MiB.", domain.MinPluginMemoryMiB, domain.MaxPluginMemoryMiB),
		})
	}

	if err := problems.Err(); err != nil {
		return err
	}

	audit := domain.PluginAudit{PluginID: id, Action: domain.PluginCapsSet, Detail: capsDetail(caps), By: p.User.ID, At: s.now()}
	if err := s.repo.SetCaps(ctx, caps, audit); err != nil {
		return err
	}

	s.log.Info(ctx, "Plugin caps set", "plugin", id, "by", p.User.ID, "data_mib", caps.DataMiB, "memory_mib", caps.MemoryMiB)
	s.reload(ctx)

	return nil
}

// capsDetail is what a plugin's audit says of its caps.
func capsDetail(c domain.PluginCaps) string {
	said := func(what string, mib int) string {
		if mib == 0 {
			return what + ": the Config page's"
		}

		return fmt.Sprintf("%s: %d MiB", what, mib)
	}

	return said("data", c.DataMiB) + "; " + said("memory", c.MemoryMiB)
}

// CapsOf are a plugin's own caps (D346), as admins last set them: zero for the Config
// page's.
func (s *Service) CapsOf(id string) domain.PluginCaps {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.known[id].Caps
}

// Sandbox is whether this server runs plugins under the sandbox (D338).
func (s *Service) Sandbox(p auth.Principal) (ports.PluginSandbox, error) {
	if !p.User.IsAdmin() {
		return ports.PluginSandbox{}, domain.ErrForbidden
	}

	return s.runtime.Sandbox(), nil
}

// LeftData is what a plugin no longer installed left behind (D342): bytes of its directory,
// and of what its nodes stored.
type LeftData struct {
	ID     string
	Files  int64
	Stored int64
}

// LeftBehind is the data of plugins uninstalled without it, by plugin.
func (s *Service) LeftBehind(ctx context.Context, p auth.Principal) ([]LeftData, error) {
	if !p.User.IsAdmin() {
		return nil, domain.ErrForbidden
	}

	installed, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}

	data, err := s.files.Data()
	if err != nil {
		return nil, err
	}

	stores, err := s.repo.LeftStores(ctx)
	if err != nil {
		return nil, err
	}

	byID := map[string]*LeftData{}
	left := func(id string) *LeftData {
		if byID[id] == nil {
			byID[id] = &LeftData{ID: id}
		}

		return byID[id]
	}

	for _, d := range data {
		if !slices.ContainsFunc(installed, func(ip domain.InstalledPlugin) bool { return ip.ID == d.ID }) {
			left(d.ID).Files = d.Bytes
		}
	}

	for id, n := range stores {
		left(id).Stored = n
	}

	out := make([]LeftData, 0, len(byID))
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		out = append(out, *byID[id])
	}

	return out, nil
}

// RemoveLeftBehind removes what a plugin no longer installed left behind: never an
// installed one's, which goes as it is uninstalled.
func (s *Service) RemoveLeftBehind(ctx context.Context, p auth.Principal, id string) error {
	if !p.User.IsAdmin() {
		return domain.ErrForbidden
	}

	// As a manifest has it, so never an installed one's by another case (P27).
	if !domain.ValidPluginID(id) {
		return fmt.Errorf("%w: %q is no plugin's ID", domain.ErrInvalidInput, id)
	}

	switch _, err := s.repo.Get(ctx, id); {
	case err == nil:
		return fmt.Errorf("%w: %s is installed: uninstall it, removing its data", domain.ErrInvalidInput, id)
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}

	if err := s.files.RemoveData(id); err != nil {
		return err
	}

	audit := domain.PluginAudit{PluginID: id, Action: domain.PluginDataRemoved, Detail: "left behind as it was uninstalled", By: p.User.ID, At: s.now()}
	if err := s.repo.ForgetStores(ctx, audit); err != nil {
		return err
	}

	s.log.Info(ctx, "Plugin data left behind removed", "plugin", id, "by", p.User.ID)

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

		err := fmt.Errorf("%w: %s has no version %s", domain.ErrNotFound, ip.ID, ip.Version)
		if v, ok := ip.Find(ip.Version); ok {
			// Its files held to the version approved, their manifest the approved one (P16):
			// the runtime gets it as the files have it, since jsonb orders keys anew, and the
			// program must describe itself in it word for word (D324).
			var f ports.PluginFile
			if f, err = s.opened(ctx, v); err == nil {
				err = s.runtime.Load(ctx, f)
			}
		}

		if err != nil {
			s.mu.Lock()
			s.failed[ip.ID] = err.Error()
			s.mu.Unlock()

			s.log.Error(ctx, "Plugin not loaded", "plugin", ip.ID, "version", ip.Version, logging.Err(err))
			s.note(ctx, domain.PluginAudit{PluginID: ip.ID, Version: ip.Version, Action: domain.PluginNotLoaded, Detail: err.Error(), At: s.now()})

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

// CheckFiles checks a version's files again before the runtime starts it again after a
// crash (D322): held to what was recorded of them (P16), a refusal recorded in its plugin's
// history (P29).
func (s *Service) CheckFiles(ctx context.Context, id, version string) error {
	ip, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}

	v, ok := ip.Find(version)
	if !ok {
		return fmt.Errorf("%w: %s has no version %s", domain.ErrNotFound, id, version)
	}

	if _, err := s.opened(ctx, v); err != nil {
		s.note(ctx, domain.PluginAudit{PluginID: id, Version: version, Action: domain.PluginNotLoaded, Detail: "as it started again: " + err.Error(), At: s.now()})
		return err
	}

	return nil
}

// opened opens a version's files, held to what was recorded of them (P16): their digest,
// recorded now for a version recorded without one, and their manifest, the one installed
// and approved. Who signed them is recorded again when the keys admins trust now say
// otherwise (P21).
func (s *Service) opened(ctx context.Context, v domain.PluginVersion) (ports.PluginFile, error) {
	f, err := s.files.Open(v.PluginID, v.Version)
	if err != nil {
		return ports.PluginFile{}, err
	}

	tampered := func(what string) error {
		return fmt.Errorf("%w: %s %s: %s", domain.ErrInvalidInput, v.PluginID, v.Version, what)
	}

	switch {
	case f.Digest == "":
		return ports.PluginFile{}, tampered("its files have no digest")
	case v.Digest != "" && f.Digest != v.Digest:
		return ports.PluginFile{}, tampered(fmt.Sprintf("its files are not those installed: their digest is %s, not %s", f.Digest, v.Digest))
	}

	same, err := sameManifest(v.Manifest, f.Manifest)
	if err != nil {
		return ports.PluginFile{}, err
	}

	if !same {
		return ports.PluginFile{}, tampered("its files' manifest is not the one installed")
	}

	audit := domain.PluginAudit{PluginID: v.PluginID, Version: v.Version, At: s.now()}

	if v.Digest == "" {
		audit.Action, audit.Detail = domain.PluginDigest, f.Digest
		if err := s.repo.SetDigest(ctx, f.Digest, audit); err != nil {
			return ports.PluginFile{}, fmt.Errorf("its digest not recorded: %w", err)
		}
	}

	if f.Signer != v.Signer || f.KeyID != v.KeyID {
		audit.Action, audit.Detail = domain.PluginSigner, signedBy(f.Signer, f.KeyID)+"; was "+signedBy(v.Signer, v.KeyID)
		if err := s.repo.SetSigner(ctx, f.Signer, f.KeyID, audit); err != nil {
			return ports.PluginFile{}, fmt.Errorf("its signer not recorded: %w", err)
		}

		s.log.Warn(ctx, "Plugin's signer changed", "plugin", v.PluginID, "version", v.Version, "signer", f.Signer, "was", v.Signer)
	}

	return f, nil
}

// sameManifest says whether two manifests say the same, as JSON reads them: whatever the
// order of their keys, the form of their numbers, and members empty or not there, which
// one kept before P19 may have.
func sameManifest(a, b json.RawMessage) (bool, error) {
	var va, vb any

	if err := json.Unmarshal(a, &va); err != nil {
		return false, fmt.Errorf("a manifest: %w", err)
	}

	if err := json.Unmarshal(b, &vb); err != nil {
		return false, fmt.Errorf("a manifest: %w", err)
	}

	return reflect.DeepEqual(withoutEmpty(va), withoutEmpty(vb)), nil
}

// withoutEmpty is a JSON value with its objects' members null, false, "", [] or {} left
// out, as omitempty leaves them.
func withoutEmpty(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))

		for k, m := range v {
			m = withoutEmpty(m)

			switch m := m.(type) {
			case nil:
				continue
			case bool:
				if !m {
					continue
				}
			case string:
				if m == "" {
					continue
				}
			case []any:
				if len(m) == 0 {
					continue
				}
			case map[string]any:
				if len(m) == 0 {
					continue
				}
			}

			out[k] = m
		}

		return out
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			out[i] = withoutEmpty(e)
		}

		return out
	default:
		return v
	}
}

// note records what the server did with a plugin in its history.
func (s *Service) note(ctx context.Context, a domain.PluginAudit) {
	if err := s.repo.Record(ctx, a); err != nil {
		s.log.Warn(ctx, "Plugin's history not written", "plugin", a.PluginID, "action", a.Action, logging.Err(err))
	}
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

// reload reads what admins decided again, for the gate, and the plugins' settings, for
// their runs.
func (s *Service) reload(ctx context.Context) {
	installed, err := s.repo.List(ctx)

	runSettings := make(map[string]map[string]any, len(installed))

	for _, ip := range installed {
		specs, serr := specsOf(ip)
		values, _, verr := s.values(ctx, ip)

		if serr != nil || verr != nil {
			s.log.Warn(ctx, "Plugin settings not read", "plugin", ip.ID, logging.Err(errors.Join(serr, verr)))
			continue
		}

		runSettings[ip.ID] = encodedValues(specs, values)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err != nil {
		s.refresh = fmt.Errorf("the plugins installed: %w", err)
		s.log.Warn(ctx, "Plugins not read", logging.Err(err))

		return
	}

	s.refresh = nil
	s.known = make(map[string]domain.InstalledPlugin, len(installed))
	s.runSettings = runSettings

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
