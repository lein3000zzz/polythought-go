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
	assert.Equal(t, filepath.Join(dir.Root(), "echo", "1.2.0"), f.Dir)
	assert.Equal(t, filepath.Join(dir.Root(), ".data", "echo"), f.Data, "its plugin's, beside its versions")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	r := registry(t)
	rt := pluginhost.NewRuntime(ctx, sandboxed(pluginhost.Deps{}), r)
	t.Cleanup(rt.Close)

	require.NoError(t, rt.Load(ctx, f))

	out, err := run(t, r, "plugin.echo.shout", map[string]any{"field": "q"}, items(map[string]any{"q": "installed"}))
	require.NoError(t, err)
	assert.Equal(t, "INSTALLED", out["main"][0].JSON["shouted"])

	// What it keeps is in its lasting directory, which is no version of a plugin.
	out, err = run(t, r, "plugin.echo.disk", map[string]any{"outside": key, "dir": f.Dir}, items(map[string]any{}))
	require.NoError(t, err)
	assert.Equal(t, "ok", out["main"][0].JSON["write_data"])
	assert.FileExists(t, filepath.Join(f.Data, "kept.txt"))

	found, err := pf.List()
	require.NoError(t, err)
	assert.Len(t, found, 1)

	// Its program changed after the files' check is refused, its path the same (P17).
	_ = os.Remove(filepath.Join(f.Dir, "planted")) // what the disk node wrote where no sandbox refused it

	f, err = pf.Open("echo", "1.2.0")
	require.NoError(t, err)
	assert.Equal(t, hash(echo), f.ProgramSum)

	other, err := os.ReadFile(bare)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(f.Program, 0o700))
	require.NoError(t, os.WriteFile(f.Program, other, 0o700)) //nolint:gosec // a program

	again := pluginhost.NewRuntime(ctx, sandboxed(pluginhost.Deps{}), registry(t))
	t.Cleanup(again.Close)

	assert.ErrorContains(t, again.Load(ctx, f), "its program changed after its files were checked")
}
