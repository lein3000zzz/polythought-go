package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// ConfirmationRepository keeps the live actions waiting for their users (D406).
type ConfirmationRepository interface {
	Create(ctx context.Context, c domain.Confirmation) error
	// Get is one of owner's; domain.ErrNotFound if it is someone else's.
	Get(ctx context.Context, owner domain.UserID, id domain.ConfirmationID) (domain.Confirmation, error)
	// Recent are owner's, the newest first, at most limit.
	Recent(ctx context.Context, owner domain.UserID, limit int) ([]domain.Confirmation, error)
	// CountPending counts owner's still pending at now.
	CountPending(ctx context.Context, owner domain.UserID, now time.Time) (int, error)
	// Decide moves one of owner's pending at now to status, once: domain.ErrConflict if it
	// is not pending any more, or has expired.
	Decide(
		ctx context.Context, owner domain.UserID, id domain.ConfirmationID, status domain.ConfirmStatus, now time.Time, via domain.ConfirmVia,
	) (domain.Confirmation, error)
	// Finish records how a confirmed one ended.
	Finish(ctx context.Context, id domain.ConfirmationID, status domain.ConfirmStatus, result string) error
	// PendingOf are the ones an agent's grant, or an API token, asked for still pending at
	// now: what its revoking refuses.
	PendingOf(ctx context.Context, grant domain.GrantID, token domain.APITokenID, now time.Time) ([]domain.Confirmation, error)
	// SetMessage records the Telegram message that asked the user.
	SetMessage(ctx context.Context, id domain.ConfirmationID, message int) error
}
