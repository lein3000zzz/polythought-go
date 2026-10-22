package pluginhost_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/adapters/sandbox"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// echo and bare are the test plugins' programs, built once for the package, each in a
// directory of its own, as a version's is.
var echo, bare string

// launcher is the sandbox, where the kernel has one: the test plugins run under it there,
// as the server's do, and without one elsewhere.
var launcher *sandbox.Launcher

func TestMain(m *testing.M) {
	// This test binary is the sandbox's launcher too, as the server's is.
	sandbox.Launch()

	launcher, _ = sandbox.New()

	dir, err := os.MkdirTemp("", "pluginhost-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	echo, bare = filepath.Join(dir, "echo", "plugin"), filepath.Join(dir, "bare", "plugin")

	for path, src := range map[string]string{echo: "./testdata/echo", bare: "./testdata/bare"} {
		build := exec.Command("go", "build", "-o", path, src)
		build.Env = append(os.Environ(), "CGO_ENABLED=0") // as pluginpack builds them: the sandbox has no libc to give

		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build the test plugin %s: %v\n%s", src, err, out)
			os.Exit(1)
		}
	}

	code := m.Run()

	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func start(t *testing.T) (*pluginhost.Plugin, *engine.Registry) {
	t.Helper()

	return startWith(t, pluginhost.Deps{})
}

// program is a test plugin's program, with a lasting directory of its own.
func program(t *testing.T, path string) pluginhost.Program {
	t.Helper()

	return pluginhost.Program{Path: path, Dir: filepath.Dir(path), Data: t.TempDir()}
}

// sandboxed is deps starting plugins under the sandbox, where there is one, and else
// without.
func sandboxed(deps pluginhost.Deps) pluginhost.Deps {
	if launcher != nil {
		deps.Sandbox = launcher
	} else {
		deps.Unsandboxed = func() bool { return true }
	}

	return deps
}

// startWith starts the test plugin, serving it the host API from deps.
func startWith(t *testing.T, deps pluginhost.Deps) (*pluginhost.Plugin, *engine.Registry) {
	t.Helper()

	// The server's secrets, which the plugin's process must not see.
	t.Setenv("POLYTHOUGHT_SECRET_KEYS", "k1:server-secret")

	p, err := pluginhost.Start(context.Background(), program(t, echo), sandboxed(deps))
	require.NoError(t, err)
	t.Cleanup(p.Close)

	r := engine.NewRegistry()
	require.NoError(t, r.RegisterCredentialType(credtypes.HTTPHeaderType()), "a built-in type its node takes")
	require.NoError(t, r.RegisterTrigger(triggers.Manual{}), "a built-in its scenario starts with")
	require.NoError(t, p.Register(r))

	return p, r
}

func items(js ...map[string]any) engine.Ports {
	in := engine.Ports{}
	for _, j := range js {
		in.Add(engine.DefaultPort, engine.Item{JSON: j})
	}

	return in
}

func run(t *testing.T, r *engine.Registry, name string, params map[string]any, in engine.Ports) (engine.Ports, error) {
	t.Helper()

	n, ok := r.Node(name)
	require.True(t, ok, name)

	return runWith(context.Background(), n, engine.LiteralParams(n.Type(), params), in)
}

// runWith runs a node in ctx, as alice's, its settings from params.
func runWith(ctx context.Context, n engine.Node, params engine.ParamResolver, in engine.Ports) (engine.Ports, error) {
	return n.Run(ctx, engine.RunContext{
		ExecutionID: "ex-1", WorkflowID: "wf-1", OwnerID: "alice", NodeID: "n-1", StartedAt: time.Now(), Params: params,
	}, in)
}

func TestStart(t *testing.T) {
	p, r := start(t)

	m := p.Manifest()
	assert.Equal(t, "echo", m.ID)
	assert.Equal(t, "1.2.0", m.Version)
	assert.Equal(t, plugin.SDKVersion, m.SDK, "set by Serve")

	typ, trigger, ok := r.Type("plugin.echo.shout")
	require.True(t, ok, "named plugin.<id>.<node> (D285)")
	assert.False(t, trigger)
	assert.Equal(t, []string{engine.DefaultPort}, typ.Inputs, "main, unless it says")
	assert.Equal(t, "Shout", typ.Label)
	assert.Equal(t, []engine.Field{{Name: "shouted", Kind: engine.FieldText}}, typ.Items.Fields)

	_, trigger, ok = r.Type("plugin.echo.tick")
	assert.True(t, ok && trigger)

	keyed, _, _ := r.Type("plugin.echo.keyed")
	assert.Equal(t, []string{"plugin.echo.token", "http.header"}, keyed.Params[0].CredentialTypes, "its own, and a built-in by its full name")

	assert.False(t, p.Exited())
}

func TestNodes(t *testing.T) {
	_, r := start(t)

	t.Run("an item at a time, its settings and the run with it", func(t *testing.T) {
		out, err := run(t, r, "plugin.echo.shout", map[string]any{"field": "question"},
			items(map[string]any{"question": "will it rain?", "price": json.Number("0.42")}, map[string]any{"other": "x"}))
		require.NoError(t, err)

		require.Len(t, out[engine.DefaultPort], 1)
		got := out[engine.DefaultPort][0]
		assert.Equal(t, "WILL IT RAIN?", got.JSON["shouted"])
		assert.Equal(t, "alice", got.JSON["owner"], "it acts for the workflow's owner")
		assert.Equal(t, json.Number("0.42"), got.JSON["price"], "numbers exact, there and back")
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 0}}, got.Source)

		require.Len(t, out[engine.ErrorPort], 1, "the item it failed, on the error port")
		assert.EqualError(t, out[engine.ErrorPort][0].Err, "no text in question")
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 1}}, out[engine.ErrorPort][0].Source)
	})

	t.Run("all of a run's items at once", func(t *testing.T) {
		out, err := run(t, r, "plugin.echo.count", nil, items(map[string]any{}, map[string]any{}, map[string]any{}))
		require.NoError(t, err)
		require.Len(t, out[engine.DefaultPort], 1)
		assert.Equal(t, json.Number("3"), out[engine.DefaultPort][0].JSON["count"])
		assert.Len(t, out[engine.DefaultPort][0].Source, 3)
	})

	t.Run("its process sees none of the server's environment", func(t *testing.T) {
		out, err := run(t, r, "plugin.echo.env", nil, items(map[string]any{}))
		require.NoError(t, err)

		got := out[engine.DefaultPort][0].JSON
		assert.Empty(t, got["secret"])
		assert.NotContains(t, got["names"], "POLYTHOUGHT_SECRET_KEYS")
	})

	t.Run("what it gets wrong fails the node, with why", func(t *testing.T) {
		for how, want := range map[string]string{
			"fail":   "plugin echo: told to fail",
			"port":   `plugin echo sent an item out of "nowhere", which the node has not`,
			"source": "plugin echo sent an item from main 9, which it was not given",
		} {
			_, err := run(t, r, "plugin.echo.misbehave", map[string]any{"how": how}, items(map[string]any{}))
			assert.EqualError(t, err, want, how)
		}
	})
}

