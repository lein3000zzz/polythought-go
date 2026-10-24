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
	Until       time.Time     // resumed then at the latest
	Key         string        // what an answer must bring; empty when it cannot be answered
	Question    *WaitQuestion // what it asks, for answering it elsewhere; nil when it asks nothing
	Task        *WaitTask     // the task it hands the owner's agents; nil when it hands none
	State       json.RawMessage
	CreatedAt   time.Time

	// Lease is the agent a task is handed to, and until when, while it is (D412).
	Lease *TaskLease
}

// WaitTask is the task a local agent node hands its owner's agents (D386): the prompt as
// rendered, for all the items together or for each, and the shape an answer keeps to.
type WaitTask struct {
	Call     string          `json:"call"`             // each: an answer per item; together: one for all
	AnswerAs string          `json:"answerAs"`         // text, fields or schema
	Schema   json.RawMessage `json:"schema,omitempty"` // what a JSON answer keeps to, answering in fields or to a schema
	Prompt   string          `json:"prompt,omitempty"` // together: the one prompt
	Items    []TaskItem      `json:"items,omitempty"`  // each: one per item; together: the items, where included
}

// TaskItem is one item of a task: its prompt, answering each, and the item itself.
type TaskItem struct {
	Prompt string         `json:"prompt,omitempty"`
	Item   map[string]any `json:"item,omitempty"`
}

// TaskLease is a task handed to one agent (D412): who has it, and until when, unless it
// waits for another again or answers.
type TaskLease struct {
	Holder string // the grant or the API token: "grant:<id>" or "token:<id>"
	Name   string // the agent or token, by its name then (D413)
	Until  time.Time
}

// WaitQuestion is what a wait that can be answered asks (D405): its text as it was sent,
// HTML, and the options an answer picks one of, by index.
type WaitQuestion struct {
	HTML    string   `json:"html"`
	Options []string `json:"options"`
}

// WaitRepository keeps paused executions. Taking a wait removes it, so each resumes
// once, whoever takes it first: a timer, an answer, or a cancel.
type WaitRepository interface {
	Add(ctx context.Context, w WaitRecord) error
	CountForOwner(ctx context.Context, owner domain.UserID) (int, error)
	// Get returns the owner's wait of an execution, leaving it; domain.ErrNotFound as Take.
	Get(ctx context.Context, owner domain.UserID, id domain.ExecutionID) (WaitRecord, error)
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

	// LeaseTask hands the owner's oldest task no agent holds, or whose lease ran out, to
	// lease until its Until (D412); domain.ErrNotFound when there is none.
	LeaseTask(ctx context.Context, owner domain.UserID, lease TaskLease, now time.Time) (WaitRecord, error)
	// ReleaseTask takes a task from the agent that has it, for any to take (D413);
	// domain.ErrNotFound for no task of the owner's.
	ReleaseTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID) error
	// RenewLeases holds the tasks holder has until until.
	RenewLeases(ctx context.Context, owner domain.UserID, holder string, until time.Time) error
	// TakeTask is Take for a task holder has: domain.ErrNotFound for one another holds,
	// none does, or that is no task.
	TakeTask(ctx context.Context, owner domain.UserID, id domain.ExecutionID, holder string) (WaitRecord, error)
}

// TaskAgents says whether any of an owner's agents takes tasks now (D386): one waiting
// for one, or that waited a moment ago.
type TaskAgents interface {
	Around(owner domain.UserID) bool
}
