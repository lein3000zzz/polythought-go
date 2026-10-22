// Package sandbox runs plugins' programs under the sandbox (D12, D337): on Linux, Landlock
// and seccomp, which a launcher puts itself under before it becomes the program, so the
// server is never restricted itself and the program's process is the launcher's.
//
// The launcher is the server's own binary run again, its first argument naming the mode:
// main calls Launch first thing, which returns at once unless it is the launcher.
//
// A program under the sandbox (D339, D343):
//
//   - reads its version's directory, and starts no process: threads only (D349);
//   - reads and writes a private temporary directory and its lasting directory, never
//     running what it writes there;
//   - reads /dev/null, /dev/urandom and the time zones, writes /dev/null; nothing else on
//     disk, the server's files and /proc included;
//   - opens no socket but a Unix one, so no network at all: what it fetches goes through
//     the host;
//   - signals only itself, traces nothing, mounts nothing, makes no namespace, loads
//     nothing into the kernel, and has no io_uring;
//   - opens at most 1024 files, and dumps no core.
package sandbox

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// mode is the launcher's first argument.
const mode = "__polythought_sandbox"

// MinLandlock is the Landlock ABI the sandbox needs: 3, Linux 6.2, which also restricts
// truncating files. Below it there is no sandbox (D338).
const MinLandlock = 3

// MaxFiles is how many files a sandboxed program may have open (D343).
const MaxFiles = 1024

// ErrUnavailable is a server that cannot sandbox programs: not Linux, or a kernel without
// enough of Landlock. The error says which.
var ErrUnavailable = errors.New("plugins cannot be sandboxed here")

// Policy is what a program may touch.
type Policy struct {
	Program string // the program to become
	Dir     string // its version's directory, read only, the only programs it may run
	Temp    string // its private temporary directory, read and written
	Data    string // its lasting directory, read and written; empty for none
}

// Launcher starts programs under the sandbox, through the server's own binary.
type Launcher struct {
	self string
}

// New is a launcher, or ErrUnavailable, saying why, where there is no sandbox.
func New() (*Launcher, error) {
	if err := available(); err != nil {
		return nil, err
	}

	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("the server's own binary: %w", err)
	}

	return &Launcher{self: self}, nil
}

// Command is the command that starts p's program under the sandbox, as exec.Command(p.Program)
// would start it bare; set its environment as for the program.
func (l *Launcher) Command(p Policy) (*exec.Cmd, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	return exec.Command(l.self, mode, base64.StdEncoding.EncodeToString(raw)), nil //nolint:gosec // the server's own binary
}

// Launch, in the launcher, puts the process under the sandbox and becomes the program; it
// never returns there, and exits saying why when it cannot. Anywhere else it returns at
// once: call it first thing in main.
func Launch() {
	if len(os.Args) != 3 || os.Args[1] != mode {
		return
	}

	if err := launch(os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox:", err)
		os.Exit(70)
	}
}

func launch(arg string) error {
	raw, err := base64.StdEncoding.DecodeString(arg)
	if err != nil {
		return err
	}

	var p Policy
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}

	if p.Program == "" || p.Dir == "" || p.Temp == "" {
		return errors.New("a program, its directory and a temporary one are needed")
	}

	return become(p)
}
