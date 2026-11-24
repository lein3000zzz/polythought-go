package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// ScheduleFires keeps when each schedule last fired, for one that makes up a run it
// missed while Polythought was down (D440).
type ScheduleFires interface {
	// LastFire is the run the schedule last fired for, by when it was due; zero for none.
	LastFire(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID) (time.Time, error)
	Fired(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID, due time.Time) error
}
