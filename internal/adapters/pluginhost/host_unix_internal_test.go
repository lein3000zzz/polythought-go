package pluginhost

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A process on its way out holds nothing (P9), though the kernel refuses a user other than
// root its /proc/<pid>/fd once it lets go of its memory, as the server's is: not data that
// cannot be measured, which would stop a plugin that only crashed, as past its cap.
func TestUnlinkedExiting(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counted on Linux")
	}

	cmd := exec.Command("true")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Wait() })

	// Exited and not yet waited for, as a plugin is until go-plugin sees it go.
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", cmd.Process.Pid))
		return err == nil && strings.Contains(string(raw), ") Z ")
	}, patience, 5*time.Millisecond)

	held, err := unlinked(cmd.Process.Pid)
	require.NoError(t, err)
	assert.Zero(t, held)
}
