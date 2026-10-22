package sandbox_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
)

func TestUnavailableOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("Linux has the sandbox")
	}

	_, err := sandbox.New()
	require.ErrorIs(t, err, sandbox.ErrUnavailable)
	assert.Contains(t, err.Error(), "only Linux has the sandbox")
}
