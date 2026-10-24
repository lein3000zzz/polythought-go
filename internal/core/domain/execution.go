package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrExecutionFinished = errors.New("execution already finished")
	ErrNotWaiting        = errors.New("the execution is not waiting")
)

type ExecutionStatus string

const (
	ExecutionRunning   ExecutionStatus = "running"
	ExecutionWaiting   ExecutionStatus = "waiting" // paused at a node, until a time or an answer
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
	NodeRunSkipped   NodeRunStatus = "skipped"   // no input reached the node
	NodeRunWaiting   NodeRunStatus = "waiting"   // the node paused the execution
	NodeRunCancelled NodeRunStatus = "cancelled" // the execution was cancelled while it waited
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

	// WaitingOn is the node a waiting execution paused at, and WaitingUntil when the
	// wait ends at the latest. Both are empty unless the status is waiting.
	WaitingOn    NodeID
	WaitingUntil time.Time

	// By is the agent or token behind it (D407): one that ran it by hand, or switched its
	// workflow on, or changed it while on; zero for the user.
	By Actor
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

// Wait pauses a running execution at node, until until at the latest.
func (e *Execution) Wait(node NodeID, until time.Time) error {
	if e.Status != ExecutionRunning {
		return fmt.Errorf("wait at node %q: execution is %s", node, e.Status)
	}

	e.Status, e.WaitingOn, e.WaitingUntil = ExecutionWaiting, node, until

	return nil
}

// Resume carries on a waiting execution.
func (e *Execution) Resume() error {
	if e.Status != ExecutionWaiting {
		return fmt.Errorf("resume: execution is %s", e.Status)
	}

	e.Status, e.WaitingOn, e.WaitingUntil = ExecutionRunning, "", time.Time{}

	return nil
}

// UpdateRun replaces the last run of the node, such as the one that waited, with run.
func (e *Execution) UpdateRun(run NodeRun) error {
	for i := len(e.Runs) - 1; i >= 0; i-- {
		if e.Runs[i].NodeID == run.NodeID {
			e.Runs[i] = run
			return nil
		}
	}

	return fmt.Errorf("update run: node %q has not run", run.NodeID)
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
	e.WaitingOn, e.WaitingUntil = "", time.Time{}

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
