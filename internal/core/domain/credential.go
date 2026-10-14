package domain

import (
	"errors"
	"time"
)

type CredentialID string

var ErrCredentialExists = errors.New("you already have a credential with that name")

// Credential is a set of values a user keeps in their vault for nodes to use on their
// behalf, such as an API key. Secret values are stored encrypted and only ever go to the
// node that uses them; Credential carries just the others.
type Credential struct {
	ID      CredentialID
	OwnerID UserID
	Type    string // a registered credential type, e.g. "http.header"
	Name    string

	Fields  map[string]string // the values that are not secret
	Secrets []string          // which secret values are set, by field name

	CreatedAt time.Time
	UpdatedAt time.Time

	TestedAt  time.Time // zero when never tested
	TestError string    // why the last test failed; empty when it passed
}
