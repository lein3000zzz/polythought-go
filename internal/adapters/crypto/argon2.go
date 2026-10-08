// Package crypto implements the core's password hashing and secret encryption ports.
package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.PasswordHasher = (*Argon2id)(nil)

var ErrMalformedHash = errors.New("malformed password hash")

type Argon2Params struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultArgon2Params follows the OWASP minimum for argon2id (19 MiB, 2 passes, 1 lane).
// It keeps logins cheap enough for a public instance; raise Memory if you have headroom.
var DefaultArgon2Params = Argon2Params{
	Memory:      19 * 1024,
	Iterations:  2,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

// Argon2id hashes passwords into the PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
type Argon2id struct {
	params Argon2Params
}

func NewArgon2id(params Argon2Params) *Argon2id {
	return &Argon2id{params: params}
}

func (a *Argon2id) Hash(password string) (string, error) {
	salt := make([]byte, a.params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("argon2id: salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, a.params.Iterations, a.params.Memory, a.params.Parallelism, a.params.KeyLength)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, a.params.Memory, a.params.Iterations, a.params.Parallelism,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

func (a *Argon2id) Verify(password, encoded string) (ok, needsRehash bool, err error) {
	params, salt, key, err := decodeArgon2(encoded)
	if err != nil {
		return false, false, err
	}

	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, params.KeyLength)
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}

	return true, params != a.params, nil
}

var b64 = base64.RawStdEncoding

func decodeArgon2(encoded string) (Argon2Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: unsupported version %q", ErrMalformedHash, parts[2])
	}

	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: parameters: %w", ErrMalformedHash, err)
	}

	if p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: zero parameter", ErrMalformedHash)
	}

	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: salt: %w", ErrMalformedHash, err)
	}

	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: hash", ErrMalformedHash)
	}

	p.SaltLength = uint32(len(salt))
	p.KeyLength = uint32(len(key))

	return p, salt, key, nil
}
