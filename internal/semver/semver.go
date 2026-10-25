// Package semver reads the versions Polythought's releases are tagged with (D425):
// major.minor.patch, and a pre-release after a hyphen, such as 0.2.0-beta.1, as
// semver.org orders them. No leading v and no build metadata: a version is also an image
// tag, which takes no +.
package semver

import (
	"regexp"
	"strconv"
	"strings"
)

// maxLen bounds a version: an image tag takes 128 characters, and no release needs many.
const maxLen = 64

// pattern is semver.org's, without build metadata.
var pattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?$`)

// Version is a parsed version.
type Version struct {
	Major, Minor, Patch uint64
	// Pre is the pre-release's dot-separated identifiers, none for a release.
	Pre []string
}

// Parse reads s, false where it is no version.
func Parse(s string) (Version, bool) {
	if len(s) > maxLen {
		return Version{}, false
	}

	m := pattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, false
	}

	var v Version

	for i, part := range []*uint64{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return Version{}, false
		}

		*part = n
	}

	if m[4] != "" {
		v.Pre = strings.Split(m[4], ".")
	}

	return v, true
}

// Valid says whether s is a version.
func Valid(s string) bool {
	_, ok := Parse(s)
	return ok
}

// Prerelease says whether v is a pre-release.
func (v Version) Prerelease() bool { return len(v.Pre) > 0 }

func (v Version) String() string {
	s := strconv.FormatUint(v.Major, 10) + "." + strconv.FormatUint(v.Minor, 10) + "." + strconv.FormatUint(v.Patch, 10)
	if v.Prerelease() {
		s += "-" + strings.Join(v.Pre, ".")
	}

	return s
}

// Compare is -1 where a comes before b, 1 after, 0 where they are the same version.
func Compare(a, b Version) int {
	for _, d := range [][2]uint64{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}

			return 1
		}
	}

	// A release comes after its pre-releases.
	switch {
	case !a.Prerelease() && !b.Prerelease():
		return 0
	case !a.Prerelease():
		return 1
	case !b.Prerelease():
		return -1
	}

	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		if c := compareIdentifier(a.Pre[i], b.Pre[i]); c != 0 {
			return c
		}
	}

	// More identifiers, all the others equal, come after.
	switch {
	case len(a.Pre) < len(b.Pre):
		return -1
	case len(a.Pre) > len(b.Pre):
		return 1
	}

	return 0
}

// Newer says whether a comes after b, false where either is no version.
func Newer(a, b string) bool {
	va, okA := Parse(a)
	vb, okB := Parse(b)

	return okA && okB && Compare(va, vb) > 0
}

// compareIdentifier orders two pre-release identifiers: numbers by value, before words,
// and words as ASCII.
func compareIdentifier(a, b string) int {
	na, errA := strconv.ParseUint(a, 10, 64)
	nb, errB := strconv.ParseUint(b, 10, 64)

	switch {
	case errA == nil && errB == nil:
		switch {
		case na < nb:
			return -1
		case na > nb:
			return 1
		}

		return 0
	case errA == nil:
		return -1
	case errB == nil:
		return 1
	}

	return strings.Compare(a, b)
}
