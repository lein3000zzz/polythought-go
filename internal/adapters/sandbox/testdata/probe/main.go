// Probe tries what a sandboxed program may and may not do, and says how each went, as
// JSON: "ok", or the error.
package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func main() {
	if len(os.Args) > 1 {
		return // run again by itself
	}

	dir, data, tmp := os.Getenv("PROBE_DIR"), os.Getenv("PROBE_DATA"), os.TempDir()
	parent := os.Getppid()

	try := map[string]func() error{
		"read its dir":    func() error { _, err := os.ReadFile(filepath.Join(dir, "manifest.json")); return err },
		"write its dir":   func() error { return os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600) },
		"read /etc":       func() error { _, err := os.ReadFile("/etc/hostname"); return err },
		"read the parent": func() error { _, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", parent)); return err },
		"write its temp":  func() error { return os.WriteFile(filepath.Join(tmp, "x"), []byte("x"), 0o600) },
		"write its data":  func() error { return os.WriteFile(filepath.Join(data, "x"), []byte("x"), 0o600) },
		"read /dev/urandom": func() error {
			f, err := os.Open("/dev/urandom")
			if err == nil {
				_ = f.Close()
			}
			return err
		},
		"run a shell":       func() error { return exec.Command("/bin/sh", "-c", "true").Run() },
		"run what it wrote": func() error { return runCopy(tmp) },
		"run itself again":  func() error { return exec.Command(os.Args[0], "again").Run() }, //nolint:gosec // itself
		"dial tcp": func() error {
			c, err := net.Dial("tcp", "1.1.1.1:53")
			if err == nil {
				_ = c.Close()
			}
			return err
		},
		"listen tcp": func() error {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err == nil {
				_ = l.Close()
			}
			return err
		},
		"listen udp": func() error {
			c, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err == nil {
				_ = c.Close()
			}
			return err
		},
		"listen unix": func() error {
			l, err := net.Listen("unix", filepath.Join(tmp, "s"))
			if err == nil {
				_ = l.Close()
			}
			return err
		},
		"signal the parent": func() error { return syscall.Kill(parent, 0) },
		"signal itself":     func() error { return syscall.Kill(os.Getpid(), 0) },
		"trace the parent":  func() error { return syscall.PtraceAttach(parent) },
		"open 2000 files":   func() error { return openMany(2000) },
	}

	out := map[string]string{}
	for name, fn := range try {
		if err := fn(); err != nil {
			out[name] = err.Error()
		} else {
			out[name] = "ok"
		}
	}

	_ = json.NewEncoder(os.Stdout).Encode(out)
}

// runCopy copies its own program to dir and runs the copy.
func runCopy(dir string) error {
	self, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		if self, err = os.ReadFile(os.Args[0]); err != nil {
			return err
		}
	}

	at := filepath.Join(dir, "copy")
	if err := os.WriteFile(at, self, 0o700); err != nil {
		return err
	}

	return exec.Command(at).Run()
}

func openMany(n int) error {
	var files []*os.File
	defer func() {
		for _, f := range files {
			_ = f.Close()
		}
	}()

	for range n {
		f, err := os.Open("/dev/null")
		if err != nil {
			return err
		}

		files = append(files, f)
	}

	return nil
}
