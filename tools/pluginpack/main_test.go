package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/plugindir"
)

// The host's test plugin, packed, is an archive the plugins directory takes.
func TestPack(t *testing.T) {
	out := filepath.Join(t.TempDir(), "echo.tar.gz")
	here := runtime.GOOS + "_" + runtime.GOARCH

	require.NoError(t, pack("../../internal/adapters/pluginhost/testdata/echo", out, []string{here}, ""))

	archive, err := os.Open(out) //nolint:gosec // the test's
	require.NoError(t, err)
	t.Cleanup(func() { _ = archive.Close() })

	dir, err := plugindir.Open(filepath.Join(t.TempDir(), "plugins"))
	require.NoError(t, err)

	v, err := dir.Install(archive, 256<<20, nil)
	require.NoError(t, err)

	assert.Equal(t, "echo", v.Manifest.ID)
	assert.Equal(t, "1.2.0", v.Manifest.Version)
	assert.NotEmpty(t, v.Manifest.SDK, "the program's manifest, which names its SDK")
	assert.FileExists(t, v.Program)
	assert.False(t, v.Signed)

	require.Error(t, pack("../../internal/adapters/pluginhost/testdata/echo", out, []string{"plan9"}, ""), "a platform not os_arch")
}

// The example plugin, a module of its own as an author's is, packs as well (D360).
func TestPackExample(t *testing.T) {
	out := filepath.Join(t.TempDir(), "movers.tar.gz")

	require.NoError(t, pack("../../examples/plugins/movers", out, []string{runtime.GOOS + "_" + runtime.GOARCH}, ""))

	archive, err := os.Open(out) //nolint:gosec // the test's
	require.NoError(t, err)
	t.Cleanup(func() { _ = archive.Close() })

	dir, err := plugindir.Open(filepath.Join(t.TempDir(), "plugins"))
	require.NoError(t, err)

	v, err := dir.Install(archive, 256<<20, nil)
	require.NoError(t, err)

	assert.Equal(t, "movers", v.Manifest.ID)
	assert.Equal(t, []string{"telegram", "fetch:api.coingecko.com"}, v.Manifest.Permissions)
}
