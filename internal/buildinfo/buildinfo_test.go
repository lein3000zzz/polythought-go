package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGet(t *testing.T) {
	stamp := func(t *testing.T, v, c string) {
		t.Helper()

		oldV, oldC := version, commit
		version, commit = v, c
		t.Cleanup(func() { version, commit = oldV, oldC })
	}

	t.Run("a release, as its build stamped it", func(t *testing.T) {
		stamp(t, "v0.1.2", "0123456789abcdef0123456789abcdef01234567")

		in := Get()
		assert.Equal(t, "0.1.2", in.Version, "without the v")
		assert.True(t, in.Release())
		assert.Equal(t, "0123456", in.Short())
		assert.Equal(t, "0.1.2 (0123456)", in.String())
		assert.NotEmpty(t, in.Go)
	})

	t.Run("nothing stamped is dev", func(t *testing.T) {
		stamp(t, "", "")

		in := Get()
		assert.Equal(t, Dev, in.Version)
		assert.False(t, in.Release())
	})

	t.Run("a pre-release keeps its suffix", func(t *testing.T) {
		stamp(t, "0.2.0-beta.1", "abc")

		in := Get()
		assert.Equal(t, "0.2.0-beta.1", in.Version)
		assert.Equal(t, "abc", in.Short(), "a short hash stays as it is")
		assert.Equal(t, "0.2.0-beta.1 (abc)", in.String())
	})
}

func TestString(t *testing.T) {
	assert.Equal(t, "dev", Info{Version: Dev}.String())
	assert.Equal(t, "dev (0123456, modified)", Info{Version: Dev, Commit: "0123456789", Modified: true}.String())
}
