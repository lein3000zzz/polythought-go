package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrExecutionFinished = errors.New("execution already finished")

type ExecutionStatus string

const (
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionSucceeded ExecutionStatus = "succeeded"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionCancelled ExecutionStatus = "cancelled"
)

func (s ExecutionStatus) Terminal() bool {
	return s == ExecutionSucceeded || s == ExecutionFailed || s == ExecutionCancelled
}

type NodeRunStatus string

const (
	NodeRunSucceeded NodeRunStatus = "succeeded"
	NodeRunFailed    NodeRunStatus = "failed"
	NodeRunSkipped   NodeRunStatus = "skipped" // no input reached the node
)

// Execution is one run of a workflow, started by a trigger.
type Execution struct {
	ID              ExecutionID
	WorkflowID      WorkflowID
	WorkflowVersion int // the definition that ran
	TriggeredBy     NodeID
	Status          ExecutionStatus
	StartedAt       time.Time
	FinishedAt      time.Time
	Runs            []NodeRun
	Error           string
}

// NodeRun records one node's part in an execution, with item counts per port.
type NodeRun struct {
	NodeID     NodeID
	Status     NodeRunStatus
	StartedAt  time.Time
	FinishedAt time.Time
	Input      map[string]int
	Output     map[string]int
	Error      string

	// Samples are the first items of each output port, for executions that keep them;
	// Output has the full counts.
	Samples map[string][]ItemSample
}

// ItemSample is an item a node emitted, as JSON.
type ItemSample struct {
	JSON  json.RawMessage
	Error string // why the item failed, for items on the error port
}

func NewExecution(id ExecutionID, workflowID WorkflowID, trigger NodeID, at time.Time) Execution {
	return Execution{
		ID:          id,
		WorkflowID:  workflowID,
		TriggeredBy: trigger,
		Status:      ExecutionRunning,
		StartedAt:   at,
	}
}

func (e *Execution) Record(run NodeRun) error {
	if e.Status.Terminal() {
		return fmt.Errorf("record node %q: %w", run.NodeID, ErrExecutionFinished)
	}

	e.Runs = append(e.Runs, run)

	return nil
}

// Finish moves the execution to a terminal status. cause is kept as the error message
// and must be nil for a success.
func (e *Execution) Finish(status ExecutionStatus, at time.Time, cause error) error {
	switch {
	case e.Status.Terminal():
		return ErrExecutionFinished
	case !status.Terminal():
		return fmt.Errorf("finish with non-terminal status %q", status)
	case status == ExecutionSucceeded && cause != nil:
		return fmt.Errorf("finish as succeeded with error: %w", cause)
	}

	e.Status = status
	e.FinishedAt = at

	if cause != nil {
		e.Error = cause.Error()
	}

	return nil
}

// Duration is zero until the execution finishes.
func (e Execution) Duration() time.Duration {
	if e.FinishedAt.IsZero() {
		return 0
	}

	return e.FinishedAt.Sub(e.StartedAt)
}
