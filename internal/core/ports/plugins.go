package ports

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// PluginStore keeps what plugin nodes store (D288): JSON values by key, for each node of a
// workflow and the plugin it is of. A node's store counts as its memory and goes as it
// does: NodeMemory's Count, Forget and ForgetOthers take it in.
type PluginStore interface {
	// Get is the value under key, and whether there is one.
	Get(ctx context.Context, n PluginNode, key string) (json.RawMessage, bool, error)

	// Set keeps value under key at now, in place of what was there. It is
	// domain.ErrStoreFull when the node's keys and values would take more than max bytes;
	// two at once count each other's.
	Set(ctx context.Context, n PluginNode, key string, value json.RawMessage, max int, now time.Time) error

	// Delete removes key, if it is there.
	Delete(ctx context.Context, n PluginNode, key string) error

	// Keys are the node's keys starting with prefix, in order, after after, at most limit.
	Keys(ctx context.Context, n PluginNode, prefix, after string, limit int) ([]string, error)
}

// PluginNode is a workflow's node, as a plugin's.
type PluginNode struct {
	Workflow domain.WorkflowID
	Node     domain.NodeID
	Plugin   string
}

// PluginRepository keeps what admins decided about plugins (D321). Each change comes
// with its audit record, which it writes with it.
type PluginRepository interface {
	List(ctx context.Context) ([]domain.InstalledPlugin, error)

	// Get is domain.ErrNotFound for a plugin not installed.
	Get(ctx context.Context, id string) (domain.InstalledPlugin, error)

	// AddVersion records a version installed, and its plugin when it is new, enabled
	// and for everyone; domain.ErrPluginInstalled when the version is there already.
	AddVersion(ctx context.Context, v domain.PluginVersion, a domain.PluginAudit) error

	// Approve approves a.Version, which becomes the one the plugin runs.
	Approve(ctx context.Context, a domain.PluginAudit) error

	// SetDigest records the digest of a.Version's files, recorded without one (P16): never
	// in place of one; domain.ErrNotFound then.
	SetDigest(ctx context.Context, digest string, a domain.PluginAudit) error

	// SetSigner records who signed a.Version, as the keys admins trust now say (P21).
	SetSigner(ctx context.Context, signer, keyID string, a domain.PluginAudit) error

	// RemoveVersion forgets a.Version: one not approved, or one an upgrade replaced.
	RemoveVersion(ctx context.Context, a domain.PluginAudit) error

	SetEnabled(ctx context.Context, enabled bool, a domain.PluginAudit) error

	// SetUsers says who may use the plugin: everyone, or the users given.
	SetUsers(ctx context.Context, everyone bool, users []domain.UserID, a domain.PluginAudit) error

	// SetCaps sets the plugin's own caps (D346).
	SetCaps(ctx context.Context, caps domain.PluginCaps, a domain.PluginAudit) error

	// Delete forgets the plugin, its audit kept; with stores, what its nodes stored in
	// every workflow too (D348), and the key its ID is bound to (D365).
	Delete(ctx context.Context, a domain.PluginAudit, stores bool) error

	// StoreBytes are the bytes plugins' nodes stored, by plugin, installed or not (D342).
	StoreBytes(ctx context.Context) (map[string]int64, error)

	// ForgetStores forgets what the nodes of a plugin no longer installed stored, and the
	// key its ID is bound to (D366).
	ForgetStores(ctx context.Context, a domain.PluginAudit) error

	// Key is the key a plugin's ID is bound to (D365), when it is.
	Key(ctx context.Context, id string) (domain.PluginKey, bool, error)

	// Bind binds a plugin's ID to a key, unless it is bound already: the first stays.
	Bind(ctx context.Context, k domain.PluginKey) error

	// Record records something done with a plugin that changes nothing kept here.
	Record(ctx context.Context, a domain.PluginAudit) error

	// History is what admins did with a plugin, newest first.
	History(ctx context.Context, id string, limit int) ([]domain.PluginAudit, error)
}

// PluginNotifier tells people what plugins did to them: owners of workflows paused
// (D319), admins of plugins that crashed for good (D320). Neither blocks.
type PluginNotifier interface {
	WorkflowPaused(owner domain.UserID, w domain.Workflow, why string)
	PluginCrashed(plugin, why string)
}

