//go:build !linux

package sandbox_test

import (
	"testing"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
)

// entries are the test binary's own beside those of every system: none here.
var entries []sandbox.Entry

func run(m *testing.M) int { return m.Run() }
