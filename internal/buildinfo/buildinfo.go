// Package buildinfo is what the binary knows of its own build (D425): the version its
// release was tagged, and the commit it was built from. A release's build stamps both, as
// -ldflags "-X github.com/lein3000zzz/polythought-go/internal/buildinfo.version=0.1.2
// -X github.com/lein3000zzz/polythought-go/internal/buildinfo.commit=<sha>"; any other
// build is dev, its commit what Go recorded of the checkout, if it did.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Dev is the version of a build that no release stamped.
const Dev = "dev"

// Set at link time by a release's build; see the package doc.
var (
	version = Dev
	commit  = ""
)

// Info is the build's own: Version as its release was tagged, without the v (0.1.2), or
// Dev; Commit the full hash it was built from, empty where unknown; Modified whether the
// checkout had changes not committed, as far as Go recorded; Go the toolchain.
type Info struct {
	Version  string
	Commit   string
	Modified bool
	Go       string
}

// Get is the build's Info.
func Get() Info {
	in := Info{Version: strings.TrimPrefix(version, "v"), Commit: commit, Go: runtime.Version()}
	if in.Version == "" {
		in.Version = Dev
	}

	if in.Commit != "" {
		return in
	}

	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				in.Commit = s.Value
			case "vcs.modified":
				in.Modified = s.Value == "true"
			}
		}
	}

	return in
}

// Release is whether a release stamped the build.
func (i Info) Release() bool { return i.Version != Dev }

// Short is the commit's first 7 characters, as git shows it; empty where unknown.
func (i Info) Short() string {
	if len(i.Commit) > 7 {
		return i.Commit[:7]
	}

	return i.Commit
}

// String is the version as people read it: 0.1.2 (abc1234), or dev (abc1234, modified).
func (i Info) String() string {
	var notes []string
	if s := i.Short(); s != "" {
		notes = append(notes, s)
	}

	if i.Modified {
		notes = append(notes, "modified")
	}

	if len(notes) == 0 {
		return i.Version
	}

	return i.Version + " (" + strings.Join(notes, ", ") + ")"
}
