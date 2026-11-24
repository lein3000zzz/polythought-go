package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// HookRepository keeps the Inbound webhook triggers' secret addresses (D439).
type HookRepository interface {
	// Hook is a trigger's, domain.ErrNotFound for none yet.
	Hook(ctx context.Context, workflow domain.WorkflowID, node domain.NodeID) (domain.Hook, error)
	// ByHash is the hook whose secret hashes to hash, with its workflow's owner;
	// domain.ErrNotFound for none.
	ByHash(ctx context.Context, hash []byte) (domain.Hook, error)
	// Put keeps h, its secret hashing to hash, in place of any the trigger had.
	Put(ctx context.Context, h domain.Hook, hash []byte) error
}

// HookCall is one call to a hook, to start a run with.
type HookCall struct {
	// Execution is the ID its run takes, given to the caller before it starts.
	Execution domain.ExecutionID
	Body      any            // its JSON, read, or its text; nil for none
	Query     map[string]any // each name's value, or values in a list when given more than once
	At        time.Time
}

// HookInbox hands Inbound webhook triggers their calls.
type HookInbox interface {
	// Listen delivers the calls to a trigger, until cancel is called; a call that comes
	// while too many wait for runs is refused.
	Listen(workflow domain.WorkflowID, node domain.NodeID) (calls <-chan HookCall, cancel func())
}
