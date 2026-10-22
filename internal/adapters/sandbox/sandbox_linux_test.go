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
	tries "github.com/lein3000zzz/polythought-go/internal/adapters/sandbox/internal/probe"
)

// probe is the probe's program, built once, in its version's directory.
var probe string

// entries are the test binary's own: the probe, run in place.
var entries = []sandbox.Entry{{Name: "probe", Run: func() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}

	return json.NewEncoder(os.Stdout).Encode(probeTry(self))
}}}

// probeTry is what the probe says of an entry: no directories but its temporary one, and
// running itself again runs no test.
func probeTry(self string) map[string]string {
	return tries.Try("", "", []string{self, "-test.run=^$"})
}

// run builds the probe, then runs the tests.
func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "sandbox-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	probe = filepath.Join(dir, "version", "plugin")

	build := exec.Command("go", "build", "-o", probe, "./testdata/probe")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")

	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build the probe: %v\n%s", err, out)
		return 1
	}

	if err := os.WriteFile(filepath.Join(dir, "version", "manifest.json"), []byte("{}"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	return m.Run()
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
		"write its dir", "read /etc", "read the parent", "run a shell", "run what it wrote", "run itself again",
		"dial tcp", "listen tcp", "listen udp", "signal the parent", "trace the parent", "open 2000 files",
	} {
		assert.NotEqual(t, "ok", got[mayNot], "it may not %s", mayNot)
	}

	t.Logf("refused as: %v", got)
}

// An entry of the binary's own runs in place under the sandbox, held as a program is, with
// no directory of programs: it runs none, itself neither (D356).
func TestEntry(t *testing.T) {
	l, err := sandbox.New()
	if err != nil {
		t.Skipf("no sandbox on this kernel: %v", err)
	}

	temp := t.TempDir()

	cmd, err := l.Command(sandbox.Policy{Entry: "probe", Temp: temp})
	require.NoError(t, err)

	cmd.Env = []string{"TMPDIR=" + temp}

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoError(t, err, "the entry ran: %s", stderr.String())

	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got), string(out))

	for _, may := range []string{"write its temp", "read /dev/urandom", "listen unix", "signal itself"} {
		assert.Equal(t, "ok", got[may], "it may %s", may)
	}

	for _, mayNot := range []string{
		"read /etc", "read the parent", "run a shell", "run what it wrote", "run itself again",
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
