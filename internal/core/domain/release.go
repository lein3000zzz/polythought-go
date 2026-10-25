package domain

import (
	"errors"
	"time"
)

// Release is one of Polythought's on GitHub (D424): Version as it was tagged, without the
// v, such as 0.1.2 or 0.2.0-beta.1; URL its page there, with what changed.
type Release struct {
	Version     string
	Prerelease  bool
	URL         string
	PublishedAt time.Time
}

// The updater's (D425): none set up, or one updating the server already.
var (
	ErrNoUpdater = errors.New("no updater is set up: see Updates in the install docs")
	ErrUpdating  = errors.New("an update is under way already")
)

// UpdaterState is where Polythought's updater is with updating the server (D425).
type UpdaterState string

const (
	UpdaterIdle     UpdaterState = "idle" // asked nothing since it started
	UpdaterUpdating UpdaterState = "updating"
	UpdaterDone     UpdaterState = "done"
	UpdaterFailed   UpdaterState = "failed"
)

// UpdaterStatus is the updater's latest update: Version the release it updates or
// updated to, Error why it failed, Log the end of what it did.
type UpdaterStatus struct {
	State      UpdaterState
	Version    string
	Error      string
	Log        string
	StartedAt  time.Time
	FinishedAt time.Time
}
