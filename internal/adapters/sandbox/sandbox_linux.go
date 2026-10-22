//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"unsafe"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
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

	if err := detach(run == nil); err != nil {
		return err
	}

	if err := lower(); err != nil {
		return fmt.Errorf("its priority: %w", err)
	}

	for resource, limit := range limits(p) {
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

	if err := loadNative(); err != nil {
		return fmt.Errorf("seccomp: %w", err)
	}

	if run != nil {
		return run()
	}

	// Writing past its file size, it is told so, EFBIG, not killed, as a Go program's
	// runtime has it anyway (P9).
	signal.Ignore(syscall.SIGXFSZ)

	//nolint:gosec // the program the host was given, in the directory it may run programs from
	return syscall.Exec(p.Program, []string{p.Program}, os.Environ())
}

// detach takes the process off the server's terminal (P8): a session of its own, with no
// terminal to push input into (TIOCSTI) or to be signalled by, and a program's stdin, which
// go-plugin makes the server's own, /dev/null. An entry keeps the stdin its command gave it:
// the code runner's jobs come on it.
func detach(program bool) error {
	if program {
		null, err := unix.Open("/dev/null", unix.O_RDONLY, 0)
		if err == nil && null != 0 {
			err = unix.Dup3(null, 0, 0)
			_ = unix.Close(null)
		}

		if err != nil {
			return fmt.Errorf("its stdin: %w", err)
		}
	}

	if _, err := unix.Setsid(); err != nil {
		return fmt.Errorf("a session of its own: %w", err)
	}

	return nil
}

// niceness is the nice value a sandboxed process runs at, or past: one spinning takes the
// CPU from the server only where it has some to spare (P23).
const niceness = 10

// lower lowers the priority of every thread of the process, alone in its group since
// detach, to niceness, leaving one lower already as it is.
func lower() error {
	prio, err := unix.Getpriority(unix.PRIO_PGRP, 0)
	if err != nil {
		return err
	}

	if 20-prio >= niceness { // the kernel's 20 - nice, of the group's highest
		return nil
	}

	return unix.Setpriority(unix.PRIO_PGRP, 0, niceness)
}

// limits are the hard limits the process is held to, which it may lower but never raise
// (D343): its files, no core, no priority raised again (P23), and its memory and file size
// where p sets them (P7, P9).
func limits(p Policy) map[int]uint64 {
	l := map[int]uint64{unix.RLIMIT_NOFILE: MaxFiles, unix.RLIMIT_CORE: 0, unix.RLIMIT_NICE: 0, unix.RLIMIT_RTPRIO: 0}

	if p.Memory > 0 {
		l[unix.RLIMIT_DATA] = uint64(p.Memory)
	}

	if p.FileSize > 0 {
		l[unix.RLIMIT_FSIZE] = uint64(p.FileSize)
	}

	return l
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

	// Unix stream sockets only (P6): a datagram one, a pair's too, sends to any path it
	// names, which Landlock bounds only from ABI 9.
	var (
		notUnix   = seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.NotEqual, Value: unix.AF_UNIX}}
		notStream = seccomp.ArgumentConditions{{Argument: 1, Operation: seccomp.BitsSet, Value: sockType &^ unix.SOCK_STREAM}}
	)

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
						// Its personality, as it was given (P30).
						"personality",
						// Mounts and namespaces, which could undo the rest.
						"mount", "umount2", "pivot_root", "chroot", "unshare", "setns", "open_tree", "move_mount",
						"fsopen", "fsconfig", "fsmount", "fspick", "mount_setattr",
						// io_uring opens sockets past this filter.
						"io_uring_setup", "io_uring_enter", "io_uring_register",
						// No connecting, as Landlock bounds Unix sockets' reach only from ABI 9
						// (P6): a plugin listens, the host dials it, and a runner has its stdin
						// and stdout.
						"connect",
						// Memory past its resident set, which the host watches (P7).
						"memfd_create", "memfd_secret", "shmget", "shmat", "shmctl",
					}, spawn...),
					NamesWithCondtions: []seccomp.NameWithConditions{
						// Threads, never a process.
						{Name: "clone", Conditions: seccomp.ArgumentConditions{{Argument: 0, Operation: seccomp.BitsNotSet, Value: unix.CLONE_THREAD}}},
						// Sockets but Unix ones: no network of its own (D287).
						{Name: "socket", Conditions: notUnix},
						{Name: "socket", Conditions: notStream},
						{Name: "socketpair", Conditions: notUnix},
						{Name: "socketpair", Conditions: notStream},
						// No input pushed into a terminal, nor a console's selection pasted
						// (P8, P30).
						{Name: "ioctl", Conditions: low(1, unix.TIOCSTI)},
						{Name: "ioctl", Conditions: low(1, unix.TIOCLINUX)},
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

// sockType is the type in socket's second argument, the rest its flags.
const sockType = 0xf

// low matches an argument whose low 32 bits are v, whatever its high ones: the kernel reads
// an int argument from them alone, so a match of all 64 would miss v with high bits set.
// It is every bit of v set and every other low bit clear.
func low(arg, v uint32) seccomp.ArgumentConditions {
	c := seccomp.ArgumentConditions{{Argument: arg, Operation: seccomp.BitsNotSet, Value: uint64(^v)}}

	for bit := uint32(1); bit != 0; bit <<= 1 {
		if v&bit != 0 {
			c = append(c, seccomp.Condition{Argument: arg, Operation: seccomp.BitsSet, Value: uint64(bit)})
		}
	}

	return c
}

// native is a filter killing a process that makes a syscall of an architecture not its
// own: go-seccomp-bpf's filter allows those past every rule, and an amd64 process makes
// i386 ones with int 0x80, as an arm64 one would running an arm program.
func native() ([]unix.SockFilter, error) {
	own, err := arch.GetInfo("")
	if err != nil {
		return nil, err
	}

	return []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 4}, // the syscall's architecture
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 1, K: uint32(own.ID)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_KILL_PROCESS},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}, nil
}

// loadNative loads native over every thread of the process, after filter, which has set
// no_new_privs.
func loadNative() error {
	prog, err := native()
	if err != nil {
		return err
	}

	fprog := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}

	tid, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC,
		uintptr(unsafe.Pointer(&fprog))) //nolint:gosec // the kernel reads the filter

	switch {
	case errno != 0:
		return errno
	case tid != 0:
		return fmt.Errorf("thread %d could not take the filter", tid)
	}

	return nil
}
