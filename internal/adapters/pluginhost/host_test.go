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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// echo and bare are the test plugins' programs, built once for the package.
var echo, bare string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pluginhost-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	echo, bare = filepath.Join(dir, "echo"), filepath.Join(dir, "bare")

	for path, src := range map[string]string{echo: "./testdata/echo", bare: "./testdata/bare"} {
		if out, err := exec.Command("go", "build", "-o", path, src).CombinedOutput(); err != nil {
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

// startWith starts the test plugin, serving it the host API from deps.
func startWith(t *testing.T, deps pluginhost.Deps) (*pluginhost.Plugin, *engine.Registry) {
	t.Helper()

	// The server's secrets, which the plugin's process must not see.
	t.Setenv("POLYTHOUGHT_SECRET_KEYS", "k1:server-secret")

	p, err := pluginhost.Start(context.Background(), echo, deps)
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
	assert.Error(t, err, "its nodes fail until it is started again (5c)")
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
	_, err := pluginhost.Start(context.Background(), filepath.Join(t.TempDir(), "none"), pluginhost.Deps{})
	require.Error(t, err, "no such program")

	// A program that is no plugin: it never says the handshake.
	_, err = pluginhost.Start(context.Background(), "/bin/echo", pluginhost.Deps{})
	require.Error(t, err)
}
