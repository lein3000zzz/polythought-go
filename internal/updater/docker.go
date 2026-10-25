package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// The labels Docker Compose gives each container it starts: how it was started, with host
// paths.
const (
	labelProject     = "com.docker.compose.project"
	labelService     = "com.docker.compose.service"
	labelConfigFiles = "com.docker.compose.project.config_files"
	labelWorkingDir  = "com.docker.compose.project.working_dir"
	labelEnvFile     = "com.docker.compose.project.environment_file"
)

// socket is where the Docker socket is, inside the updater and inside its update.
const socket = "/var/run/docker.sock"

// Runner runs the docker CLI.
type Runner interface {
	// Output runs it and returns what it printed, or an error with what it said.
	Output(ctx context.Context, args ...string) ([]byte, error)
	// Run runs it with env added to its environment, writing what it prints to out.
	Run(ctx context.Context, out io.Writer, env []string, args ...string) error
}

// CLI runs the docker CLI on the PATH. Arguments go to it as they are: no shell reads them.
type CLI struct{}

func (CLI) Output(ctx context.Context, args ...string) ([]byte, error) {
	var stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}

	return out, nil
}

func (CLI) Run(ctx context.Context, out io.Writer, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.Env = append(cmd.Environ(), env...)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker %s: %w", args[0], err)
	}

	return nil
}

// Docker updates a Compose stack the updater runs in: its update is a container of the
// updater's image, as Apply.
type Docker struct {
	Run Runner
	// Self is the updater's own container, which Docker names its hostname after.
	Self string
	// Services are the stack's services to update, such as api and web.
	Services []string
	// Project is the stack's Compose project; empty for the updater's own.
	Project string
	// Image runs the update; empty for the updater's own.
	Image string
	// Binary is the updater's path inside its image.
	Binary string
}

type container struct {
	Image  string `json:"Image"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Mounts []struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	} `json:"Mounts"`
}

func (d Docker) inspect(ctx context.Context, id string) (container, error) {
	out, err := d.Run.Output(ctx, "inspect", "--type", "container", id)
	if err != nil {
		return container{}, err
	}

	var list []container
	if err := json.Unmarshal(out, &list); err != nil || len(list) != 1 {
		return container{}, fmt.Errorf("docker inspect %s: not one container", id)
	}

	return list[0], nil
}

// Update runs an update to version in a container of its own, which outlives the updater
// should it stop.
func (d Docker) Update(ctx context.Context, version string, out io.Writer) error {
	args, services, err := d.updateArgs(ctx, version)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Updating %s to %s\n", strings.Join(services, ", "), version)

	return d.Run.Run(ctx, out, nil, args...)
}

// updateArgs are docker's for the update: the stack's files and env file mounted at the
// paths it was started with, so what they name relative to them is found as it was.
func (d Docker) updateArgs(ctx context.Context, version string) (args, services []string, err error) {
	if len(d.Services) == 0 {
		return nil, nil, errors.New("no services to update")
	}

	self, err := d.inspect(ctx, d.Self)
	if err != nil {
		return nil, nil, fmt.Errorf("the updater's own container: %w", err)
	}

	project := d.Project
	if project == "" {
		project = self.Config.Labels[labelProject]
	}

	if project == "" {
		return nil, nil, errors.New("the updater runs in no Compose project: set POLYTHOUGHT_UPDATER_PROJECT")
	}

	ids, err := d.Run.Output(ctx, "ps", "--all", "--quiet", "--filter", "label="+labelProject+"="+project,
		"--filter", "label="+labelService+"="+d.Services[0])
	if err != nil {
		return nil, nil, err
	}

	first, _, _ := strings.Cut(strings.TrimSpace(string(ids)), "\n")
	if first == "" {
		return nil, nil, fmt.Errorf("the project %s has no %s container", project, d.Services[0])
	}

	target, err := d.inspect(ctx, first)
	if err != nil {
		return nil, nil, err
	}

	how := target.Config.Labels
	files, envFiles, workDir := splitList(how[labelConfigFiles]), splitList(how[labelEnvFile]), how[labelWorkingDir]

	switch {
	case len(files) == 0 || workDir == "":
		return nil, nil, fmt.Errorf("the %s container does not say how Compose started it", d.Services[0])
	case len(envFiles) == 0:
		return nil, nil, errors.New("the stack was started without an env file (--env-file): the updater keeps the version there")
	}

	sock := ""
	for _, m := range self.Mounts {
		if m.Destination == socket {
			sock = m.Source
		}
	}

	if sock == "" {
		return nil, nil, errors.New("the updater has no Docker socket at " + socket)
	}

	image := d.Image
	if image == "" {
		image = self.Image
	}

	// The updater is updated after them, where it is of the stack: its update outlives it.
	services = slices.Clone(d.Services)

	own := ""
	if svc := self.Config.Labels[labelService]; svc != "" && self.Config.Labels[labelProject] == project && !slices.Contains(services, svc) {
		own = svc
	}

	dirs := []string{workDir}
	for _, p := range append(slices.Clone(files), envFiles...) {
		dirs = append(dirs, filepath.Dir(p))
	}

	for _, d := range append(dirs, envFiles...) {
		switch {
		case !filepath.IsAbs(d) || d == "/":
			return nil, nil, fmt.Errorf("compose named %s: the updater mounts the directories of its files, and / is none it can", d)
		case strings.ContainsAny(d, ":,"):
			return nil, nil, fmt.Errorf("compose named %s: the updater mounts no path with a comma or a colon", d)
		}
	}

	// No network: Docker pulls, not the CLI, which talks to it through the socket. Mounts
	// spelled out, as -v's short form can misread a path.
	args = []string{
		"run", "--rm", "--name", project + "-update", "--network", "none", "--label", "polythought.update=" + version,
		"--mount", "type=bind,source=" + sock + ",target=" + socket, "--workdir", workDir, "--entrypoint", d.Binary,
	}

	for _, dir := range mountDirs(dirs) {
		args = append(args, "--mount", "type=bind,source="+dir+",target="+dir+",readonly")
	}

	for _, f := range envFiles {
		args = append(args, "--mount", "type=bind,source="+f+",target="+f) // the version goes in
	}

	args = append(args, image, "apply", "--version", version, "--project", project, "--project-directory", workDir)
	for _, f := range files {
		args = append(args, "--file", f)
	}

	for _, f := range envFiles {
		args = append(args, "--env-file", f)
	}

	for _, s := range services {
		args = append(args, "--service", s)
	}

	if own != "" {
		args = append(args, "--self", own)
		services = append(services, own)
	}

	return args, services, nil
}

// splitList reads a label Compose lists paths in, comma-separated.
func splitList(label string) []string {
	var out []string

	for p := range strings.SplitSeq(label, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}

// mountDirs are dirs to mount, those inside another left out.
func mountDirs(dirs []string) []string {
	dirs = slices.Clone(dirs)
	for i, d := range dirs {
		dirs[i] = filepath.Clean(d)
	}

	slices.Sort(dirs)
	dirs = slices.Compact(dirs)

	var out []string

	for _, d := range dirs {
		if !slices.ContainsFunc(out, func(o string) bool { return strings.HasPrefix(d, o+"/") }) {
			out = append(out, d)
		}
	}

	return out
}
