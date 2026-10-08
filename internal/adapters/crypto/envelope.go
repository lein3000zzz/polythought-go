package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.SecretBox = (*Envelope)(nil)

var (
	ErrUnknownKey      = errors.New("unknown master key")
	ErrMalformedSecret = errors.New("malformed sealed secret")
	ErrDecrypt         = errors.New("secret failed to decrypt")
)

// KeyProvider wraps data keys with a master key it never reveals. Implementations can
// keep master keys in memory, Vault Transit or a cloud KMS.
type KeyProvider interface {
	Wrap(ctx context.Context, dataKey []byte) (keyID string, wrapped []byte, err error)
	Unwrap(ctx context.Context, keyID string, wrapped []byte) ([]byte, error)
}

const (
	envelopeVersion = 1
	dataKeySize     = 32
)

// Envelope encrypts every secret with its own AES-256-GCM data key and stores that key
// wrapped by the KeyProvider. The sealed format is:
//
//	version(1) | keyIDLen(1) | keyID | wrappedLen(2) | wrapped | nonce(12) | ciphertext
//
// The key ID travels with the secret, so master keys can rotate: old secrets open with
// the key that sealed them, new ones use the provider's current key.
type Envelope struct {
	keys KeyProvider
}

func NewEnvelope(keys KeyProvider) *Envelope {
	return &Envelope{keys: keys}
}

func (e *Envelope) Seal(ctx context.Context, plaintext, aad []byte) ([]byte, error) {
	dataKey := make([]byte, dataKeySize)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, fmt.Errorf("envelope: data key: %w", err)
	}
	defer clear(dataKey)

	keyID, wrapped, err := e.keys.Wrap(ctx, dataKey)
	if err != nil {
		return nil, fmt.Errorf("envelope: wrap: %w", err)
	}

	if len(keyID) > 255 || len(wrapped) > 65535 {
		return nil, errors.New("envelope: key id or wrapped key too long")
	}

	nonce, ciphertext, err := gcmSeal(dataKey, plaintext, aad)
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 4+len(keyID)+len(wrapped)+len(nonce)+len(ciphertext))
	out = append(out, envelopeVersion, byte(len(keyID)))
	out = append(out, keyID...)
	out = binary.BigEndian.AppendUint16(out, uint16(len(wrapped)))
	out = append(out, wrapped...)
	out = append(out, nonce...)
	out = append(out, ciphertext...)

	return out, nil
}

func (e *Envelope) Open(ctx context.Context, sealed, aad []byte) ([]byte, error) {
	keyID, wrapped, nonce, ciphertext, err := parseSealed(sealed)
	if err != nil {
		return nil, err
	}

	dataKey, err := e.keys.Unwrap(ctx, keyID, wrapped)
	if err != nil {
		return nil, fmt.Errorf("envelope: unwrap: %w", err)
	}
	defer clear(dataKey)

	return gcmOpen(dataKey, nonce, ciphertext, aad)
}

func parseSealed(b []byte) (keyID string, wrapped, nonce, ciphertext []byte, err error) {
	read := func(n int) ([]byte, bool) {
		if len(b) < n {
			return nil, false
		}

		chunk := b[:n]
		b = b[n:]

		return chunk, true
	}

	head, ok := read(2)
	if !ok || head[0] != envelopeVersion {
		return "", nil, nil, nil, ErrMalformedSecret
	}

	id, ok := read(int(head[1]))
	if !ok {
		return "", nil, nil, nil, ErrMalformedSecret
	}

	size, ok := read(2)
	if !ok {
		return "", nil, nil, nil, ErrMalformedSecret
	}

	wrapped, ok = read(int(binary.BigEndian.Uint16(size)))
	if !ok {
		return "", nil, nil, nil, ErrMalformedSecret
	}

	nonce, ok = read(12)
	if !ok || len(b) == 0 {
		return "", nil, nil, nil, ErrMalformedSecret
	}

	return string(id), wrapped, nonce, b, nil
}

func gcmSeal(key, plaintext, aad []byte) (nonce, ciphertext []byte, err error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, nil, err
	}

	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("envelope: nonce: %w", err)
	}

	return nonce, aead.Seal(nil, nonce, plaintext, aad), nil
}

func gcmOpen(key, nonce, ciphertext, aad []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, ErrDecrypt
	}

	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("envelope: cipher: %w", err)
	}

	return cipher.NewGCM(block)
}

// StaticKeys holds master keys in memory, loaded from configuration. The first key
// wraps new data keys; the others only unwrap, which is how rotation works.
type StaticKeys struct {
	current string
	keys    map[string][]byte
}

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ParseStaticKeys reads "id:base64key[,id:base64key...]". Keys must be 32 bytes
// (generate one with `openssl rand -base64 32`). The first entry is the current key.
func ParseStaticKeys(spec string) (*StaticKeys, error) {
	s := &StaticKeys{keys: make(map[string][]byte)}

	for i, entry := range strings.Split(spec, ",") {
		id, encoded, found := strings.Cut(strings.TrimSpace(entry), ":")
		if !found || !keyIDPattern.MatchString(id) {
			return nil, fmt.Errorf("secret keys: entry %d: want id:base64key with a [A-Za-z0-9_.-] id", i)
		}

		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != dataKeySize {
			return nil, fmt.Errorf("secret keys: key %q: want %d base64-encoded bytes", id, dataKeySize)
		}

		if _, dup := s.keys[id]; dup {
			return nil, fmt.Errorf("secret keys: duplicate key id %q", id)
		}

		if i == 0 {
			s.current = id
		}

		s.keys[id] = key
	}

	return s, nil
}

func (s *StaticKeys) Wrap(_ context.Context, dataKey []byte) (string, []byte, error) {
	nonce, ciphertext, err := gcmSeal(s.keys[s.current], dataKey, []byte(s.current))
	if err != nil {
		return "", nil, err
	}

	return s.current, append(nonce, ciphertext...), nil
}

func (s *StaticKeys) Unwrap(_ context.Context, keyID string, wrapped []byte) ([]byte, error) {
	master, ok := s.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKey, keyID)
	}

	if len(wrapped) < 12 {
		return nil, ErrMalformedSecret
	}

	return gcmOpen(master, wrapped[:12], wrapped[12:], []byte(keyID))
}
