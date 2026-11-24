package domain

import (
	"errors"
	"time"
)

var (
	// ErrHooksOff means an admin keeps inbound webhooks off on this instance (D439).
	ErrHooksOff = errors.New("inbound webhooks are off on this instance")
	// ErrHookIdle means a hook's workflow is not on, or no longer starts from it: nothing
	// listens to it now.
	ErrHookIdle = errors.New("the workflow is not on: switch it on to take calls")
)

// Hook is an Inbound webhook trigger's secret address, /hooks/<secret> (D439).
type Hook struct {
	WorkflowID WorkflowID
	NodeID     NodeID
	OwnerID    UserID // its workflow's, as read
	Sealed     []byte // the secret, sealed to its hook, for its owner to copy again
	CreatedAt  time.Time
}
