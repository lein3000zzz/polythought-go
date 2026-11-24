package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// ModelRepository keeps users' models and their worker keys.
type ModelRepository interface {
	// Create keeps a new model; domain.ErrModelExists when the owner has one by its name.
	Create(ctx context.Context, m domain.Model) error
	// List is the owner's models, by name; Get one by name, ByID one by its ID,
	// domain.ErrNotFound for none.
	List(ctx context.Context, owner domain.UserID) ([]domain.Model, error)
	Get(ctx context.Context, owner domain.UserID, name string) (domain.Model, error)
	ByID(ctx context.Context, id domain.ModelID) (domain.Model, error)
	// SetURL changes where a URL model is called, and with what credential.
	SetURL(ctx context.Context, id domain.ModelID, url string, credential domain.CredentialID, at time.Time) error
	Delete(ctx context.Context, id domain.ModelID) error

	// Answered keeps when the model last answered; Failed, its last error and when.
	Answered(ctx context.Context, id domain.ModelID, at time.Time) error
	Failed(ctx context.Context, id domain.ModelID, reason string, at time.Time) error

	// CreateKey keeps a worker key by its secret's hash; Keys are the model's, the newest
	// first; KeyByHash is the key with the hash and its model, domain.ErrNotFound for none;
	// KeyUsed keeps when it was last used; DeleteKey revokes it.
	CreateKey(ctx context.Context, k domain.WorkerKey, hash []byte) error
	Keys(ctx context.Context, model domain.ModelID) ([]domain.WorkerKey, error)
	KeyByHash(ctx context.Context, hash []byte) (domain.WorkerKey, domain.Model, error)
	KeyUsed(ctx context.Context, id domain.WorkerKeyID, at time.Time) error
	DeleteKey(ctx context.Context, model domain.ModelID, id domain.WorkerKeyID) error
}

// ModelAnswer is a model's answer to a batch of items, one per item, with the version that
// answered and the worker, for a worker model. Refused are the answers, by index, that do
// not keep to the schema asked for.
type ModelAnswer struct {
	Answers []json.RawMessage
	Version string
	Worker  string
	Refused map[int]error
}

// ModelAsker asks the owner's model about items, each a JSON object, waiting up to wait for
// its answers: what the Ask a model node needs. A model gone, or kept from its owner, is an
// error before anything is asked.
type ModelAsker interface {
	Ask(ctx context.Context, owner domain.UserID, name string, items []json.RawMessage, schema json.RawMessage, wait time.Duration) (ModelAnswer, error)
}
