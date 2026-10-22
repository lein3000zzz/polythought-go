//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/landlock-lsm/go-landlock/landlock"
	ll "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"golang.org/x/sys/unix"
)

func available() error {
	abi, err := ll.LandlockGetABIVersion()
	if err != nil {
		return fmt.Errorf("%w: the kernel has no Landlock (%v)", ErrUnavailable, err)
	}

	if abi < MinLandlock {
		return fmt.Errorf("%w: the kernel's Landlock is version %d, and %d is needed (Linux 6.2)", ErrUnavailable, abi, MinLandlock)
	}

	if !seccomp.Supported() {
		return fmt.Errorf("%w: the kernel has no seccomp filters", ErrUnavailable)
	}

	return nil
}

// Rights on what a program reads and writes, never running it, nor linking it elsewhere.
const (
	read  = ll.AccessFSReadFile | ll.AccessFSReadDir
	write = ll.AccessFSWriteFile | ll.AccessFSTruncate | ll.AccessFSRemoveDir | ll.AccessFSRemoveFile |
		ll.AccessFSMakeDir | ll.AccessFSMakeReg | ll.AccessFSMakeSock
)

// become puts the process under the sandbox, then execs the program, which inherits it,
// or runs run in place: every thread of the process is restricted, as are those it starts.
func become(p Policy, run func() error) error {
	if err := available(); err != nil {
		return err
	}

	for resource, limit := range map[int]uint64{unix.RLIMIT_NOFILE: MaxFiles, unix.RLIMIT_CORE: 0} {
		if err := unix.Setrlimit(resource, &unix.Rlimit{Cur: limit, Max: limit}); err != nil {
			return fmt.Errorf("limits: %w", err)
		}
	}

	rules := []landlock.Rule{
		landlock.PathAccess(read|write, p.Temp).WithResolveUnix(),
		landlock.PathAccess(ll.AccessFSReadFile|ll.AccessFSWriteFile, "/dev/null"),
		landlock.PathAccess(ll.AccessFSReadFile, "/dev/urandom", "/dev/random").IgnoreIfMissing(),
		landlock.PathAccess(read, "/usr/share/zoneinfo").IgnoreIfMissing(),
		landlock.PathAccess(ll.AccessFSReadFile, "/etc/localtime").IgnoreIfMissing(),
	}

	if p.Dir != "" {
		rules = append(rules, landlock.RODirs(p.Dir)) // its programs, the only ones it may run
	}

	if p.Data != "" {
		rules = append(rules, landlock.PathAccess(read|write, p.Data))
	}

	// No network rule: no TCP, nor UDP where the kernel restricts it. Signals and abstract
	// sockets are scoped to the sandbox where the kernel has scopes (V6).
	if err := landlock.V10.BestEffort().Restrict(rules...); err != nil {
		return fmt.Errorf("landlock: %w", err)
	}

	if err := seccomp.LoadFilter(filter()); err != nil {
		return fmt.Errorf("seccomp: %w", err)
	}

	if run != nil {
		return run()
	}

	//nolint:gosec // the program the host was given, in the directory it may run programs from
	return syscall.Exec(p.Program, []string{p.Program}, os.Environ())
}

// filter is the seccomp filter: everything allowed but what a program never needs and the
// sandbox must not give, refused with EPERM. The process's ID is the program's, as exec
// keeps it, so it may still signal itself, as Go's runtime does.
func filter() seccomp.Filter {
	self := uint64(os.Getpid()) //nolint:gosec // a pid

	// No process of its own, which its caps would not reach (D349): threads only.
	var spawn []string
	if runtime.GOARCH == "amd64" {
		spawn = []string{"fork", "vfork"} // arm64 has only clone
	}

	return seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionAllow,
			Syscalls: []seccomp.SyscallGroup{
				{
					Action: seccomp.ActionErrno,
					Names: append([]string{
						// Others' processes, and the kernel.
						"ptrace", "process_vm_readv", "process_vm_writev", "pidfd_getfd", "pidfd_send_signal", "tkill",
						"kcmp", "perf_event_open", "bpf", "userfaultfd", "keyctl", "add_key", "request_key",
						"init_module", "finit_module", "delete_module", "kexec_load", "kexec_file_load", "reboot",
						"swapon", "swapoff", "acct", "quotactl",
						// Mounts and namespaces, which could undo the rest.
						"mount", "umount2", "pivot_root", "chroot", "unshare", "setns", "open_tree", "move_mount",
						"fsopen", "fsconfig", "fsmount", "fspick", "mount_setattr",
						// io_uring opens sockets past this filter.
						"io_uring_setup", "io_uring_enter", "io_uring_register",
					}, spawn...),
					NamesWithCondtions: []seccomp.NameWithConditions{
						// Threads, never a process.
						{Name: "clone", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.BitsNotSet, Value: unix.CLONE_THREAD}}},
						// Sockets but Unix ones: no network of its own (D287).
						{Name: "socket", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: unix.AF_UNIX}}},
						{Name: "socketpair", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: unix.AF_UNIX}}},
						// Signals only to itself.
						{Name: "kill", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: self}}},
						{Name: "tgkill", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: self}}},
						{Name: "rt_sigqueueinfo", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: self}}},
						{Name: "rt_tgsigqueueinfo", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: self}}},
						// Its own limits only, never raised past what it was given.
						{Name: "prlimit64", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: 0}}},
					},
				},
				{
					// clone3's flags are past a filter's reach: refused as if the kernel had
					// none, so a C library starts its threads with clone.
					Action: seccomp.Action(unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)),
					Names:  []string{"clone3"},
				},
			},
		},
	}
}
