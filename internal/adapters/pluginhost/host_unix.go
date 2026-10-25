//go:build unix

package pluginhost

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// ownSession gives an unsandboxed plugin's process a session of its own (P8), with no
// controlling terminal: where the server runs in one, the plugin cannot push input into
// it through the stdin go-plugin hands it, the server's.
func ownSession(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// unlinked is what the files pid holds open with no name left take, as Usage counts them
// (P9): a plugin's data that no walk of its directories finds. Linux shows them in /proc;
// elsewhere there are none to count. A process gone, or on its way out, takes nothing.
func unlinked(pid int) (int64, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}

	dir := fmt.Sprintf("/proc/%d/fd", pid)

	fds, err := os.ReadDir(dir)
	if gone(pid, err) {
		return 0, nil
	} else if err != nil {
		return 0, bare(err)
	}

	var (
		total int64
		seen  []fs.FileInfo // one file open more than once is counted once
	)

	for _, fd := range fds {
		info, err := os.Stat(filepath.Join(dir, fd.Name()))

		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue // closed meanwhile
		case gone(pid, err):
			return 0, nil // on its way out meanwhile
		case err != nil:
			return 0, bare(err)
		}

		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || st.Nlink != 0 {
			continue
		}

		dup := false
		for _, s := range seen {
			dup = dup || os.SameFile(s, info)
		}

		if !dup {
			seen = append(seen, info)
			total += rounded(info.Size())
		}
	}

	return total, nil
}

// gone is whether err, reading pid's /proc, says it is gone or on its way out: no such
// process, or one refused for having let go of its memory, as it does as it exits, the
// kernel then giving its /proc to root and refusing the server's user. One that keeps its
// memory, its files hidden from the server, is not.
func gone(pid int, err error) bool {
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH):
		return true
	case !errors.Is(err, fs.ErrPermission):
		return false
	}

	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH)
	}

	size, _, _ := strings.Cut(string(raw), " ")

	return size == "0"
}
