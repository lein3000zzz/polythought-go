package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// NotificationPrefRepository keeps which kinds of notification users switched on or off.
type NotificationPrefRepository interface {
	// Prefs returns the kinds the user changed, by key; the others are at their defaults.
	Prefs(ctx context.Context, user domain.UserID) (map[string]bool, error)
	SetPref(ctx context.Context, user domain.UserID, kind string, enabled bool) error
}

// QuestionReminder reminds users of a question still open, unless they switched that off.
type QuestionReminder interface {
	// RemindQuestion replies to the question's message, which left is still open for.
	RemindQuestion(ctx context.Context, user domain.UserID, messageID int, left time.Duration) error
}
