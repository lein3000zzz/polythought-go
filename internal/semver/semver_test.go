package semver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	v, ok := Parse("1.22.333-beta.4.x-y")
	require.True(t, ok)
	assert.Equal(t, Version{Major: 1, Minor: 22, Patch: 333, Pre: []string{"beta", "4", "x-y"}}, v)
	assert.True(t, v.Prerelease())
	assert.Equal(t, "1.22.333-beta.4.x-y", v.String())

	v, ok = Parse("0.0.0")
	require.True(t, ok)
	assert.False(t, v.Prerelease())
	assert.Equal(t, "0.0.0", v.String())

	for _, bad := range []string{
		"", "dev", "v1.2.3", "1.2", "1.2.3.4", "01.2.3", "1.02.3", "1.2.3-", "1.2.3-01", "1.2.3-a..b",
		"1.2.3+build", "1.2.3 ", " 1.2.3", "1.2.3-a b", "1.2.3-$(id)", "1.2.3\n",
		"99999999999999999999.0.0", "1.2.3-" + strings.Repeat("a", maxLen),
	} {
		assert.False(t, Valid(bad), "%q", bad)
	}
}

func TestCompare(t *testing.T) {
	// semver.org's own order, each before the next.
	order := []string{
		"0.0.1", "0.1.0", "0.9.9", "0.10.0", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.10.0", "2.0.0",
	}

	for i := range order {
		for j := range order {
			a, _ := Parse(order[i])
			b, _ := Parse(order[j])

			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}

			assert.Equal(t, want, Compare(a, b), "%s against %s", order[i], order[j])
		}
	}
}

func TestNewer(t *testing.T) {
	assert.True(t, Newer("0.0.2", "0.0.1"))
	assert.False(t, Newer("0.0.1", "0.0.1"))
	assert.False(t, Newer("0.0.1", "0.0.2"))
	assert.True(t, Newer("0.1.0", "0.1.0-beta.1"))
	assert.False(t, Newer("0.0.2", "dev"), "a development build is no version to compare")
	assert.False(t, Newer("dev", "0.0.1"))
}
