//go:build linux

package sandbox

import (
	"encoding/binary"
	"runtime"
	"testing"

	"github.com/elastic/go-seccomp-bpf/arch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// The filter names only syscalls this architecture has: one it lacks fails to assemble.
func TestFilterAssembles(t *testing.T) {
	f := filter()

	_, err := f.Policy.Assemble()
	require.NoError(t, err)
}

// The filters refuse what they should, as the kernel runs them, on this architecture
// whatever the kernel: the emulated one of another too, which has no Landlock to run the
// probe under.
func TestFilterDecides(t *testing.T) {
	f := filter()

	insts, err := f.Policy.Assemble()
	require.NoError(t, err)

	prog := make([]unix.SockFilter, 0, len(insts))

	for _, in := range insts {
		raw, err := in.Assemble()
		require.NoError(t, err)

		prog = append(prog, unix.SockFilter{Code: raw.Op, Jt: raw.Jt, Jf: raw.Jf, K: raw.K})
	}

	own, err := native()
	require.NoError(t, err)

	info, err := arch.GetInfo("")
	require.NoError(t, err)

	const (
		allow = unix.SECCOMP_RET_ALLOW
		eperm = unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)
	)

	for _, c := range []struct {
		name string
		args []uint64
		want uint32
	}{
		{"read", nil, allow},
		{"connect", []uint64{3}, eperm},
		{"socket", []uint64{unix.AF_UNIX, unix.SOCK_STREAM | unix.SOCK_NONBLOCK | unix.SOCK_CLOEXEC}, allow},
		{"socket", []uint64{unix.AF_UNIX, unix.SOCK_DGRAM | unix.SOCK_CLOEXEC}, eperm},
		{"socket", []uint64{unix.AF_UNIX, unix.SOCK_RAW}, eperm}, // a Unix datagram socket too
		{"socket", []uint64{unix.AF_UNIX, unix.SOCK_SEQPACKET}, eperm},
		{"socket", []uint64{unix.AF_INET, unix.SOCK_STREAM}, eperm},
		{"socket", []uint64{1<<32 | unix.AF_UNIX, unix.SOCK_STREAM}, eperm},
		{"socketpair", []uint64{unix.AF_UNIX, unix.SOCK_STREAM | unix.SOCK_CLOEXEC}, allow},
		{"socketpair", []uint64{unix.AF_UNIX, unix.SOCK_DGRAM}, eperm},
		{"ioctl", []uint64{0, unix.TCGETS}, allow},
		{"ioctl", []uint64{0, unix.TIOCSTI}, eperm},
		{"ioctl", []uint64{0, 1<<32 | unix.TIOCSTI}, eperm},
		{"ioctl", []uint64{0, unix.TIOCSTI | 1<<8}, allow},
		{"ioctl", []uint64{0, unix.TIOCLINUX}, eperm},
		{"memfd_create", nil, eperm},
		{"memfd_secret", nil, eperm},
		{"shmget", nil, eperm},
		{"shmat", nil, eperm},
		{"shmctl", nil, eperm},
		{"personality", []uint64{0xffffffff}, eperm},
		{"clone", []uint64{unix.CLONE_VM | unix.CLONE_THREAD}, allow},
		{"clone", []uint64{uint64(unix.SIGCHLD)}, eperm},
		{"clone3", nil, unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)},
	} {
		nr, ok := info.SyscallNames[c.name]
		require.True(t, ok, c.name)

		in := data(uint32(info.ID), nr, c.args)
		assert.Equal(t, c.want, decide(t, prog, in), "%s%v", c.name, c.args)
		assert.Equal(t, uint32(allow), decide(t, own, in), "%s%v of its own architecture", c.name, c.args)
	}

	// Another architecture's, such as an amd64 process's int 0x80 makes.
	other := map[string]*arch.Info{"amd64": arch.I386, "arm64": arch.ARM}[runtime.GOARCH]
	if other == nil {
		t.Skipf("no other architecture known beside %s", runtime.GOARCH)
	}

	in := data(uint32(other.ID), other.SyscallNames["socket"], []uint64{unix.AF_INET, unix.SOCK_STREAM})
	assert.Equal(t, uint32(unix.SECCOMP_RET_KILL_PROCESS), decide(t, own, in))
	t.Logf("%s's socket, by filter alone: %#x", other.Name, decide(t, prog, in))
}

// data is a syscall as the kernel lays it out for a filter, struct seccomp_data.
func data(arch uint32, nr int, args []uint64) []byte {
	b := make([]byte, 64)
	binary.NativeEndian.PutUint32(b[0:], uint32(nr)) //nolint:gosec // a syscall's number
	binary.NativeEndian.PutUint32(b[4:], arch)

	for i, a := range args {
		binary.NativeEndian.PutUint64(b[16+8*i:], a)
	}

	return b
}

// decide runs a classic BPF filter on in as the kernel would, knowing only the
// instructions filter and native are made of.
func decide(t *testing.T, prog []unix.SockFilter, in []byte) uint32 {
	t.Helper()

	var a uint32

	for pc := 0; pc < len(prog); pc++ {
		ins := prog[pc]

		jump := func(cond bool) {
			if cond {
				pc += int(ins.Jt)
			} else {
				pc += int(ins.Jf)
			}
		}

		switch ins.Code {
		case unix.BPF_LD | unix.BPF_W | unix.BPF_ABS:
			a = binary.NativeEndian.Uint32(in[ins.K:])
		case unix.BPF_JMP | unix.BPF_JA:
			pc += int(ins.K)
		case unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K:
			jump(a == ins.K)
		case unix.BPF_JMP | unix.BPF_JGT | unix.BPF_K:
			jump(a > ins.K)
		case unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K:
			jump(a >= ins.K)
		case unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K:
			jump(a&ins.K != 0)
		case unix.BPF_RET | unix.BPF_K:
			return ins.K
		default:
			require.Failf(t, "an instruction not known", "%#x at %d", ins.Code, pc)
		}
	}

	require.Fail(t, "the filter ran out without a return")

	return 0
}
