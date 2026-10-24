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

	// Changing or deleting a custom node that a workflow trading live and on uses (A5): its
	// next run runs the new code. The Change switch says whether it waits.
	ConfirmNodeChange ConfirmKind = "custom_node"

	// An admin's work on another user (D410).
	ConfirmRole    ConfirmKind = "admin_role"    // making a user an admin, or one no longer
	ConfirmDisable ConfirmKind = "admin_disable" // disabling a user, or enabling one again
	ConfirmCaps    ConfirmKind = "admin_caps"    // raising, or removing, a user's own caps
	ConfirmCode    ConfirmKind = "admin_code"    // letting a user write code

	// An admin's work on the instance (D411).
	ConfirmConfig        ConfirmKind = "admin_config"         // changing the Config settings
	ConfirmRestart       ConfirmKind = "admin_restart"        // restarting the server
	ConfirmPluginApprove ConfirmKind = "admin_plugin_approve" // letting a plugin's version run
	ConfirmPluginChange  ConfirmKind = "admin_plugin"         // enabling, its users, settings, caps, removing
)

// Ask is a live action, or an admin's, to hold for its user: what it is, what it does said
// for them, and what carrying it out needs. About is whom it is about, such as a user's
// email, shown to the user and never to the agent (D410).
type Ask struct {
	Kind       ConfirmKind
	WorkflowID WorkflowID
	What       string
	About      string
	// Detail is what it changes, line by line, for the user to read before they confirm
	// (A3): a workflow's new graph against the one it has.
	Detail  string
	Payload any
}

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
	About   string          // whom it is about, for the user only: never the agent's to see
	Detail  string          // what it changes, line by line, for the user (A3)
	Payload json.RawMessage // what carrying it out needs, by kind

	Status ConfirmStatus
	Result string // what came of it: an execution's ID, or why it failed

	// Message is the Telegram message that asked the user, to say there how it was
	// decided; 0 when none went.
	Message int

	CreatedAt time.Time
	ExpiresAt time.Time
	DecidedAt time.Time // zero while pending
	// DecidedVia is where the user decided it (D409); empty while pending.
	DecidedVia ConfirmVia
}

// ConfirmVia is where a confirmation was decided: at a browser, on Telegram, or in the
// agent's own window, where the user lets it ask (D409).
type ConfirmVia string

const (
	ConfirmViaApp      ConfirmVia = "app"
	ConfirmViaTelegram ConfirmVia = "telegram"
	ConfirmViaAgent    ConfirmVia = "agent"
)

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
	Change  bool // a new graph for a workflow that trades live and is on, or a custom node it uses
	KillOff bool // turning the kill switch off

	// An admin's work on other users (D410).
	Role    bool
	Disable bool
	Caps    bool
	Code    bool

	// An admin's work on the instance (D411).
	Config        bool
	Restart       bool
	PluginApprove bool
	PluginChange  bool
}

// Waits says whether an action of kind waits for the user.
func (u Unconfirmed) Waits(kind ConfirmKind) bool {
	switch kind {
	case ConfirmGoLive:
		return !u.GoLive
	case ConfirmSwitchOn, ConfirmRun:
		return !u.Run
	case ConfirmChange, ConfirmNodeChange:
		return !u.Change
	case ConfirmKillOff:
		return !u.KillOff
	case ConfirmRole:
		return !u.Role
	case ConfirmDisable:
		return !u.Disable
	case ConfirmCaps:
		return !u.Caps
	case ConfirmCode:
		return !u.Code
	case ConfirmConfig:
		return !u.Config
	case ConfirmRestart:
		return !u.Restart
	case ConfirmPluginApprove:
		return !u.PluginApprove
	case ConfirmPluginChange:
		return !u.PluginChange
	}

	return true
}

// ErrConfirmRefused means the user refused a live action that waited for them (D409).
var ErrConfirmRefused = errors.New("the user refused it")

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
