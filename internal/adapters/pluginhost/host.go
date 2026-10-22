// Package pluginhost runs plugins (phase 5): it starts a plugin's program through
// hashicorp/go-plugin, reads its manifest, and makes its nodes, triggers, credential types
// and scenarios the engine's and the library's (D284), the nodes and triggers as proxies
// passing items over gRPC (D286). A plugin's names are plugin.<id>.<name> (D285).
//
// It serves each plugin the host API (5b), the plugin's only way out of its process:
// each call names a session of one of its runs, is checked against the permissions its
// manifest asks for, and is held to the run's limits. The process runs under the sandbox
// (5d), where it can reach nothing else.
package pluginhost

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

const (
	// startFor is how long a plugin may take to start and describe itself.
	startFor = 30 * time.Second
	// checkFor bounds a credential's check by the plugin.
	checkFor = 10 * time.Second
)

// Plugin is a plugin the host started: its manifest, and its process while it runs.
// Supervised, it is started again when its process crashes (D320).
type Plugin struct {
	prog     Program
	manifest plugin.Manifest
	log      logging.Logger
	api      *api
	out      *ring // what its process and go-plugin last said about it

	mu      sync.Mutex
	conn    *conn // nil while it does not run
	state   State
	since   time.Time
	crashes []time.Time // within the supervision's window
	lastErr string
	closed  bool
	wake    chan struct{} // an admin's start, for a plugin stopped as crashed
	used    usage
}

// conn is a plugin's running process, and its temporary directory.
type conn struct {
	client    *goplugin.Client
	rpc       *plugin.Client
	temp      string
	pid       int
	sandboxed bool
}

// end stops the process, emptying its temporary directory (D339).
func (c *conn) end() {
	c.client.Kill()
	_ = os.RemoveAll(c.temp)
}

// Program is a plugin's program, and the directories its process may touch (D339).
type Program struct {
	ID   string // the plugin's, as its files name it, which its caps are read by
	Path string // the program
	Dir  string // its version's directory, holding it: read, never written
	Data string // its plugin's lasting directory, made when it is not there; empty: none
}

// Start starts the plugin's program and reads what it brings, refusing a manifest the
// host cannot take, and serves it the host API from deps. Its process runs under deps'
// sandbox, or without one only where there is none and deps allow it (D338). It gets none
// of the server's environment, so none of its settings or secrets: only a PATH, a
// temporary directory of its own, which it works in, emptied when it stops, and its
// lasting one.
func Start(ctx context.Context, prog Program, deps Deps) (*Plugin, error) {
	api, err := newAPI(deps)
	if err != nil {
		return nil, err
	}

	if prog.Data != "" {
		if err := os.MkdirAll(prog.Data, 0o700); err != nil {
			return nil, fmt.Errorf("plugin %s: its data: %w", prog.Path, err)
		}
	}

	p := &Plugin{prog: prog, log: api.log, api: api, out: newRing(logLines), wake: make(chan struct{}, 1)}

	c, m, err := p.connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", prog.Path, err)
	}

	if err := api.bind(m); err != nil {
		c.end()
		return nil, fmt.Errorf("plugin %s: %w", prog.Path, err)
	}

	p.manifest, p.log = m, api.log
	p.conn, p.state, p.since = c, Running, time.Now()

	return p, nil
}

