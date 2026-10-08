package ports

import "context"

// PasswordHasher turns passwords into self-describing hashes for storage.
type PasswordHasher interface {
	Hash(password string) (string, error)

	// Verify reports whether password matches encoded. needsRehash is true when encoded
	// was made with weaker parameters than the current ones and should be replaced.
	Verify(password, encoded string) (ok, needsRehash bool, err error)
}

// SecretBox encrypts secrets at rest. aad (associated data) binds a ciphertext to where
// it belongs, such as a row ID and field name, so it cannot be copied to another row.
type SecretBox interface {
	Seal(ctx context.Context, plaintext, aad []byte) ([]byte, error)
	Open(ctx context.Context, sealed, aad []byte) ([]byte, error)
}
