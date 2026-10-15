package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// WaitRecord is a paused execution, kept until it resumes. State is the engine's own
// snapshot of the execution, as JSON.
type WaitRecord struct {
	ExecutionID domain.ExecutionID
	WorkflowID  domain.WorkflowID
	OwnerID     domain.UserID
	NodeID      domain.NodeID
	Until       time.Time // resumed then at the latest
	Key         string    // what an answer must bring; empty when it cannot be answered
	State       json.RawMessage
	CreatedAt   time.Time
}

// WaitRepository keeps paused executions. Taking a wait removes it, so each resumes
// once, whoever takes it first: a timer, an answer, or a cancel.
type WaitRepository interface {
	Add(ctx context.Context, w WaitRecord) error
	CountForOwner(ctx context.Context, owner domain.UserID) (int, error)
	// Take removes and returns the owner's wait of an execution; domain.ErrNotFound if
	// there is none, it is someone else's, or it was taken already.
	Take(ctx context.Context, owner domain.UserID, id domain.ExecutionID) (WaitRecord, error)
	// TakeWithKey is Take for a wait that carries key. Waits without a key are never
	// taken this way.
	TakeWithKey(ctx context.Context, owner domain.UserID, id domain.ExecutionID, key string) (WaitRecord, error)
	// TakeDue removes and returns up to limit waits whose time is up.
	TakeDue(ctx context.Context, now time.Time, limit int) ([]WaitRecord, error)
	// TakeForWorkflow removes and returns every wait of a workflow; owner scopes it.
	TakeForWorkflow(ctx context.Context, owner domain.UserID, id domain.WorkflowID) ([]WaitRecord, error)
}