// PluginFiles keeps installed plugins' files on disk (D315): each version checked as it
// is installed, and again as it is opened to run.
type PluginFiles interface {
	// Install unpacks and checks an archive of at most max bytes, keeping it as the
	// version its manifest says; the error says what is wrong with it.
	Install(archive io.Reader, max int64) (PluginFile, error)

	// Open checks a version on disk again, as it is about to run, or be adopted.
	Open(id, version string) (PluginFile, error)

	Remove(id, version string) error

	// List is every version on disk.
	List() ([]PluginFound, error)

	// Data is every plugin's lasting directory on disk (D339), installed or not, and what
	// it takes, as the data cap counts it.
	Data() ([]PluginData, error)

	// RemoveData removes a plugin's lasting directory (D348).
	RemoveData(id string) error
}

// PluginData is a plugin's lasting directory, and the bytes it takes.
type PluginData struct {
	ID    string
	Bytes int64
}

// PluginFile is a version of a plugin on disk, checked: its manifest, its program for
// this server, and who signed it (D280).
type PluginFile struct {
	ID          string
	Version     string
	Name        string
	Permissions []string
	Manifest    json.RawMessage // as manifest.json has it, decoded and encoded again: one value, whoever reads it (P19)

	Program    string
	ProgramSum string // the program's SHA-256, in hex, as checksums.txt has it
	Dir        string // the version's directory, holding Program
	Data       string // its plugin's lasting directory, every version's (D339); maybe not made yet

	// Digest is the SHA-256, in hex, of the version's checksums.txt: its files as installed,
	// which an approval holds to (P16).
	Digest string

	Signed  bool
	Signer  string // the trusted key's name; empty for none an admin trusts
	KeyID   string
	Warning string // for the admin who approves it, when no trusted key signed it
}

// PluginFound is a version on disk.
type PluginFound struct {
	ID, Version string
}

// ArchiveFetcher fetches a plugin's archive from a URL an admin gives (5c), at most max
// bytes of it.
type ArchiveFetcher interface {
	Fetch(ctx context.Context, url string, max int64) (io.ReadCloser, error)
}

// PluginRuntime is the plugins this server runs, loaded as it starts (D317).
type PluginRuntime interface {
	// Load starts a version of a plugin, registering its nodes, triggers, credential
	// types and scenarios. A failure is its status's too.
	Load(ctx context.Context, f PluginFile) error

	// Status is how the plugin loaded is, if one was.
	Status(id string) (PluginStatus, bool)

	// Start starts again a plugin stopped as crashed (D320).
	Start(id string) error

	// Sandbox is whether plugins run under the sandbox here (D338).
	Sandbox() PluginSandbox
}

// PluginSandbox is whether this server sandboxes plugins: Why says why not, and
// Unsandboxed whether admins let them run without it then.
type PluginSandbox struct {
	Available   bool
	Why         string
	Unsandboxed bool
}

// PluginStatus is how a plugin loaded is.
type PluginStatus struct {
	Version   string
	State     string // running, restarting, crashed, stopped, or failed: it would not start
	Since     time.Time
	Crashes   int // in the last 10 minutes
	LastError string
	Log       []string // its last lines

	// Sandboxed says its process runs under the sandbox. Memory and Data are what it took
	// when last measured, in bytes, Memory zero where it is not watched; the caps are
	// those it is held to.
	Sandboxed bool
	Memory    int64
	MemoryCap int64
	Data      int64
	DataCap   int64
}

// StoredPluginSetting is a plugin's setting as kept: its value as JSON, or sealed for a
// secret.
type StoredPluginSetting struct {
	Key    string
	Value  json.RawMessage
	Sealed []byte
}

// PluginSettingChange sets a plugin's setting, or resets it to its default when Value and
// Sealed are both nil.
type PluginSettingChange struct {
	Key    string
	Value  json.RawMessage
	Sealed []byte
}

// PluginSettings keeps plugins' own settings (D329), audited as each change is made.
type PluginSettings interface {
	Settings(ctx context.Context, plugin string) ([]StoredPluginSetting, error)

	// SetSettings applies changes together, with their audit record.
	SetSettings(ctx context.Context, plugin string, changes []PluginSettingChange, a domain.PluginAudit) error
}
