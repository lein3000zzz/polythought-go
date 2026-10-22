//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The filter names only syscalls this architecture has: one it lacks fails to assemble.
func TestFilterAssembles(t *testing.T) {
	f := filter()

	_, err := f.Policy.Assemble()
	require.NoError(t, err)
}
