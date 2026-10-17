//go:build unix

package main

import (
	"fmt"
	"os"
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

	return fmt.Errorf("restart: %w", syscall.Exec(exe, os.Args, os.Environ()))
}
