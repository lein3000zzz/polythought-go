package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/services/auth"
)

// An admin's agent's or token's work on plugins waits for the admin where they say so
// (D411): approving a version, which runs another's code on the server, has a switch of its
// own; enabling, its users, settings and caps, starting, removing, the other. Secret
// settings are the browser's alone.

// Confirmer holds an action for its user to confirm: the confirmations service.
type Confirmer interface {
	Hold(ctx context.Context, p auth.Principal, a domain.Ask) error
}

// UseConfirmations has an admin's agents' and tokens' work on plugins wait for the admin.
// Call it once, as the server starts.
func (s *Service) UseConfirmations(c Confirmer) {
	s.confirm = c
}

// The plugin actions a held change carries out.
const (
	OpApprove       = "approve"
	OpRemoveVersion = "remove_version"
	OpEnable        = "enable"
	OpUsers         = "users"
	OpStart         = "start"
	OpUninstall     = "uninstall"
	OpCaps          = "caps"
	OpSettings      = "settings"
	OpRemoveLeft    = "remove_left_behind"
)

// Asked is what a held plugin action carries out, by Op.
type Asked struct {
	Op          string                     `json:"op"`
	ID          string                     `json:"id"`
	Version     string                     `json:"version,omitempty"`
	Permissions []string                   `json:"permissions,omitempty"`
	Enabled     bool                       `json:"enabled,omitempty"`
	Everyone    bool                       `json:"everyone,omitempty"`
	Users       []domain.UserID            `json:"users,omitempty"`
	RemoveData  bool                       `json:"removeData,omitempty"`
	Caps        domain.PluginCaps          `json:"caps"`
	Settings    map[string]json.RawMessage `json:"settings,omitempty"`
}

// hold keeps a token's or an agent's plugin action for the admin, saying what it does.
func (s *Service) hold(ctx context.Context, p auth.Principal, a Asked, what string) error {
	if s.confirm == nil || p.Itself() || p.Confirmed {
		return nil
	}

	kind := domain.ConfirmPluginChange
	if a.Op == OpApprove {
		kind = domain.ConfirmPluginApprove
	}

	return s.confirm.Hold(ctx, p, domain.Ask{Kind: kind, What: what, Payload: a})
}

func approveSays(id, version string, permissions []string) string {
	if len(permissions) == 0 {
		return fmt.Sprintf("Approve the plugin %s %s, to run on the server, asking for no permissions", id, version)
	}

	return fmt.Sprintf("Approve the plugin %s %s, to run on the server, with the permissions %s", id, version, strings.Join(permissions, ", "))
}