// connect starts the plugin's process and reads its manifest.
func (p *Plugin) connect(ctx context.Context) (*conn, plugin.Manifest, error) {
	temp, err := os.MkdirTemp("", "polythought-plugin-")
	if err != nil {
		return nil, plugin.Manifest{}, fmt.Errorf("its temporary directory: %w", err)
	}

	caps := p.caps()

	data := Usage(p.prog.Data)

	p.mu.Lock()
	p.used = usage{data: data, caps: caps}
	p.mu.Unlock()

	// Starting it again would not take its data back under its cap (D341).
	if caps.Data > 0 && data > caps.Data {
		_ = os.RemoveAll(temp)
		return nil, plugin.Manifest{}, stopped{fmt.Errorf("its data takes %s, past its cap of %s", mib(data), mib(caps.Data))}
	}

	cmd, err := p.command(temp, caps)
	if err != nil {
		_ = os.RemoveAll(temp)
		return nil, plugin.Manifest{}, err
	}

	client := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins:         plugin.HostPlugins(p.api),
		Cmd:             cmd,
		SkipHostEnv:     true,
		// The host API over the connection to the plugin's socket, in its temporary
		// directory: the host makes no socket of its own, which the sandbox would keep
		// the plugin from.
		GRPCBrokerMultiplex: true,
		AllowedProtocols:    []goplugin.Protocol{goplugin.ProtocolGRPC},
		AutoMTLS:            true,
		StartTimeout:        startFor,
		Logger: hclog.New(&hclog.LoggerOptions{
			Name: "plugin", Level: hclog.Info, Output: &lines{log: p.log, out: p.out}, DisableTime: true,
		}),
	})

	c := &conn{client: client, temp: temp, sandboxed: p.api.deps.Sandbox != nil}

	fail := func(err error) (*conn, plugin.Manifest, error) {
		c.end()
		return nil, plugin.Manifest{}, err
	}

	cc, err := client.Client()
	if err != nil {
		return fail(fmt.Errorf("start: %w", err))
	}

	c.pid = cmd.Process.Pid

	raw, err := cc.Dispense(plugin.Name)
	if err != nil {
		return fail(err)
	}

	rpc, ok := raw.(*plugin.Client)
	if !ok {
		return fail(fmt.Errorf("it serves %T, not the plugin contract", raw))
	}

	ctx, cancel := context.WithTimeout(ctx, startFor)
	defer cancel()

	described, err := rpc.Describe(ctx, &pluginv1.DescribeRequest{Host: rpc.HostID})
	if err != nil {
		return fail(fmt.Errorf("describe: %w", err))
	}

	var m plugin.Manifest

	dec := json.NewDecoder(bytes.NewReader(described.GetManifest()))
	dec.DisallowUnknownFields()
	dec.UseNumber()

	if err := dec.Decode(&m); err != nil {
		return fail(fmt.Errorf("its manifest: %w", err))
	}

	if err := Check(m); err != nil {
		return fail(err)
	}

	c.rpc = rpc

	return c, m, nil
}

// command is the command starting the plugin's program: under the sandbox, or where there
// is none, without one if deps allow it, asked each time (D338).
func (p *Plugin) command(temp string, caps Caps) (*exec.Cmd, error) {
	var (
		cmd  *exec.Cmd
		deps = p.api.deps
	)

	switch {
	case deps.Sandbox != nil:
		c, err := deps.Sandbox.Command(sandbox.Policy{Program: p.prog.Path, Dir: p.prog.Dir, Temp: temp, Data: p.prog.Data})
		if err != nil {
			return nil, err
		}

		cmd = c
	case deps.Unsandboxed != nil && deps.Unsandboxed():
		cmd = exec.Command(p.prog.Path) //nolint:gosec // an admin's plugin, approved by them (5c), and let run unsandboxed
	default:
		return nil, fmt.Errorf("%w, and unsandboxed plugins are not allowed", cmp.Or(deps.NoSandbox, sandbox.ErrUnavailable))
	}

	cmd.Dir = temp
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "TMPDIR=" + temp, "HOME=" + cmp.Or(p.prog.Data, temp)}
	if p.prog.Data != "" {
		cmd.Env = append(cmd.Env, plugin.EnvData+"="+p.prog.Data)
	}

	if caps.Memory > 0 {
		// A soft limit a little under its cap, which a Go plugin's collector keeps to (D345).
		cmd.Env = append(cmd.Env, "GOMEMLIMIT="+strconv.FormatInt(caps.Memory/10*9, 10))
	}

	return cmd, nil
}

// caps are what the plugin may take, as deps say now.
func (p *Plugin) caps() Caps {
	if p.api.deps.Caps == nil {
		return DefaultCaps
	}

	return p.api.deps.Caps(p.prog.ID)
}

func (p *Plugin) Manifest() plugin.Manifest { return p.manifest }

// Exited reports whether its process has ended, crashed or stopped, and not been started
// again.
func (p *Plugin) Exited() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.conn == nil || p.conn.client.Exited()
}

// Close stops its process, for good.
func (p *Plugin) Close() {
	p.mu.Lock()
	c := p.conn
	p.conn, p.closed, p.state, p.since = nil, true, Stopped, time.Now()
	p.mu.Unlock()

	if c != nil {
		c.end()
	}

	p.api.http.CloseIdleConnections()
}

// rpc is its process's client, while it runs.
func (p *Plugin) rpc() (pluginv1.PluginServiceClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch {
	case p.conn == nil:
		return nil, fmt.Errorf("plugin %s is not running: %s", p.manifest.ID, p.state)
	case p.conn.client.Exited():
		return nil, fmt.Errorf("plugin %s is not running: its process exited", p.manifest.ID)
	}

	return p.conn.rpc, nil
}

