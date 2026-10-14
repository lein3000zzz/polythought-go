package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// StoredSetting is a value an admin saved. Secrets are sealed, everything else is JSON.
type StoredSetting struct {
	Key    string
	Value  json.RawMessage // nil for secrets
	Sealed []byte          // secrets only
}

// SettingChange saves or clears one setting, and records the change.
type SettingChange struct {
	Key    string
	Reset  bool // clear the saved value, back to the default
	Value  json.RawMessage
	Sealed []byte

	// For the audit record: the values that applied before and after. Secrets leave
	// both out.
	Secret   bool
	OldValue json.RawMessage
	NewValue json.RawMessage
}

// SettingAudit records one change to a setting.
type SettingAudit struct {
	ID         string
	Key        string
	OldValue   json.RawMessage // the value that applied before; nil for a secret
	NewValue   json.RawMessage // and after; nil for a secret
	Secret     bool
	ActorID    domain.UserID
	ActorEmail string // empty once the actor's account is gone
	At         time.Time
}

type SettingsRepository interface {
	All(ctx context.Context) ([]StoredSetting, error)
	// Apply saves the changes and their audit records together.
	Apply(ctx context.Context, changes []SettingChange, actor domain.UserID, at time.Time) error
	// History lists changes, newest first.
	History(ctx context.Context, f PageFilter) (Page[SettingAudit], error)
}
