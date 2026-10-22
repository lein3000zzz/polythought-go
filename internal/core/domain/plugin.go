package domain

import (
	"encoding/json"
	"errors"
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

// InstalledPlugin is a plugin admins installed, as they decided about it (D321): the
// version that runs, whether it does, who may use it.
type InstalledPlugin struct {
	ID       string
	Version  string // the approved version, the one that runs; empty until the first approval
	Enabled  bool
	Everyone bool     // false: only Users may use it (D282)
	Users    []UserID // when not Everyone

	Versions  []PluginVersion // newest first
	CreatedAt time.Time
	UpdatedAt time.Time
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
	PluginUninstalled = "uninstalled"
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
