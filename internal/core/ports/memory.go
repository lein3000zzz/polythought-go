package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// MaxRemembered is the most entries one node remembers (D138): past it the oldest go
// first.
const MaxRemembered = 10_000

// NodeMemory keeps what nodes remember across runs and restarts (D121): per workflow
// node, entries of a key and a member, with when they were remembered, each until it
// expires. A node's memory outlives changes to its settings (D139), not the node or its
// workflow.
type NodeMemory interface {
	// Remember records keys for a node at now, until expires, and returns those it did
	// not hold yet, or held expired, in the order given. A key given twice counts once.
	// Past MaxRemembered the oldest are forgotten.
	Remember(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, keys []string, now, expires time.Time) ([]string, error)

	// Admit lets members in under key, as many as keep it within limit entries
	// remembered after since, and remembers them at now until expires. It returns those
	// let in, the first ones given; two calls at once count each other's.
	Admit(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, key string, members []string, limit int, since, now, expires time.Time) ([]string, error)

	// Gather remembers member under key at now until expires, again if it was there, and
	// says how many members the key holds remembered after since, it among them; two
	// calls at once count each other's.
	Gather(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, key, member string, since, now, expires time.Time) (int, error)

	// Count is how many entries a node remembers at now.
	Count(ctx context.Context, wf domain.WorkflowID, node domain.NodeID, now time.Time) (int, error)

	// Forget clears what a node remembers.
	Forget(ctx context.Context, wf domain.WorkflowID, node domain.NodeID) error

	// ForgetOthers clears what the workflow's nodes not in keep remember: they are gone.
	ForgetOthers(ctx context.Context, wf domain.WorkflowID, keep []domain.NodeID) error

	// Sweep forgets every key expired at now, and says how many.
	Sweep(ctx context.Context, now time.Time) (int, error)
}
