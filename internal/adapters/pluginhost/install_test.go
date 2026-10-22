package pluginhost_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jedisct1/go-minisign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/plugindir"
	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
)

// TestInstallThenLoad takes the test plugin the way an admin's does: a signed archive of
// its program and manifest.json, installed in the plugins directory, opened again as the
// server starts, and loaded.
func TestInstallThenLoad(t *testing.T) {
	program, err := os.ReadFile(echo)
	require.NoError(t, err)

	files := map[string][]byte{
		"manifest.json": echoFile(t, nil).Manifest,
		"bin/" + plugindir.Platform() + "/plugin": program,
	}

	var sums bytes.Buffer
	for _, name := range []string{"bin/" + plugindir.Platform() + "/plugin", "manifest.json"} {
		h := sha256.Sum256(files[name])
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), name)
	}

	files["checksums.txt"] = sums.Bytes()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	var sk minisign.PrivateKey
	sk.SignatureAlgorithm = [2]byte{'E', 'd'}
	_, _ = rand.Read(sk.KeyId[:])
	copy(sk.SecretKey[:], priv)

	sig, err := sk.Sign(sums.Bytes(), minisign.SignOptions{Hashed: true})
	require.NoError(t, err)

	files["checksums.txt.minisig"] = sig.Encode()
	key := "Echo's author " + base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), sk.KeyId[:]...), pub...))

	var archive bytes.Buffer

	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)

	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write(body)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())

	dir, err := plugindir.Open(filepath.Join(t.TempDir(), "plugins"))
	require.NoError(t, err)

	pf := plugindir.NewFiles(dir, func() []string { return []string{key} })

	installed, err := pf.Install(&archive, 256<<20)
	require.NoError(t, err)
	assert.Equal(t, "Echo's author", installed.Signer)
	assert.Equal(t, []string{"fetch:127.0.0.1", "wallets", "telegram", "orders"}, installed.Permissions)

	// As the server starts: the files checked again, then the program run as approved.
	f, err := pf.Open("echo", "1.2.0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	r := registry(t)
	rt := pluginhost.NewRuntime(ctx, pluginhost.Deps{}, r)
	t.Cleanup(rt.Close)

	require.NoError(t, rt.Load(ctx, f))

	out, err := run(t, r, "plugin.echo.shout", map[string]any{"field": "q"}, items(map[string]any{"q": "installed"}))
	require.NoError(t, err)
	assert.Equal(t, "INSTALLED", out["main"][0].JSON["shouted"])
}
