package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/semver"
)

// versionVar is the variable the Compose file names its images' version by.
const versionVar = "POLYTHOUGHT_VERSION"

// Apply is an update, run in a container of its own that sees the stack's files where
// they are: it pulls the services' images at Version, keeps it in the env file, and
// recreates them, nothing else. Then the updater's own, Self, as it can: its update
// outlives it, and it failing leaves the services updated.
type Apply struct {
	Run        Runner
	Version    string
	Project    string
	ProjectDir string
	Files      []string
	EnvFiles   []string
	Services   []string
	Self       string
}

func (a Apply) Do(ctx context.Context, out io.Writer) error {
	switch {
	case !semver.Valid(a.Version):
		return fmt.Errorf("%q is no version, such as 0.1.2", a.Version)
	case a.Project == "" || a.ProjectDir == "" || len(a.Files) == 0 || len(a.EnvFiles) == 0 || len(a.Services) == 0:
		return errors.New("an update needs its project, project directory, files, env files and services")
	}

	compose := []string{"compose", "--project-name", a.Project, "--project-directory", a.ProjectDir}
	for _, f := range a.Files {
		compose = append(compose, "--file", f)
	}

	for _, f := range a.EnvFiles {
		compose = append(compose, "--env-file", f)
	}

	// Over what the env files say, until the last of them says it too.
	env := []string{versionVar + "=" + a.Version}

	_, _ = fmt.Fprintf(out, "Pulling %s at %s\n", strings.Join(a.Services, ", "), a.Version)

	if err := a.Run.Run(ctx, out, env, slices.Concat(compose, []string{"pull", "--quiet"}, a.Services)...); err != nil {
		return fmt.Errorf("pulling the images: %w", err)
	}

	// The last env file wins, so a docker compose up after this stays on the version.
	last := a.EnvFiles[len(a.EnvFiles)-1]
	if err := setVersionIn(last, a.Version); err != nil {
		return fmt.Errorf("keeping the version in %s: %w", last, err)
	}

	_, _ = fmt.Fprintf(out, "%s=%s kept in %s; starting %s\n", versionVar, a.Version, last, strings.Join(a.Services, ", "))

	if err := a.Run.Run(ctx, out, env, slices.Concat(compose, []string{"up", "--detach", "--no-deps"}, a.Services)...); err != nil {
		return fmt.Errorf("starting the new containers: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Updated to %s\n", a.Version)

	if a.Self == "" {
		return nil
	}

	if err := a.Run.Run(ctx, out, env, slices.Concat(compose, []string{"pull", "--quiet", a.Self})...); err != nil {
		_, _ = fmt.Fprintf(out, "The updater stays as it is: its image at %s did not pull: %v\n", a.Version, err)
		return nil
	}

	if err := a.Run.Run(ctx, out, env, slices.Concat(compose, []string{"up", "--detach", "--no-deps", a.Self})...); err != nil {
		_, _ = fmt.Fprintf(out, "The updater stays as it is: %v\n", err)
		return nil
	}

	_, _ = fmt.Fprintf(out, "The updater is at %s too\n", a.Version)

	return nil
}

// setVersionIn sets the version in an env file, writing it in place: a file Docker
// mounts cannot be replaced.
func setVersionIn(path, version string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return os.WriteFile(path, []byte(SetVersion(string(b), version)), 0o600)
}

// SetVersion is env, an env file, with POLYTHOUGHT_VERSION set to version: each line
// setting it changed, or one added at the end; comments and the rest as they were.
func SetVersion(env, version string) string {
	lines := strings.SplitAfter(env, "\n")
	set := false

	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(body)
		export, rest := "", trimmed

		if r, ok := strings.CutPrefix(trimmed, "export "); ok {
			export, rest = "export ", strings.TrimSpace(r)
		}

		name, _, ok := strings.Cut(rest, "=")
		if !ok || strings.TrimSpace(name) != versionVar {
			continue
		}

		lines[i] = export + versionVar + "='" + version + "'" + line[len(body):]
		set = true
	}

	out := strings.Join(lines, "")
	if set {
		return out
	}

	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}

	return out + "\n# The version Polythought's updater last updated to.\n" + versionVar + "='" + version + "'\n"
}
