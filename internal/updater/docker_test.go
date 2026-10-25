package updater

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDocker answers docker's commands from what it was given, and keeps what it ran.
type fakeDocker struct {
	outputs map[string]string // by the command's words, joined by spaces
	ran     [][]string
	env     [][]string
	fail    map[string]error // Run by its first words
}

func (f *fakeDocker) Output(_ context.Context, args ...string) ([]byte, error) {
	out, ok := f.outputs[strings.Join(args, " ")]
	if !ok {
		return nil, fmt.Errorf("docker %s: no such container", args[0])
	}

	return []byte(out), nil
}

func (f *fakeDocker) Run(_ context.Context, out io.Writer, env []string, args ...string) error {
	f.ran = append(f.ran, args)
	f.env = append(f.env, env)
	_, _ = fmt.Fprintln(out, "ran", args[len(args)-1])

	for prefix, err := range f.fail {
		if strings.HasPrefix(strings.Join(args, " "), prefix) {
			return err
		}
	}

	return nil
}

const (
	selfJSON = `[{"Image":"sha256:updater","Config":{"Labels":{"com.docker.compose.project":"poly","com.docker.compose.service":"updater"}},
		"Mounts":[{"Source":"/srv/poly","Destination":"/polythought"},{"Source":"/run/docker.sock","Destination":"/var/run/docker.sock"}]}]`
	apiJSON = `[{"Image":"sha256:api","Config":{"Labels":{"com.docker.compose.project":"poly","com.docker.compose.service":"api",
		"com.docker.compose.project.config_files":"/srv/poly/deployments/docker-compose.yml,/etc/poly/demo.yml",
		"com.docker.compose.project.working_dir":"/srv/poly/deployments",
		"com.docker.compose.project.environment_file":"/srv/poly/.env"}},"Mounts":[]}]`
	psArgs = "ps --all --quiet --filter label=com.docker.compose.project=poly --filter label=com.docker.compose.service=api"
)

func stack() *fakeDocker {
	return &fakeDocker{outputs: map[string]string{
		"inspect --type container abc123": selfJSON,
		psArgs:                            "def456\n",
		"inspect --type container def456": apiJSON,
	}}
}

func TestDockerUpdate(t *testing.T) {
	f := stack()
	d := Docker{Run: f, Self: "abc123", Services: []string{"api", "web"}, Binary: "/usr/local/bin/polythought-updater"}

	var out strings.Builder
	require.NoError(t, d.Update(context.Background(), "0.0.2", &out))
	require.Len(t, f.ran, 1)

	assert.Equal(t, []string{
		"run", "--rm", "--name", "poly-update", "--network", "none", "--label", "polythought.update=0.0.2",
		"--mount", "type=bind,source=/run/docker.sock,target=/var/run/docker.sock", "--workdir", "/srv/poly/deployments",
		"--entrypoint", "/usr/local/bin/polythought-updater",
		// each directory once, those inside another in it
		"--mount", "type=bind,source=/etc/poly,target=/etc/poly,readonly",
		"--mount", "type=bind,source=/srv/poly,target=/srv/poly,readonly",
		"--mount", "type=bind,source=/srv/poly/.env,target=/srv/poly/.env",
		"sha256:updater", "apply", "--version", "0.0.2", "--project", "poly", "--project-directory", "/srv/poly/deployments",
		"--file", "/srv/poly/deployments/docker-compose.yml", "--file", "/etc/poly/demo.yml",
		"--env-file", "/srv/poly/.env", "--service", "api", "--service", "web", "--self", "updater",
	}, f.ran[0], "as the stack was started, with the paths it was, the updater after")
	assert.Equal(t, "Updating api, web, updater to 0.0.2\nran updater\n", out.String())

	t.Run("its project and image as set", func(t *testing.T) {
		f := stack()
		f.outputs[strings.ReplaceAll(psArgs, "=poly ", "=other ")] = "def456\n"
		d := Docker{Run: f, Self: "abc123", Services: []string{"api"}, Project: "other", Image: "ghcr.io/x/updater:1", Binary: "/u"}

		require.NoError(t, d.Update(context.Background(), "0.0.2", io.Discard))
		assert.Contains(t, strings.Join(f.ran[0], " "), "--name other-update")
		assert.Contains(t, strings.Join(f.ran[0], " "), "ghcr.io/x/updater:1 apply")
		assert.True(t, strings.HasSuffix(strings.Join(f.ran[0], " "), "--service api"), "not the updater of another project")
	})
}

func TestDockerUpdateRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(f *fakeDocker)
		want   string
	}{
		"not in a container": {func(f *fakeDocker) { delete(f.outputs, "inspect --type container abc123") }, "the updater's own container"},
		"no project": {func(f *fakeDocker) {
			f.outputs["inspect --type container abc123"] = strings.Replace(selfJSON, `"com.docker.compose.project":"poly"`, `"x":"y"`, 1)
		}, "set POLYTHOUGHT_UPDATER_PROJECT"},
		"no api": {func(f *fakeDocker) { f.outputs[psArgs] = "" }, "the project poly has no api container"},
		"no env file": {func(f *fakeDocker) {
			f.outputs["inspect --type container def456"] = strings.Replace(apiJSON, "/srv/poly/.env", "", 1)
		}, "started without an env file"},
		"no socket": {func(f *fakeDocker) {
			f.outputs["inspect --type container abc123"] = strings.Replace(selfJSON, "/var/run/docker.sock", "/elsewhere", 1)
		}, "no Docker socket"},
		"files at the root": {func(f *fakeDocker) {
			f.outputs["inspect --type container def456"] = strings.Replace(apiJSON, "/etc/poly/demo.yml", "/demo.yml", 1)
		}, "/ is none it can"},
		"a colon": {func(f *fakeDocker) {
			f.outputs["inspect --type container def456"] = strings.Replace(apiJSON, "/srv/poly/.env", "/srv/poly/x:/etc/.env", 1)
		}, "no path with a comma or a colon"},
		"relative": {func(f *fakeDocker) {
			f.outputs["inspect --type container def456"] = strings.Replace(apiJSON, "/etc/poly/demo.yml", "demo.yml", 1)
		}, "compose named ."},
	} {
		t.Run(name, func(t *testing.T) {
			f := stack()
			tc.change(f)

			err := Docker{Run: f, Self: "abc123", Services: []string{"api", "web"}, Binary: "/u"}.Update(context.Background(), "0.0.2", io.Discard)
			assert.ErrorContains(t, err, tc.want)
			assert.Empty(t, f.ran, "nothing run")
		})
	}
}

