package domain

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var (
	// ErrStoreFull means a plugin node's store has no room for a value (D303).
	ErrStoreFull = errors.New("the node's store is full")

	// ErrPluginNotAllowed means a workflow's owner may not use a plugin of its nodes
	// (D282), or that it is not there.
	ErrPluginNotAllowed = errors.New("its owner may not use the plugin")

	// ErrPluginInstalled means a version of a plugin is installed already.
	ErrPluginInstalled = errors.New("that version of the plugin is installed already")
)

// pluginID is a plugin's ID, as its manifest has it (D285): a-z, 0-9 and dashes, starting
// with a letter.
var pluginID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// ValidPluginID says whether id is a plugin's ID as a manifest may have it: lowercase, so
// one plugin's directory is never another's where the filesystem ignores case (P27).
func ValidPluginID(id string) bool { return pluginID.MatchString(id) }

// InstalledPlugin is a plugin admins installed, as they decided about it (D321): the
// version that runs, whether it does, who may use it.
type InstalledPlugin struct {
	ID       string
	Version  string // the approved version, the one that runs; empty until the first approval
	Enabled  bool
	Everyone bool     // false: only Users may use it (D282)
	Users    []UserID // when not Everyone
	Caps     PluginCaps

	Versions  []PluginVersion // newest first
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PluginCaps are what one plugin may take, as its card sets them (D346): MiB of its data,
// its directory and its temporary one together (D341), and of its process's memory
// (D343); zero for the Config page's.
type PluginCaps struct {
	DataMiB   int
	MemoryMiB int
}

// The bounds of a plugin's caps, on its card as on the Config page.
const (
	MaxPluginDataMiB   = 1 << 20 // a TiB
	MinPluginMemoryMiB = 16
	MaxPluginMemoryMiB = 64 << 10
)

// PluginKey is the key a plugin's ID is bound to (D365): the one a trusted key verified on
// its first signed version. Its versions after are those it signs, and it stays while the
// plugin's data does (D366).
type PluginKey struct {
	PluginID string
	KeyID    string
	Signer   string // the trusted key's name, as it bound the ID
	BoundAt  time.Time
}

// Find is the plugin's version v, when it has it.
func (p InstalledPlugin) Find(v string) (PluginVersion, bool) {
	for _, pv := range p.Versions {
		if pv.Version == v {
			return pv, true
		}
	}

	return PluginVersion{}, false
}

// Allows says whether user may use the plugin (D282): an admin may restrict it to
// chosen users.
func (p InstalledPlugin) Allows(user UserID) bool {
	if p.Everyone {
		return true
	}

	for _, u := range p.Users {
		if u == user {
			return true
		}
	}

	return false
}

// PluginVersion is a version of a plugin installed: its manifest, who signed it, who
// installed and approved it.
type PluginVersion struct {
	PluginID string
	Version  string
	Manifest json.RawMessage
	Signer   string // the trusted key's name; empty for none an admin trusts
	KeyID    string // the signing key's ID; empty unsigned
	Digest   string // the SHA-256 of its checksums.txt, in hex: its files as installed

	InstalledBy UserID
	InstalledAt time.Time
	ApprovedBy  UserID
	ApprovedAt  time.Time // zero until approved
}

// Approved says an admin approved the version, and the permissions it asks for.
func (v PluginVersion) Approved() bool { return !v.ApprovedAt.IsZero() }

// What admins do with plugins, as their audit records it.
const (
	PluginInstalled   = "installed"
	PluginApproved    = "approved"
	PluginRemoved     = "version removed"
	PluginEnabled     = "enabled"
	PluginDisabled    = "disabled"
	PluginUsers       = "users"
	PluginStarted     = "started"
	PluginSettings    = "settings"
	PluginCapsSet     = "caps"
	PluginUninstalled = "uninstalled"
	PluginDataRemoved = "data removed"

	// What the server itself records: a version's digest, recorded as it was first opened
	// when it was recorded without one (P16); who signed it, as the keys admins trust now
	// say (P21); a version that would not load, or start again (P29).
	PluginDigest    = "digest recorded"
	PluginSigner    = "signer changed"
	PluginNotLoaded = "not loaded"
)

// PluginAudit is something an admin did with a plugin.
type PluginAudit struct {
	PluginID string
	Version  string
	Action   string
	Detail   string
	By       UserID // empty when the server did it itself
	At       time.Time
}
