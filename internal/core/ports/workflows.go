package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

type PageFilter struct {
	Limit  int
	Cursor string
}

// WorkflowRepository scopes lookups to the owner: someone else's workflow is not found.
type WorkflowRepository interface {
	Create(ctx context.Context, w domain.Workflow) error
	Get(ctx context.Context, owner domain.UserID, id domain.WorkflowID) (domain.Workflow, error)
	List(ctx context.Context, owner domain.UserID, f PageFilter) (Page[domain.Workflow], error)
	// Update saves w if the stored version is still expectedVersion, else returns
	// domain.ErrConflict. The stored version becomes w.Version.
	Update(ctx context.Context, w domain.Workflow, expectedVersion int) error
	// SetActive switches it on or off, by naming who is behind its runs from now (D407):
	// the zero Actor for the user, and when off.
	SetActive(ctx context.Context, owner domain.UserID, id domain.WorkflowID, active bool, at time.Time, by domain.Actor) error
	Delete(ctx context.Context, owner domain.UserID, id domain.WorkflowID) error
	Count(ctx context.Context, owner domain.UserID) (total, active int, err error)
	// DeactivateAll turns off every active workflow of owner and returns their IDs.
	DeactivateAll(ctx context.Context, owner domain.UserID, at time.Time) ([]domain.WorkflowID, error)
	// ListActive returns the active workflows of every enabled user, for starting them
	// on boot.
	ListActive(ctx context.Context) ([]domain.Workflow, error)
}

// ExecutionRecorder keeps finished executions.
type ExecutionRecorder interface {
	Save(ctx context.Context, e domain.Execution) error
}

// ExecutionNotifier hears about every finished execution once it is recorded. It must
// not block: the executor calls it on the execution's own goroutine.
type ExecutionNotifier interface {
	ExecutionFinished(owner domain.UserID, e domain.Execution)
}

type ExecutionRepository interface {
	ExecutionRecorder
	// List and Get see only executions of the given workflow; callers check ownership
	// of the workflow first.
	List(ctx context.Context, workflowID domain.WorkflowID, f PageFilter) (Page[domain.Execution], error)
	Get(ctx context.Context, workflowID domain.WorkflowID, id domain.ExecutionID) (domain.Execution, error)
	DeleteFinishedBefore(ctx context.Context, before time.Time) (int64, error)
}