func TestApply(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(envFile, []byte("POLYTHOUGHT_SECRET_KEYS='k1:x'\n# POLYTHOUGHT_VERSION='latest'\n"), 0o600))

	a := Apply{
		Version: "0.0.2", Project: "poly", ProjectDir: "/srv/poly/deployments", Files: []string{"/srv/poly/deployments/docker-compose.yml"},
		EnvFiles: []string{"/srv/poly/base.env", envFile}, Services: []string{"api", "web"},
	}

	t.Run("pulls, keeps the version, starts", func(t *testing.T) {
		f := &fakeDocker{}
		a.Run = f

		var out strings.Builder
		require.NoError(t, a.Do(context.Background(), &out))

		compose := "compose --project-name poly --project-directory /srv/poly/deployments --file /srv/poly/deployments/docker-compose.yml " +
			"--env-file /srv/poly/base.env --env-file " + envFile
		require.Len(t, f.ran, 2)
		assert.Equal(t, compose+" pull --quiet api web", strings.Join(f.ran[0], " "))
		assert.Equal(t, compose+" up --detach --no-deps api web", strings.Join(f.ran[1], " "), "nothing but the services")
		assert.Equal(t, [][]string{{"POLYTHOUGHT_VERSION=0.0.2"}, {"POLYTHOUGHT_VERSION=0.0.2"}}, f.env)

		b, err := os.ReadFile(envFile)
		require.NoError(t, err)
		assert.Equal(t, "POLYTHOUGHT_SECRET_KEYS='k1:x'\n# POLYTHOUGHT_VERSION='latest'\n\n"+
			"# The version Polythought's updater last updated to.\nPOLYTHOUGHT_VERSION='0.0.2'\n", string(b), "in the last env file")
		assert.Contains(t, out.String(), "Updated to 0.0.2")
	})

	t.Run("the updater after, as it can", func(t *testing.T) {
		f := &fakeDocker{fail: map[string]error{"compose --project-name poly --project-directory /srv/poly/deployments --file " +
			"/srv/poly/deployments/docker-compose.yml --env-file /srv/poly/base.env --env-file " + envFile + " pull --quiet updater": errors.New("exit status 1")}}
		self := a
		self.Run, self.Version, self.Self = f, "0.0.3", "updater"

		var out strings.Builder
		require.NoError(t, self.Do(context.Background(), &out), "the services are updated")
		assert.Len(t, f.ran, 3, "not started again")
		assert.Contains(t, out.String(), "The updater stays as it is: its image at 0.0.3 did not pull: exit status 1")

		f.fail = nil
		f.ran = nil
		require.NoError(t, self.Do(context.Background(), &out))
		require.Len(t, f.ran, 4)
		assert.Equal(t, []string{"up", "--detach", "--no-deps", "updater"}, f.ran[3][len(f.ran[3])-4:])
	})

	t.Run("a failed pull changes nothing", func(t *testing.T) {
		before, err := os.ReadFile(envFile)
		require.NoError(t, err)

		f := &fakeDocker{fail: map[string]error{"compose": errors.New("docker compose: exit status 1")}}
		a.Run, a.Version = f, "0.0.3"

		require.ErrorContains(t, a.Do(context.Background(), io.Discard), "pulling the images")
		assert.Len(t, f.ran, 1, "not started")

		after, err := os.ReadFile(envFile)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})

	t.Run("no version, nothing run", func(t *testing.T) {
		f := &fakeDocker{}
		a.Run, a.Version = f, "0.0.3 --pull always"

		require.ErrorContains(t, a.Do(context.Background(), io.Discard), "is no version")
		assert.Empty(t, f.ran)
	})
}

func TestSetVersion(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"set":                    {"A=1\nPOLYTHOUGHT_VERSION='0.0.1'\nB=2\n", "A=1\nPOLYTHOUGHT_VERSION='0.0.2'\nB=2\n"},
		"spaced, unquoted, CRLF": {"A=1\r\n  POLYTHOUGHT_VERSION = 0.0 \r\n", "A=1\r\nPOLYTHOUGHT_VERSION='0.0.2'\r\n"},
		"exported":               {"export POLYTHOUGHT_VERSION=latest", "export POLYTHOUGHT_VERSION='0.0.2'"},
		"each time it is set":    {"POLYTHOUGHT_VERSION=1\nPOLYTHOUGHT_VERSION=2\n", "POLYTHOUGHT_VERSION='0.0.2'\nPOLYTHOUGHT_VERSION='0.0.2'\n"},
		"another variable":       {"POLYTHOUGHT_VERSIONS=1\n", "POLYTHOUGHT_VERSIONS=1\n\n# The version Polythought's updater last updated to.\nPOLYTHOUGHT_VERSION='0.0.2'\n"},
		"no newline at the end":  {"A=1", "A=1\n\n# The version Polythought's updater last updated to.\nPOLYTHOUGHT_VERSION='0.0.2'\n"},
		"empty":                  {"", "\n# The version Polythought's updater last updated to.\nPOLYTHOUGHT_VERSION='0.0.2'\n"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, SetVersion(tc.in, "0.0.2"))
		})
	}
}