// Register makes the plugin's credential types, nodes and triggers the registry's: the
// credential types first, as its nodes may ask for them.
func (p *Plugin) Register(r *engine.Registry) error {
	for _, c := range p.manifest.CredentialTypes {
		if err := r.RegisterCredentialType(p.credentialType(c)); err != nil {
			return fmt.Errorf("plugin %s: %w", p.manifest.ID, err)
		}
	}

	for _, n := range p.manifest.Nodes {
		typ, err := nodeType(p.manifest.ID, n, false)
		if err != nil {
			return fmt.Errorf("plugin %s: node %s: %w", p.manifest.ID, n.Name, err)
		}

		typ.PluginName = p.manifest.Name

		if err := r.RegisterNode(&proxyNode{p: p, name: n.Name, typ: typ, trades: n.Trades}); err != nil {
			return fmt.Errorf("plugin %s: %w", p.manifest.ID, err)
		}
	}

	for _, t := range p.manifest.Triggers {
		typ, err := nodeType(p.manifest.ID, t, true)
		if err != nil {
			return fmt.Errorf("plugin %s: trigger %s: %w", p.manifest.ID, t.Name, err)
		}

		typ.PluginName = p.manifest.Name

		if err := r.RegisterTrigger(&proxyTrigger{p: p, name: t.Name, typ: typ}); err != nil {
			return fmt.Errorf("plugin %s: %w", p.manifest.ID, err)
		}
	}

	return nil
}

// Scenarios are the plugin's scenarios as the library's templates, read as built-in ones
// are, their IDs plugin.<id>.<scenario>.
func (p *Plugin) Scenarios() ([]domain.Template, error) {
	out := make([]domain.Template, 0, len(p.manifest.Scenarios))

	for _, sc := range p.manifest.Scenarios {
		raw, err := json.Marshal(sc)
		if err != nil {
			return nil, err
		}

		t, err := library.ParseScenario(raw)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: scenario %s: %w", p.manifest.ID, sc.ID, err)
		}

		t.ID = domain.TemplateID(qualified(p.manifest.ID, sc.ID))
		out = append(out, t)
	}

	return out, nil
}

// credentialType is one of the plugin's credential types as the engine's, its checks the
// plugin's.
func (p *Plugin) credentialType(c plugin.CredentialType) engine.CredentialType {
	t := engine.CredentialType{Name: qualified(p.manifest.ID, c.Name), Label: c.Label, Description: c.Description}

	for _, f := range c.Fields {
		t.Fields = append(t.Fields, engine.CredentialField{
			Name: f.Name, Label: f.Label, Description: f.Description, Required: f.Required, Secret: f.Secret,
		})
	}

	cred := func(values map[string]string) *pluginv1.Credential {
		return &pluginv1.Credential{Type: c.Name, Values: values}
	}

	t.Check = func(values map[string]string) error {
		ctx, cancel := context.WithTimeout(context.Background(), checkFor)
		defer cancel()

		rpc, err := p.rpc()
		if err != nil {
			return err
		}

		res, err := rpc.CheckCredential(ctx, &pluginv1.CheckCredentialRequest{Credential: cred(values)})
		if err != nil {
			return fmt.Errorf("plugin %s did not check it: %w", p.manifest.ID, callError(err))
		}

		return errorOf(res.GetError())
	}

	if c.Testable {
		t.Test = func(ctx context.Context, values map[string]string) error {
			rpc, err := p.rpc()
			if err != nil {
				return err
			}

			res, err := rpc.TestCredential(ctx, &pluginv1.TestCredentialRequest{Credential: cred(values)})
			if err != nil {
				return fmt.Errorf("plugin %s did not test it: %w", p.manifest.ID, callError(err))
			}

			return errorOf(res.GetError())
		}
	}

	return t
}

// lines writes what go-plugin logs, the plugin's stderr among it, to the server's log,
// keeping the last lines as the plugin's own log.
type lines struct {
	log logging.Logger
	out *ring
}

func (l *lines) Write(b []byte) (int, error) {
	for line := range strings.SplitSeq(strings.TrimRight(string(b), "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			l.log.Info(context.Background(), "Plugin says", "line", line)
			l.out.add(line)
		}
	}

	return len(b), nil
}
