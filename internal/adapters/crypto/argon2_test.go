package crypto_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
)

// fastParams keeps tests quick; production uses DefaultArgon2Params.
var fastParams = crypto.Argon2Params{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestArgon2idVerify(t *testing.T) {
	hasher := crypto.NewArgon2id(fastParams)

	encoded, err := hasher.Hash("correct horse")
	require.NoError(t, err)

	stronger := fastParams
	stronger.Iterations = 2

	tests := []struct {
		name       string
		hasher     *crypto.Argon2id
		password   string
		encoded    string
		wantOK     bool
		wantRehash bool
		wantErrIs  error
	}{
		{name: "matching password", hasher: hasher, password: "correct horse", encoded: encoded, wantOK: true},
		{name: "wrong password", hasher: hasher, password: "correct horse!", encoded: encoded},
		{name: "empty password", hasher: hasher, password: "", encoded: encoded},
		{name: "outdated parameters ask for a rehash", hasher: crypto.NewArgon2id(stronger), password: "correct horse", encoded: encoded, wantOK: true, wantRehash: true},
		{name: "not a phc string", hasher: hasher, encoded: "plaintext", wantErrIs: crypto.ErrMalformedHash},
		{name: "other algorithm", hasher: hasher, encoded: "$bcrypt$v=19$m=64,t=1,p=1$c2FsdA$aGFzaA", wantErrIs: crypto.ErrMalformedHash},
		{name: "unsupported version", hasher: hasher, encoded: "$argon2id$v=16$m=64,t=1,p=1$c2FsdA$aGFzaA", wantErrIs: crypto.ErrMalformedHash},
		{name: "zero parameter", hasher: hasher, encoded: "$argon2id$v=19$m=0,t=1,p=1$c2FsdA$aGFzaA", wantErrIs: crypto.ErrMalformedHash},
		{name: "bad salt encoding", hasher: hasher, encoded: "$argon2id$v=19$m=64,t=1,p=1$!!!$aGFzaA", wantErrIs: crypto.ErrMalformedHash},
		{name: "empty hash", hasher: hasher, encoded: "$argon2id$v=19$m=64,t=1,p=1$c2FsdA$", wantErrIs: crypto.ErrMalformedHash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, rehash, err := tt.hasher.Verify(tt.password, tt.encoded)

			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantRehash, rehash)
		})
	}
}

func TestArgon2idHash(t *testing.T) {
	hasher := crypto.NewArgon2id(fastParams)

	first, err := hasher.Hash("same password")
	require.NoError(t, err)

	second, err := hasher.Hash("same password")
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "every hash gets its own salt")
	assert.Regexp(t, `^\$argon2id\$v=19\$m=64,t=1,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`, first)

	defaults, err := crypto.NewArgon2id(crypto.DefaultArgon2Params).Hash("x")
	require.NoError(t, err)
	assert.Contains(t, defaults, "$m=19456,t=2,p=1$")
}
