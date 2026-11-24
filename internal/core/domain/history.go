package domain

import "time"

// The workflow's history (D433): who switched it, and why.

type SwitchID string

// SwitchKind is what a switch did to a workflow.
type SwitchKind string

const (
	SwitchOn      SwitchKind = "on"       // its triggers switched on
	SwitchOff     SwitchKind = "off"      // and off
	SwitchHold    SwitchKind = "hold"     // held off by an admin, switched off if it was on (D435)
	SwitchRelease SwitchKind = "release"  // released by an admin, to be switched on again
	SwitchPaper   SwitchKind = "paper"    // back to trading on paper
	SwitchLive    SwitchKind = "live"     // to trading live, from Wallet
	SwitchWallet  SwitchKind = "wallet"   // trading live from another wallet, Wallet
	SwitchReset   SwitchKind = "reset"    // its paper account started over
	SwitchKillOn  SwitchKind = "kill_on"  // its owner's kill switch on, as it traded live
	SwitchKillOff SwitchKind = "kill_off" // and off
)

// SwitchKinds are every kind.
var SwitchKinds = []SwitchKind{SwitchOn, SwitchOff, SwitchHold, SwitchRelease, SwitchPaper, SwitchLive, SwitchWallet, SwitchReset, SwitchKillOn, SwitchKillOff}

// SwitchGroup is kinds together, as the history is narrowed by.
type SwitchGroup string

const (
	GroupOnOff      SwitchGroup = "on_off"
	GroupTrading    SwitchGroup = "trading"
	GroupReset      SwitchGroup = "reset"
	GroupKillSwitch SwitchGroup = "kill_switch"
)

// SwitchGroups are every group, each with its kinds.
var SwitchGroups = map[SwitchGroup][]SwitchKind{
	GroupOnOff:      {SwitchOn, SwitchOff, SwitchHold, SwitchRelease},
	GroupTrading:    {SwitchPaper, SwitchLive, SwitchWallet},
	GroupReset:      {SwitchReset},
	GroupKillSwitch: {SwitchKillOn, SwitchKillOff},
}

// SwitchReasonMost is the most runes a reason keeps.
const SwitchReasonMost = 500

// Switch is one switch of a workflow.
type Switch struct {
	ID         SwitchID
	WorkflowID WorkflowID
	Kind       SwitchKind
	// By is the agent or token that switched it, its How unused; zero, the user, unless
	// Polythought did, or an admin (D435).
	By          Actor
	Polythought bool
	Admin       bool
	// Reason is why: the agent's or token's own words, as it gave them, or Polythought's;
	// empty when none was given.
	Reason string
	// Wallet is the wallet it trades live from, for live and wallet: its address kept, as
	// the wallet may go.
	WalletID      WalletID
	WalletAddress WalletAddress
	At            time.Time
}
