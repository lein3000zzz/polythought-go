//go:build unix

package main

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// A file opened without close-on-exec, as Hertz's poller accepts connections, is closed
// by the restart's exec all the same.
func TestCloseOnExec(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fd")
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	fd, err := syscall.Dup(int(f.Fd())) // a dup has close-on-exec cleared
	require.NoError(t, err)
	t.Cleanup(func() { _ = syscall.Close(fd) })

	flags := func(fd int) int {
		v, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		require.NoError(t, err)
		return v
	}

	require.Zero(t, flags(fd)&unix.FD_CLOEXEC, "open across an exec, before")
	require.NoError(t, closeOnExec())
	assert.NotZero(t, flags(fd)&unix.FD_CLOEXEC, "closed by it, after")
	assert.Zero(t, flags(2)&unix.FD_CLOEXEC, "stderr stays")
}
