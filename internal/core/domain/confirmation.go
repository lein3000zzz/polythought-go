package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// A live action a token or an agent asks for waits for its user to confirm it, where the
// user's settings say so (D385, D406): going live, switching on or running a workflow that
// trades live, changing one that is on, turning the kill switch off. The user confirms it
// in the app; it is then carried out as the token or the agent asked.

type ConfirmationID string

// ConfirmKind is what a confirmation is for.
type ConfirmKind string

const (
	ConfirmGoLive   ConfirmKind = "go_live"   // trading a workflow live, or from another wallet
	ConfirmSwitchOn ConfirmKind = "switch_on" // switching on a workflow that trades live
	ConfirmRun      ConfirmKind = "run"       // running one by hand
	ConfirmChange   ConfirmKind = "change"    // a new graph for one that is on
	ConfirmKillOff  ConfirmKind = "kill_off"  // turning the kill switch off
)

type ConfirmStatus string

const (
	ConfirmPending   ConfirmStatus = "pending"   // waiting for the user
	ConfirmConfirmed ConfirmStatus = "confirmed" // confirmed, being carried out
	ConfirmDone      ConfirmStatus = "done"
	ConfirmFailed    ConfirmStatus = "failed" // confirmed, but carrying it out failed: Result says why
	ConfirmRefused   ConfirmStatus = "refused"
	ConfirmExpired   ConfirmStatus = "expired" // not confirmed in time
)

// Confirmation is a live action waiting for its user, or how it ended.
type Confirmation struct {
	ID      ConfirmationID
	OwnerID UserID
	Kind    ConfirmKind
	// WorkflowID is the workflow it is about; empty for the kill switch.
	WorkflowID WorkflowID

	// Who asked: an agent's grant or an API token, by its name then, with its scopes then.
	GrantID GrantID
	TokenID APITokenID
	Agent   string // the agent's name, or the token's
	Scopes  []Scope

	What    string          // what it does, said for the user
	Payload json.RawMessage // what carrying it out needs, by kind

	Status ConfirmStatus
	Result string // what came of it: an execution's ID, or why it failed

	// Message is the Telegram message that asked the user, to say there how it was
	// decided; 0 when none went.
	Message int

	CreatedAt time.Time
	ExpiresAt time.Time
	DecidedAt time.Time // zero while pending
}

// StatusAt is its status at now: a pending one past its time has expired.
func (c Confirmation) StatusAt(now time.Time) ConfirmStatus {
	if c.Status == ConfirmPending && !now.Before(c.ExpiresAt) {
		return ConfirmExpired
	}

	return c.Status
}

// Asker is who asked, for the user: the agent by its name, or the token by its.
func (c Confirmation) Asker() string {
	switch {
	case c.GrantID != "" && c.Agent != "":
		return c.Agent
	case c.GrantID == "" && c.Agent != "":
		return "the API token “" + c.Agent + "”"
	}

	return "an API token"
}

// Unconfirmed says which live actions of the user's tokens and agents go ahead without the
// user confirming them (D406): none to start with, so its zero value asks for every one.
type Unconfirmed struct {
	GoLive  bool // trading a workflow live, or from another wallet
	Run     bool // switching on, or running, a workflow that trades live
	Change  bool // a new graph for a workflow that trades live and is on
	KillOff bool // turning the kill switch off
}

// Waits says whether an action of kind waits for the user.
func (u Unconfirmed) Waits(kind ConfirmKind) bool {
	switch kind {
	case ConfirmGoLive:
		return !u.GoLive
	case ConfirmSwitchOn, ConfirmRun:
		return !u.Run
	case ConfirmChange:
		return !u.Change
	case ConfirmKillOff:
		return !u.KillOff
	}

	return true
}

// ErrAwaitsConfirmation means the action waits for its user to confirm it; an
// *AwaitingError says which confirmation.
var ErrAwaitsConfirmation = errors.New("it waits for the user to confirm it")

type AwaitingError struct {
	Confirmation Confirmation
}

func (e *AwaitingError) Error() string {
	return fmt.Sprintf("%s, as it trades live or lets live trading start again: the user confirms it in Polythought, "+
		"until %s; confirmation %s", ErrAwaitsConfirmation, e.Confirmation.ExpiresAt.UTC().Format(time.RFC3339), e.Confirmation.ID)
}

func (e *AwaitingError) Unwrap() error { return ErrAwaitsConfirmation }