func TestCrash(t *testing.T) {
	p, r := start(t)

	_, err := run(t, r, "plugin.echo.misbehave", map[string]any{"how": "crash"}, items(map[string]any{}))
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "plugin echo: "), err.Error())

	require.Eventually(t, p.Exited, 2*time.Second, 10*time.Millisecond, "the process is gone, the server not")

	_, err = run(t, r, "plugin.echo.shout", nil, items(map[string]any{"text": "a"}))
	assert.EqualError(t, err, "plugin echo is not running: its process exited", "its nodes fail while nothing starts it again")
}

func TestSupervise(t *testing.T) {
	p, r := start(t)

	crashed := make(chan error, 1)

	var checks atomic.Int32

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p.Supervise(ctx, pluginhost.Restarts{First: 10 * time.Millisecond, Max: 40 * time.Millisecond, Crashes: 3, Within: time.Minute, Every: 5 * time.Millisecond},
		pluginhost.Watch{
			Check:   func() error { checks.Add(1); return nil },
			Crashed: func(err error) { crashed <- err },
		})

	crash := func() {
		t.Helper()
		_, _ = run(t, r, "plugin.echo.misbehave", map[string]any{"how": "crash"}, items(map[string]any{}))
	}

	running := func() bool {
		_, err := run(t, r, "plugin.echo.shout", nil, items(map[string]any{"text": "a"}))
		return err == nil
	}

	t.Run("started again after a crash, its files checked first", func(t *testing.T) {
		crash()
		require.Eventually(t, running, 5*time.Second, 20*time.Millisecond)

		st := p.Status()
		assert.Equal(t, pluginhost.Running, st.State)
		assert.Equal(t, 1, st.Crashes)
		assert.NotEmpty(t, st.LastError)
		assert.NotEmpty(t, st.Log, "its last lines, kept")
		assert.Equal(t, int32(1), checks.Load())
	})

	t.Run("stopped as crashed, once too often, until an admin starts it", func(t *testing.T) {
		crash()
		require.Eventually(t, running, 5*time.Second, 20*time.Millisecond)
		crash()

		select {
		case err := <-crashed:
			assert.Error(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("not stopped as crashed")
		}

		assert.Equal(t, pluginhost.Crashed, p.Status().State)
		assert.False(t, running())

		require.NoError(t, p.Start())
		require.Eventually(t, running, 5*time.Second, 20*time.Millisecond)
		assert.Zero(t, p.Status().Crashes, "its crashes forgotten")

		assert.Error(t, p.Start(), "only a crashed one")
	})
}

func TestTrigger(t *testing.T) {
	_, r := start(t)

	tr, ok := r.Trigger("plugin.echo.tick")
	require.True(t, ok)

	t.Run("an execution each event, answered once it is done", func(t *testing.T) {
		var got []map[string]any

		emit := func(_ context.Context, out engine.Ports) error {
			got = append(got, out[engine.DefaultPort][0].JSON)
			if len(got) == 2 {
				return fmt.Errorf("the second run failed")
			}

			return nil
		}

		err := tr.Start(context.Background(), engine.TriggerContext{
			WorkflowID: "wf-1", OwnerID: "alice", NodeID: "tick", Params: engine.Params{"times": 3}, Resumed: true,
		}, emit)
		require.NoError(t, err, "done after three")

		require.Len(t, got, 3)
		assert.Equal(t, json.Number("0"), got[0]["n"])
		assert.Equal(t, true, got[0]["resumed"])
		assert.Equal(t, "", got[1]["lastFailed"])
		assert.Equal(t, "the second run failed", got[2]["lastFailed"], "the plugin hears how each went")
	})

	t.Run("until the host stops it", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		var (
			mu sync.Mutex
			n  int
		)

		emit := func(context.Context, engine.Ports) error {
			mu.Lock()
			defer mu.Unlock()

			if n++; n == 5 {
				cancel()
			}

			return nil
		}

		done := make(chan error, 1)
		go func() { done <- tr.Start(ctx, engine.TriggerContext{Params: engine.Params{"times": 0}}, emit) }()

		select {
		case err := <-done:
			require.NoError(t, err, "stopped, quietly")
		case <-time.After(5 * time.Second):
			t.Fatal("the trigger did not stop")
		}
	})
}

