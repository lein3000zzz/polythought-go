// Package updater is Polythought's updater (D425): a small server beside the stack in
// Docker Compose, holding the Docker socket, that updates the server's containers to a
// release when the server asks with its token. The server's client for it is here too.
//
// An update runs in a container of its own, the updater's image, that sees the stack's
// Compose files and env file at the paths the stack was started with. It pulls the
// release's images, keeps the version in the env file as POLYTHOUGHT_VERSION, so a later
// docker compose up stays on it, and recreates the services, nothing else.
package updater

import "time"

// The updater's paths: health is open, the others take the token as a Bearer.
const (
	PathHealth = "/healthz"
	PathStatus = "/v1/status"
	PathUpdate = "/v1/update"
)

// MinToken is the shortest token the updater takes.
const MinToken = 32

// Where an update is.
const (
	StateIdle     = "idle" // asked nothing since it started
	StateUpdating = "updating"
	StateDone     = "done"
	StateFailed   = "failed"
)

// UpdateRequest asks for an update to Version, such as 0.1.2.
type UpdateRequest struct {
	Version string `json:"version"`
}

// Status is the latest update: Version the release it is to, Error why it failed, Log
// the end of what it did.
type Status struct {
	State      string    `json:"state"`
	Version    string    `json:"version,omitempty"`
	Error      string    `json:"error,omitempty"`
	Log        string    `json:"log,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
}

// errorBody is how the updater says what is wrong.
type errorBody struct {
	Error string `json:"error"`
}
