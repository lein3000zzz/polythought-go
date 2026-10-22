//go:build linux

// Package probe tries what a sandboxed program may and may not do, for the sandbox's
// tests: as a program of its own (testdata/probe), and as an entry the launcher runs in
// place.
package probe

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Try tries each thing and says how it went: "ok", or the error. dir is the program's
// directory and data its lasting one, outside one it may not reach, holding a stream
// socket and a datagram one listening, each empty for none, which is then not tried;
// again runs the program itself again, doing nothing.
func Try(dir, data, outside string, again []string) map[string]string {
	tmp := os.TempDir()
	parent := os.Getppid()

	try := map[string]func() error{
		"read /etc":       func() error { _, err := os.ReadFile("/etc/hostname"); return err },
		"read the parent": func() error { _, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", parent)); return err },
		"write its temp":  func() error { return os.WriteFile(filepath.Join(tmp, "x"), []byte("x"), 0o600) },
		"read /dev/urandom": func() error {
			f, err := os.Open("/dev/urandom")
			if err == nil {
				_ = f.Close()
			}
			return err
		},
		"run a shell":       func() error { return exec.Command("/bin/sh", "-c", "true").Run() },
		"run what it wrote": func() error { return runCopy(tmp) },
		"run itself again":  func() error { return exec.Command(again[0], again[1:]...).Run() }, //nolint:gosec // itself
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
		"make a stream pair": func() error {
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err == nil {
				_, _ = unix.Close(fds[0]), unix.Close(fds[1])
			}
			return err
		},
		"signal the parent": func() error { return syscall.Kill(parent, 0) },
		"signal itself":     func() error { return syscall.Kill(os.Getpid(), 0) },
		"trace the parent":  func() error { return syscall.PtraceAttach(parent) },
		"open 2000 files":   func() error { return openMany(2000) },
		"make a memfd": func() error {
			fd, err := unix.MemfdCreate("probe", unix.MFD_CLOEXEC)
			if err == nil {
				_ = unix.Close(fd)
			}
			return err
		},
		"get shared memory": func() error {
			id, err := unix.SysvShmGet(unix.IPC_PRIVATE, 4096, unix.IPC_CREAT|0o600)
			if err == nil {
				_, _ = unix.SysvShmCtl(id, unix.IPC_RMID, nil)
			}
			return err
		},
		"change its personality": func() error {
			_, _, errno := unix.Syscall(unix.SYS_PERSONALITY, 0xffffffff, 0, 0) // asks, changing nothing
			return errnoErr(errno)
		},
		"push input into its terminal": func() error { return tiocsti(unix.TIOCSTI) },
		"push input, high bits set":    func() error { return tiocsti(1<<32 | unix.TIOCSTI) },
		"find stdin at EOF": func() error {
			n, err := os.Stdin.Read(make([]byte, 64))
			if errors.Is(err, io.EOF) && n == 0 {
				return nil
			}
			return fmt.Errorf("read %d bytes (%v)", n, err)
		},
		"have a session of its own": func() error {
			if sid, err := unix.Getsid(0); err != nil || sid != os.Getpid() {
				return fmt.Errorf("its session is %d (%v)", sid, err)
			}
			return nil
		},
		"run at nice 10": func() error {
			prio, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
			if err == nil && 20-prio != 10 { // the kernel's 20 - nice
				err = fmt.Errorf("it runs at nice %d", 20-prio)
			}
			return err
		},
		"raise its priority":       func() error { return unix.Setpriority(unix.PRIO_PROCESS, 0, 0) },
		"map past its data limit":  mapPast,
		"write past its file size": func() error { return writePast(tmp) },
	}

	if dir != "" {
		try["read its dir"] = func() error { _, err := os.ReadFile(filepath.Join(dir, "manifest.json")); return err }
		try["write its dir"] = func() error { return os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600) }
	}

	if data != "" {
		try["write its data"] = func() error { return os.WriteFile(filepath.Join(data, "x"), []byte("x"), 0o600) }
	}

	if outside != "" {
		stream, dgram := filepath.Join(outside, "stream"), &unix.SockaddrUnix{Name: filepath.Join(outside, "dgram")}

		try["connect outside"] = func() error {
			c, err := net.Dial("unix", stream)
			if err == nil {
				_ = c.Close()
			}
			return err
		}
		try["send outside from a datagram socket"] = func() error {
			fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				return err
			}
			defer func() { _ = unix.Close(fd) }()

			return unix.Sendto(fd, []byte("x"), 0, dgram)
		}
		try["send outside from a datagram pair"] = func() error {
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				return err
			}
			defer func() { _, _ = unix.Close(fds[0]), unix.Close(fds[1]) }()

			return unix.Sendto(fds[0], []byte("x"), 0, dgram)
		}
	}

	out := map[string]string{}

	for name, fn := range try {
		if err := fn(); err != nil {
			out[name] = err.Error()
		} else {
			out[name] = "ok"
		}
	}

	return out
}

// errNoLimit is a limit not set.
var errNoLimit = errors.New("no limit")

// mapPast maps as much memory as its data limit, past it with what it has mapped already.
func mapPast() error {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_DATA, &rl); err != nil {
		return err
	}

	if rl.Cur == unix.RLIM_INFINITY {
		return errNoLimit
	}

	b, err := unix.Mmap(-1, 0, int(rl.Cur), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS) //nolint:gosec // a limit set from an int64
	if err == nil {
		_ = unix.Munmap(b)
	}

	return err
}

// writePast writes a file one byte past its file size limit, in dir.
func writePast(dir string) error {
	var rl unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &rl); err != nil {
		return err
	}

	if rl.Cur == unix.RLIM_INFINITY {
		return errNoLimit
	}

	return os.WriteFile(filepath.Join(dir, "big"), make([]byte, rl.Cur+1), 0o600)
}

// tiocsti pushes a byte into the input of the terminal fd 0 would be, by the ioctl req.
func tiocsti(req uint64) error {
	c := byte('\n')

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, 0, uintptr(req), uintptr(unsafe.Pointer(&c))) //nolint:gosec // to be refused

	return errnoErr(errno)
}

func errnoErr(errno unix.Errno) error {
	if errno != 0 {
		return errno
	}

	return nil
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
	if err := os.WriteFile(at, self, 0o700); err != nil { //nolint:gosec // its own copy, to be refused
		return err
	}

	return exec.Command(at).Run() //nolint:gosec // to be refused
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