func TestCredentialsAndScenarios(t *testing.T) {
	p, r := start(t)

	typ, ok := r.CredentialType("plugin.echo.token")
	require.True(t, ok)
	assert.Equal(t, "Echo token", typ.Label)
	assert.True(t, typ.Fields[0].Secret)

	require.NoError(t, typ.Check(map[string]string{"token": "tok_1"}))
	require.EqualError(t, typ.Check(map[string]string{"token": "nope"}), "a token starts with tok_")

	require.NotNil(t, typ.Test, "testable")
	require.NoError(t, typ.Test(context.Background(), map[string]string{"token": "tok_good"}))
	require.EqualError(t, typ.Test(context.Background(), map[string]string{"token": "tok_bad"}), "the echo service refused it")

	scenarios, err := p.Scenarios()
	require.NoError(t, err)
	require.Len(t, scenarios, 1)

	sc := scenarios[0]
	assert.Equal(t, domain.TemplateID("plugin.echo.demo"), sc.ID)
	assert.Equal(t, "Echo demo", sc.Name)
	require.Len(t, sc.Nodes, 2)
	assert.Equal(t, "plugin.echo.shout", sc.Nodes[1].Type)
	assert.Equal(t, "question", sc.Nodes[1].Params["field"])
	require.NoError(t, r.Validate(domain.Workflow{Nodes: sc.Nodes, Connections: sc.Connections}), "a workflow of its nodes")
}

