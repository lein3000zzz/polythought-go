package ports

import (
	"context"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

// SealedCredential is a credential as stored: its secret values sealed together.
type SealedCredential struct {
	Credential domain.Credential
	Sealed     []byte // the secret values as a JSON object, sealed; nil when none are set
}

// CredentialRepository scopes every lookup to the owning user, so one user can never
// reach another's credential: it is simply not found.
type CredentialRepository interface {
	// Create returns domain.ErrCredentialExists when the owner has one by that name.
	Create(ctx context.Context, c SealedCredential) error
	// Update replaces the name, values and update time. Like Create, it returns
	// domain.ErrCredentialExists on a name already taken.
	Update(ctx context.Context, c SealedCredential) error
	Get(ctx context.Context, owner domain.UserID, id domain.CredentialID) (SealedCredential, error)
	// List returns the owner's credentials by name.
	List(ctx context.Context, owner domain.UserID) ([]domain.Credential, error)
	CountForOwner(ctx context.Context, owner domain.UserID) (int, error)
	Delete(ctx context.Context, owner domain.UserID, id domain.CredentialID) error
	// RecordTest saves when the credential was tested and why it failed, empty if it
	// passed.
	RecordTest(ctx context.Context, owner domain.UserID, id domain.CredentialID, at time.Time, testErr string) error
}

// CredentialReader gives a node the values of one of the owner's credentials, secret
// ones included. It only returns the owner's own credentials, of the type asked for.
type CredentialReader interface {
	Values(ctx context.Context, owner domain.UserID, id domain.CredentialID, credType string) (map[string]string, error)
}
