//go:build linux

package sandbox_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
)

// probe is the probe's program, built once, in its version's directory.
var probe string

func TestMain(m *testing.M) {
	// This test binary is the launcher too, as the server's is.
	sandbox.Launch()

	dir, err := os.MkdirTemp("", "sandbox-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	probe = filepath.Join(dir, "version", "plugin")

	build := exec.Command("go", "build", "-o", probe, "./testdata/probe")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")

	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build the probe: %v\n%s", err, out)
		os.Exit(1)
	}

	if err := os.WriteFile(filepath.Join(dir, "version", "manifest.json"), []byte("{}"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	code := m.Run()

	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestSandbox(t *testing.T) {
	l, err := sandbox.New()
	if err != nil {
		t.Skipf("no sandbox on this kernel: %v", err)
	}

	temp, data := t.TempDir(), t.TempDir()

	cmd, err := l.Command(sandbox.Policy{Program: probe, Dir: filepath.Dir(probe), Temp: temp, Data: data})
	require.NoError(t, err)

	cmd.Env = []string{"TMPDIR=" + temp, "PROBE_DIR=" + filepath.Dir(probe), "PROBE_DATA=" + data}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoError(t, err, "the probe ran: %s", stderr.String())

	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got), string(out))

	for _, may := range []string{"read its dir", "write its temp", "write its data", "read /dev/urandom", "listen unix", "signal itself"} {
		assert.Equal(t, "ok", got[may], "it may %s", may)
	}

	for _, mayNot := range []string{
		"write its dir", "read /etc", "read the parent", "run a shell", "run what it wrote",
		"dial tcp", "listen tcp", "listen udp", "signal the parent", "trace the parent", "open 2000 files",
	} {
		assert.NotEqual(t, "ok", got[mayNot], "it may not %s", mayNot)
	}

	t.Logf("refused as: %v", got)
}

func TestLaunchRefusesNoPolicy(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)

	out, err := exec.Command(self, "__polythought_sandbox", "bm90IGEgcG9saWN5").CombinedOutput() //nolint:gosec // itself
	require.Error(t, err, string(out))
	assert.Contains(t, string(out), "sandbox:")
}