func TestStartRefuses(t *testing.T) {
	_, err := pluginhost.Start(context.Background(), program(t, filepath.Join(t.TempDir(), "none")), sandboxed(pluginhost.Deps{}))
	require.Error(t, err, "no such program")

	// A program that is no plugin: it never says the handshake.
	_, err = pluginhost.Start(context.Background(), program(t, "/bin/echo"), sandboxed(pluginhost.Deps{}))
	require.Error(t, err)
}

// Where there is no sandbox, a plugin does not start, saying why, unless an admin allows
// it, which is asked each time (D338).
func TestNoSandbox(t *testing.T) {
	ctx := context.Background()
	why := fmt.Errorf("%w: only Linux has the sandbox", sandbox.ErrUnavailable)

	_, err := pluginhost.Start(ctx, program(t, echo), pluginhost.Deps{NoSandbox: why})
	require.ErrorIs(t, err, sandbox.ErrUnavailable)
	assert.ErrorContains(t, err, "only Linux has the sandbox, and unsandboxed plugins are not allowed")

	allowed := false
	deps := pluginhost.Deps{NoSandbox: why, Unsandboxed: func() bool { return allowed }}

	_, err = pluginhost.Start(ctx, program(t, echo), deps)
	require.ErrorIs(t, err, sandbox.ErrUnavailable)

	allowed = true

	p, err := pluginhost.Start(ctx, program(t, echo), deps)
	require.NoError(t, err, "allowed")
	p.Close()
}

// Its process writes its lasting directory and its temporary one, emptied as it stops;
// under the sandbox, nothing else (D339).
func TestDirectories(t *testing.T) {
	p, r := start(t)

	server := filepath.Join(t.TempDir(), "server.txt")
	require.NoError(t, os.WriteFile(server, []byte("the server's"), 0o600))

	out, err := run(t, r, "plugin.echo.disk", map[string]any{"outside": server, "dir": filepath.Dir(echo)}, items(map[string]any{}))
	require.NoError(t, err)

	got := out[engine.DefaultPort][0].JSON
	data, temp := got["data_dir"].(string), got["temp_dir"].(string)

	require.NotEmpty(t, data)
	assert.Equal(t, "ok", got["write_data"])
	assert.Equal(t, "ok", got["write_temp"])
	assert.FileExists(t, filepath.Join(temp, "scratch.txt"))

	if launcher != nil {
		assert.NotEqual(t, "ok", got["read_outside"], "the server's files")
		assert.NotEqual(t, "ok", got["write_own"], "its own program's directory")
		t.Logf("refused as: %s; %s", got["read_outside"], got["write_own"])
	} else {
		t.Log("no sandbox here: what it refuses is not tried")
	}

	p.Close()

	assert.NoDirExists(t, temp, "emptied as it stops")

	kept, err := os.ReadFile(filepath.Join(data, "kept.txt")) //nolint:gosec // the test's
	require.NoError(t, err, "its lasting directory outlives it")
	assert.Equal(t, "kept", string(kept))
}
