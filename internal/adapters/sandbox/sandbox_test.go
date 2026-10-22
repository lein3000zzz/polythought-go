package sandbox_test

import (
	"fmt"
	"os"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
)

func TestMain(m *testing.M) {
	// This test binary is the launcher too, as the server's is, with entries of its own.
	sandbox.Launch(append(entries, sandbox.Entry{Name: "hello", Run: func() error {
		_, err := fmt.Println("hello")
		return err
	}})...)

	os.Exit(run(m))
}

// Where there is no sandbox, an entry runs without it (D354); one the binary has not is
// refused.
func TestEntryCommand(t *testing.T) {
	cmd, err := sandbox.EntryCommand("hello")
	require.NoError(t, err)

	out, err := cmd.Output()
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(out))

	cmd, err = sandbox.EntryCommand("none")
	require.NoError(t, err)

	out, err = cmd.CombinedOutput()
	require.Error(t, err)
	assert.Contains(t, string(out), `the binary has no entry "none"`)
}

func TestUnavailableOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux has the sandbox")
	}

	_, err := sandbox.New()
	require.ErrorIs(t, err, sandbox.ErrUnavailable)
	assert.Contains(t, err.Error(), "only Linux has the sandbox")
}
