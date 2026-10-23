//go:build unix

package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// reexec replaces the process with a fresh run of the same binary, arguments and
// environment, which picks up settings that apply on restart. The process ID stays the
// same, so a container or service manager sees no exit.
func reexec() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("restart: %w", err)
	}

	if err := closeOnExec(); err != nil {
		return fmt.Errorf("restart: %w", err)
	}

	return fmt.Errorf("restart: %w", syscall.Exec(exe, os.Args, os.Environ()))
}

// closeOnExec marks every open file but stdin, stdout and stderr to close as the process
// execs. Hertz's network poller accepts connections without close-on-exec, so the new
// server would inherit the old one's open connections, serve none of them, and a proxy
// sending a request down one would wait for an answer that never comes.
func closeOnExec() error {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return err
	}

	for _, e := range entries {
		if fd, err := strconv.Atoi(e.Name()); err == nil && fd > 2 {
			syscall.CloseOnExec(fd) // the directory's own, closed by now, is no matter
		}
	}

	return nil
}
