package crypto_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/crypto"
)

func newKey(t *testing.T) string {
	t.Helper()

	k := make([]byte, 32)
	_, err := rand.Read(k)
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(k)
}

func envelope(t *testing.T, spec string) *crypto.Envelope {
	t.Helper()

	keys, err := crypto.ParseStaticKeys(spec)
	require.NoError(t, err)

	return crypto.NewEnvelope(keys)
}

func TestEnvelopeOpen(t *testing.T) {
	ctx := context.Background()
	oldKey, newKeyB64, otherKey := newKey(t), newKey(t), newKey(t)

	sealedWithOld, err := envelope(t, "old:"+oldKey).Seal(ctx, []byte("api-secret"), []byte("wallet-1/secret"))
	require.NoError(t, err)

	flip := func(b []byte, i int) []byte {
		c := bytes.Clone(b)
		c[i] ^= 0xff

		return c
	}

	tests := []struct {
		name      string
		box       *crypto.Envelope
		sealed    []byte
		aad       string
		want      string
		wantErrIs error
	}{
		{name: "same key", box: envelope(t, "old:"+oldKey), sealed: sealedWithOld, aad: "wallet-1/secret", want: "api-secret"},
		{name: "after rotation the old key still opens", box: envelope(t, "new:"+newKeyB64+",old:"+oldKey), sealed: sealedWithOld, aad: "wallet-1/secret", want: "api-secret"},
		{name: "retired key is gone", box: envelope(t, "new:"+newKeyB64), sealed: sealedWithOld, aad: "wallet-1/secret", wantErrIs: crypto.ErrUnknownKey},
		{name: "same id, different key material", box: envelope(t, "old:"+otherKey), sealed: sealedWithOld, aad: "wallet-1/secret", wantErrIs: crypto.ErrDecrypt},
		{name: "copied to another row", box: envelope(t, "old:"+oldKey), sealed: sealedWithOld, aad: "wallet-2/secret", wantErrIs: crypto.ErrDecrypt},
		{name: "tampered ciphertext", box: envelope(t, "old:"+oldKey), sealed: flip(sealedWithOld, len(sealedWithOld)-1), aad: "wallet-1/secret", wantErrIs: crypto.ErrDecrypt},
		{name: "unknown format version", box: envelope(t, "old:"+oldKey), sealed: flip(sealedWithOld, 0), aad: "wallet-1/secret", wantErrIs: crypto.ErrMalformedSecret},
		{name: "truncated", box: envelope(t, "old:"+oldKey), sealed: sealedWithOld[:10], aad: "wallet-1/secret", wantErrIs: crypto.ErrMalformedSecret},
		{name: "empty", box: envelope(t, "old:"+oldKey), sealed: nil, aad: "wallet-1/secret", wantErrIs: crypto.ErrMalformedSecret},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.box.Open(ctx, tt.sealed, []byte(tt.aad))

			if tt.wantErrIs != nil {
				require.ErrorIs(t, err, tt.wantErrIs)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestEnvelopeSeal(t *testing.T) {
	ctx := context.Background()
	box := envelope(t, "k1:"+newKey(t))

	first, err := box.Seal(ctx, []byte("same"), nil)
	require.NoError(t, err)

	second, err := box.Seal(ctx, []byte("same"), nil)
	require.NoError(t, err)

	assert.NotEqual(t, first, second, "fresh data key and nonce per seal")
	assert.NotContains(t, string(first), "same", "plaintext does not leak")

	rotated := envelope(t, "k2:"+newKey(t)+",k1:"+base64.StdEncoding.EncodeToString(make([]byte, 32)))
	sealed, err := rotated.Seal(ctx, []byte("x"), nil)
	require.NoError(t, err)
	assert.Equal(t, "k2", string(sealed[2:2+int(sealed[1])]), "new secrets use the first, current key")
}

func TestParseStaticKeys(t *testing.T) {
	key := newKey(t)

	tests := []struct {
		name    string
		spec    string
		wantErr string
	}{
		{name: "one key", spec: "k1:" + key},
		{name: "rotation list with spaces", spec: "k2:" + key + ", k1:" + newKey(t)},
		{name: "missing id", spec: ":" + key, wantErr: "want id:base64key"},
		{name: "no separator", spec: key, wantErr: "want id:base64key"},
		{name: "bad id characters", spec: "k 1:" + key, wantErr: "want id:base64key"},
		{name: "not base64", spec: "k1:???", wantErr: "want 32 base64-encoded bytes"},
		{name: "wrong length", spec: "k1:" + base64.StdEncoding.EncodeToString([]byte("short")), wantErr: "want 32 base64-encoded bytes"},
		{name: "duplicate id", spec: "k1:" + key + ",k1:" + key, wantErr: "duplicate key id"},
		{name: "empty", spec: "", wantErr: "want id:base64key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := crypto.ParseStaticKeys(tt.spec)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}

			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
