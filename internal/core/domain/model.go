package domain

import (
	"errors"
	"time"
)

type ModelID string

// ModelKind is how a model is reached: by its workers, which take its requests, or at a
// URL, called as webhooks are.
type ModelKind string

const (
	ModelWorker ModelKind = "worker"
	ModelURL    ModelKind = "url"
)

func (k ModelKind) Valid() bool { return k == ModelWorker || k == ModelURL }

// Model is a user's named model, its name fixed: workflows ask it, through its workers or
// its URL, and it answers each item.
type Model struct {
	ID      ModelID
	OwnerID UserID
	Name    string
	Kind    ModelKind

	// URL and Credential are a URL model's: where it is called, and an http.header
	// credential sent with each call, none for none.
	URL        string
	Credential CredentialID

	// What it last did: when it last answered, and its last error and when.
	LastAnswerAt time.Time
	LastError    string
	LastErrorAt  time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

type WorkerKeyID string

// WorkerKey lets a worker serve one model, and that model alone. Its secret is shown once,
// as it is made, and kept hashed; Prefix is enough of it to tell keys apart.
type WorkerKey struct {
	ID         WorkerKeyID
	ModelID    ModelID
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt time.Time
}

var (
	// ErrModelExists means the owner has a model by that name already.
	ErrModelExists = errors.New("you have a model by that name already")
	// ErrModelLimit means the owner has as many models, or the model as many workers or
	// keys, as they may.
	ErrModelLimit = errors.New("no more")
	// ErrModelOffline means no worker serving the model is connected.
	ErrModelOffline = errors.New("no worker serving the model is connected")
	// ErrModelTimeout means the model did not answer in time.
	ErrModelTimeout = errors.New("the model did not answer in time")
)
