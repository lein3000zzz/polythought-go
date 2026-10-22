//go:build linux

package sandbox_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	tries "github.com/lein3000zzz/polythought-go/internal/adapters/sandbox/internal/probe"
)

// probe is the probe's program, built once, in its version's directory.
var probe string

// grown is what the grow entry holds.
var grown [][]byte

// entries are the test binary's own: the probe, run in place, and one taking memory until
// it can take no more.
var entries = []sandbox.Entry{
	{Name: "probe", Run: func() error {
		self, err := os.Executable()
		if err != nil {
			return err
		}

		return json.NewEncoder(os.Stdout).Encode(probeTry(self))
	}},
	{Name: "grow", Run: func() error {
		for {
			grown = append(grown, make([]byte, 1<<20))
		}
	}},
}

// probeTry is what the probe says of an entry: no directories but its temporary one, and
// running itself again runs no test.
func probeTry(self string) map[string]string {
	return tries.Try("", "", os.Getenv("PROBE_OUTSIDE"), []string{self, "-test.run=^$"})
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

// What a program and an entry alike may and may not do; filtered are refused by the
// seccomp filter itself, with EPERM, where the kernel would answer otherwise (P6, P7, P8,
// P30).
var (
	mayAlways = []string{
		"write its temp", "read /dev/urandom", "listen unix", "make a stream pair", "signal itself",
		"find stdin at EOF", "have a session of its own", "run at nice 10",
	}
	mayNever = []string{
		"read /etc", "read the parent", "run a shell", "run what it wrote", "run itself again",
		"dial tcp", "listen tcp", "listen udp", "signal the parent", "trace the parent", "open 2000 files",
		"raise its priority",
	}
	filtered = []string{
		"connect outside", "send outside from a datagram socket", "send outside from a datagram pair",
		"make a memfd", "get shared memory", "change its personality",
		"push input into its terminal", "push input, high bits set",
	}
)

// probed runs cmd, the probe under the sandbox, and checks what it says it may and may not
// do, besides what every one may and may not.
func probed(t *testing.T, cmd *exec.Cmd, mayToo, mayNotToo []string) map[string]string {
	t.Helper()

	var stderr strings.Builder
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	require.NoError(t, err, "the probe ran: %s", stderr.String())

	var got map[string]string
	require.NoError(t, json.Unmarshal(out, &got), string(out))

	for _, may := range append(mayToo, mayAlways...) {
		assert.Equal(t, "ok", got[may], "it may %s", may)
	}

	for _, mayNot := range append(mayNotToo, mayNever...) {
		assert.NotEqual(t, "ok", got[mayNot], "it may not %s", mayNot)
	}

	for _, mayNot := range filtered {
		assert.Contains(t, got[mayNot], unix.EPERM.Error(), "it may not %s", mayNot)
	}

	t.Logf("refused as: %v", got)

	return got
}

// outside is a directory the sandbox does not reach, a stream socket and a datagram one
// listening in it.
func outside(t *testing.T) string {
	dir := t.TempDir()

	l, err := net.Listen("unix", filepath.Join(dir, "stream"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	c, err := net.ListenPacket("unixgram", filepath.Join(dir, "dgram"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	return dir
}

// A program is held to its limits: it maps no more than its memory, and writes no file
// past its file size, told so with EFBIG rather than killed (P7, P9). Its stdin is not
// the one its command was given, which go-plugin makes the server's own (P8).
func TestSandbox(t *testing.T) {
	l, err := sandbox.New()
	if err != nil {
		t.Skipf("no sandbox on this kernel: %v", err)
	}

	temp, data := t.TempDir(), t.TempDir()

	cmd, err := l.Command(sandbox.Policy{
		Program: probe, Dir: filepath.Dir(probe), Temp: temp, Data: data, Memory: 256 << 20, FileSize: 16 << 20,
	})
	require.NoError(t, err)

	cmd.Env = []string{"TMPDIR=" + temp, "PROBE_DIR=" + filepath.Dir(probe), "PROBE_DATA=" + data, "PROBE_OUTSIDE=" + outside(t)}
	cmd.Stdin = strings.NewReader("the server's input\n")

	got := probed(t, cmd, []string{"read its dir", "write its data"}, []string{"write its dir"})

	assert.Equal(t, unix.ENOMEM.Error(), got["map past its data limit"])
	assert.Contains(t, got["write past its file size"], unix.EFBIG.Error())
}

// An entry of the binary's own runs in place under the sandbox, held as a program is, with
// no directory of programs: it runs none, itself neither (D356). No limit was set, so none
// holds it.
func TestEntry(t *testing.T) {
	l, err := sandbox.New()
	if err != nil {
		t.Skipf("no sandbox on this kernel: %v", err)
	}

	temp := t.TempDir()

	cmd, err := l.Command(sandbox.Policy{Entry: "probe", Temp: temp})
	require.NoError(t, err)

	cmd.Env = []string{"TMPDIR=" + temp, "PROBE_OUTSIDE=" + outside(t)}

	got := probed(t, cmd, nil, nil)

	assert.Equal(t, "no limit", got["map past its data limit"])
	assert.Equal(t, "no limit", got["write past its file size"])
}

// Past its memory, a Go process dies: of its runtime's fatal error, which takes the
// kernel's refusal of more for want of memory and cannot recover from it; linked with cgo,
// of a thread whose stack it cannot map; built with the race detector, of its shadow
// memory (P7).
func TestMemoryLimit(t *testing.T) {
	l, err := sandbox.New()
	if err != nil {
		t.Skipf("no sandbox on this kernel: %v", err)
	}

	temp := t.TempDir()

	cmd, err := l.Command(sandbox.Policy{Entry: "grow", Temp: temp, Memory: 128 << 20})
	require.NoError(t, err)

	cmd.Env = []string{"TMPDIR=" + temp}

	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "it grew past its memory: %s", out)
	assert.Regexp(t, `fatal error: (runtime: )?(out of memory|cannot allocate memory)|runtime/cgo: pthread_create failed|ThreadSanitizer failed to allocate`, string(out))

	t.Logf("it died: %v, saying %.200q", err, out)
}

func TestLaunchRefusesNoPolicy(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)

	out, err := exec.Command(self, "__polythought_sandbox", "bm90IGEgcG9saWN5").CombinedOutput() //nolint:gosec // itself
	require.Error(t, err, string(out))
	assert.Contains(t, string(out), "sandbox:")
}
