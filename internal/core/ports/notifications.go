package ports

import (
	"context"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// NotificationPrefRepository keeps which kinds of notification users switched on or off.
type NotificationPrefRepository interface {
	// Prefs returns the kinds the user changed, by key; the others are at their defaults.
	Prefs(ctx context.Context, user domain.UserID) (map[string]bool, error)
	SetPref(ctx context.Context, user domain.UserID, kind string, enabled bool) error
}
