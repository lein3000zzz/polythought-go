package pluginhost_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lein3000zzz/polythought-go/internal/adapters/pluginhost"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// server is the tests' web server, on 127.0.0.1, which the echo plugin may fetch from.
func server(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Seen", r.Header.Get("X-Echo"))
		_, _ = w.Write(append([]byte(r.Method), body...))
	})
	mux.HandleFunc("/big", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	})
	mux.HandleFunc("/hop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc("/away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:"+port(r.Host)+"/", http.StatusFound)
	})
	mux.HandleFunc("/slow", func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func port(host string) string {
	_, p, _ := strings.Cut(host, ":")
	return p
}

var limits = pluginhost.Limits{HostCalls: 3, FetchTime: 300 * time.Millisecond, FetchBody: 1024}

// fetched runs the fetch node once over an item, saying how its last fetch went.
func fetched(t *testing.T, r *engine.Registry, params map[string]any) map[string]any {
	t.Helper()

	out, err := run(t, r, "plugin.echo.fetch", params, items(map[string]any{}))
	require.NoError(t, err)
	require.Len(t, out[engine.DefaultPort], 1)

	return out[engine.DefaultPort][0].JSON
}

func TestFetch(t *testing.T) {
	srv := server(t)
	local := "http://localhost:" + port(strings.TrimPrefix(srv.URL, "http://"))

	_, r := startWith(t, pluginhost.Deps{AllowPrivate: func() bool { return true }, Limits: func() pluginhost.Limits { return limits }})

	t.Run("a host its permissions name", func(t *testing.T) {
		got := fetched(t, r, map[string]any{"url": srv.URL + "/"})
		assert.Equal(t, json.Number("200"), got["status"])
		assert.Equal(t, "GET", got["body"])
		assert.Equal(t, "hi", got["seen"], "its headers go, and the response's come back")

		got = fetched(t, r, map[string]any{"url": srv.URL + "/", "method": "POST"})
		assert.Equal(t, "POSTPOST", got["body"], "with its body")

		got = fetched(t, r, map[string]any{"url": srv.URL + "/hop"})
		assert.Equal(t, "GET", got["body"], "redirected on the same host")
		assert.Equal(t, "hi", got["seen"], "its headers kept there")
	})

	t.Run("refused what they do not name", func(t *testing.T) {
		got := fetched(t, r, map[string]any{"url": local + "/"})
		assert.Equal(t, true, got["notAllowed"], "the plugin can tell")
		assert.Equal(t, "fetch from localhost: its permissions do not name it (fetch:localhost, or fetch:*)", got["error"])

		got = fetched(t, r, map[string]any{"url": srv.URL + "/away"})
		assert.Equal(t, true, got["notAllowed"])
		assert.Contains(t, got["error"], "redirected to localhost: its permissions do not name it", "a redirect is checked too")
	})

	t.Run("held to the limits", func(t *testing.T) {
		got := fetched(t, r, map[string]any{"url": srv.URL + "/big"})
		assert.Equal(t, "the response from 127.0.0.1 is larger than the 1024 bytes a fetch may take", got["error"])

		got = fetched(t, r, map[string]any{"url": srv.URL + "/slow"})
		assert.Equal(t, "fetch from 127.0.0.1: no answer within 300ms", got["error"])

		got = fetched(t, r, map[string]any{"url": srv.URL + "/", "method": "TRACE"})
		assert.Contains(t, got["error"], `method "TRACE"`)

		got = fetched(t, r, map[string]any{"url": "file:///etc/passwd"})
		assert.Equal(t, "the URL is not an http or https address", got["error"])
	})

	t.Run("a run asking past its calls fails, whatever the plugin makes of it", func(t *testing.T) {
		got := fetched(t, r, map[string]any{"url": srv.URL + "/", "times": 3})
		assert.Equal(t, "GET", got["body"], "three, the limit")

		_, err := run(t, r, "plugin.echo.fetch", map[string]any{"url": srv.URL + "/", "times": 5}, items(map[string]any{}))
		require.EqualError(t, err, "plugin echo asked the host more than the 3 times a run may")
	})

	t.Run("never inside the server's network unless an admin allows it", func(t *testing.T) {
		_, guarded := startWith(t, pluginhost.Deps{})

		got := fetched(t, guarded, map[string]any{"url": srv.URL + "/"})
		assert.Equal(t, true, got["notAllowed"])
		assert.Contains(t, got["error"], "address inside the server's network")
	})
}

// perItem gives each item its own settings, as expressions would.
type perItem []engine.Params

func (p perItem) Resolve(src engine.ItemRef) (engine.Params, error) { return p[src.Index], nil }

func TestHostCredentials(t *testing.T) {
	creds := &credentials{}
	_, r := startWith(t, pluginhost.Deps{Credentials: creds})

	keyed, _ := r.Node("plugin.echo.keyed")

	t.Run("the one each item's setting names", func(t *testing.T) {
		out, err := runWith(context.Background(), keyed, perItem{
			{"key": "cred-1", "which": "key"}, {"key": "cred-2", "which": "key"}, {"key": "cred-h", "which": "key"},
		}, items(map[string]any{}, map[string]any{}, map[string]any{}))
		require.NoError(t, err)
		require.Len(t, out[engine.DefaultPort], 3)

		got := out[engine.DefaultPort]
		assert.Equal(t, map[string]any{"type": "token", "token": "tok_1"}, got[0].JSON, "its own type, by its name")
		assert.Equal(t, "tok_2", got[1].JSON["token"])
		assert.Equal(t, "http.header", got[2].JSON["type"], "a built-in by its full name")

		assert.Equal(t, []string{"plugin.echo.token", "http.header"}, creds.asked[0], "of the types its setting takes")
	})

	t.Run("nothing else", func(t *testing.T) {
		for params, want := range map[[2]string]string{
			{"cred-1", "which"}: `"which" is none of the node's credential settings`,
			{"", "key"}:         `no credential is picked in "key"`,
			{"gone", "key"}:     `the credential in "key" is gone`,
		} {
			out, err := run(t, r, "plugin.echo.keyed", map[string]any{"key": params[0], "which": params[1]}, items(map[string]any{}))
			require.NoError(t, err)
			require.Len(t, out[engine.ErrorPort], 1, want)
			assert.EqualError(t, out[engine.ErrorPort][0].Err, want)
		}
	})

	t.Run("a trigger's own", func(t *testing.T) {
		tr, _ := r.Trigger("plugin.echo.tick")

		var tokens []any

		err := tr.Start(context.Background(), engine.TriggerContext{
			WorkflowID: "wf-1", OwnerID: "alice", NodeID: "tick", Params: engine.Params{"times": 2, "key": "cred-2"},
		}, func(_ context.Context, out engine.Ports) error {
			tokens = append(tokens, out[engine.DefaultPort][0].JSON["token"])
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, []any{"tok_2", "tok_2"}, tokens)
	})

	t.Run("none where there is no vault", func(t *testing.T) {
		_, bare := start(t)

		out, err := run(t, bare, "plugin.echo.keyed", map[string]any{"key": "cred-1"}, items(map[string]any{}))
		require.NoError(t, err)
		assert.EqualError(t, out[engine.ErrorPort][0].Err, "credentials are not available here")
	})
}

func TestSessions(t *testing.T) {
	_, r := startWith(t, pluginhost.Deps{Credentials: &credentials{}})

	_, err := run(t, r, "plugin.echo.remember", nil, items(map[string]any{}))
	require.NoError(t, err)

	out, err := run(t, r, "plugin.echo.late", nil, items(map[string]any{}))
	require.NoError(t, err)

	got := out[engine.DefaultPort][0].JSON
	assert.Equal(t, true, got["notAllowed"], "a run's session ends with it")
	assert.Equal(t, "no run of the plugin's has that session: it has ended, or never was", got["error"])
}

func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	logs := &recordedLogs{}
	m := newRecordedMetrics()

	_, r := startWith(t, pluginhost.Deps{
		Util: observability.NewUtility(logging.New(logCore{logs: logs}), m, oteltracing.NewOtelManager(oteltracing.NewOtelSpanStarter(tp))),
	})

	ctx, node := tp.Tracer("test").Start(context.Background(), "node")

	chatty, _ := r.Node("plugin.echo.chatty")
	out, err := runWith(ctx, chatty, engine.LiteralParams(chatty.Type(), nil), items(map[string]any{}))
	require.NoError(t, err)

	node.End()

	t.Run("metrics it declares, as it declares them", func(t *testing.T) {
		assert.Equal(t, map[string]any{
			"unknown":  `the manifest declares no metric "whispers"`,
			"labels":   "shouts_total takes the labels [case]",
			"negative": "shouts_total is a counter: it only goes up",
		}, out[engine.DefaultPort][0].JSON["refused"])

		def, points := m.of("watchers_plugin_echo_shouts_total")
		assert.Equal(t, metrics.Labels{"plugin": "echo"}, def.ConstLabels, "two plugins' never mix")
		assert.Equal(t, []point{{value: 2, labels: metrics.Labels{"case": "upper"}}}, points)

		_, points = m.of("watchers_plugin_echo_loudness")
		assert.Equal(t, []point{{value: 5}}, points)
	})

	t.Run("spans under the node's", func(t *testing.T) {
		byName := map[string]sdktrace.ReadOnlySpan{}
		for _, s := range spans.Ended() {
			byName[s.Name()] = s
		}

		think, harder := byName["think"], byName["think harder"]
		require.NotNil(t, think)
		require.NotNil(t, harder)

		assert.Equal(t, node.SpanContext().SpanID(), think.Parent().SpanID())
		assert.Equal(t, think.SpanContext().SpanID(), harder.Parent().SpanID(), "nested as the plugin nested them")
		assert.Equal(t, otelcodes.Error, harder.Status().Code)
		assert.Equal(t, "too hard", harder.Status().Description)

		attrs := map[string]string{}
		for _, a := range think.Attributes() {
			attrs[string(a.Key)] = a.Value.String()
		}

		assert.Equal(t, map[string]string{"plugin.id": "echo", "depth": "1"}, attrs)
	})

	t.Run("logs with its name and the run's, its fields apart", func(t *testing.T) {
		line, ok := logs.find("Thinking")
		require.True(t, ok)

		assert.Equal(t, logging.LevelInfo, line.level)
		assert.Equal(t, "echo", line.attrs["plugin"])
		assert.EqualValues(t, "wf-1", line.attrs["workflow_id"], "the run's, not what the plugin says")

		fields := map[string]any{}
		for _, a := range line.attrs["fields"].([]logging.Attr) {
			fields[a.Key] = a.Value
		}

		assert.Equal(t, map[string]any{"about": "rain", "odds.yes": "0.42", "workflow_id": "spoofed"}, fields)
	})
}

// kept runs the keep node once, its settings these.
func kept(t *testing.T, r *engine.Registry, name string, params map[string]any) map[string]any {
	t.Helper()

	out, err := run(t, r, name, params, items(map[string]any{}))
	require.NoError(t, err)
	require.Len(t, out[engine.DefaultPort], 1)

	return out[engine.DefaultPort][0].JSON
}

func TestStore(t *testing.T) {
	st := newStore()
	_, r := startWith(t, pluginhost.Deps{Store: st, Limits: func() pluginhost.Limits { return pluginhost.Limits{StoreBytes: 64} }})

	typ, _, _ := r.Type("plugin.echo.keep")
	assert.True(t, typ.Remembers, "its settings show what it keeps, as node memory")

	t.Run("its own, by key", func(t *testing.T) {
		assert.Empty(t, kept(t, r, "plugin.echo.keep", map[string]any{"op": "set", "key": "seen:a", "value": "hello"})["error"])
		kept(t, r, "plugin.echo.keep", map[string]any{"op": "set", "key": "seen:b", "value": "x"})
		kept(t, r, "plugin.echo.keep", map[string]any{"op": "set", "key": "cursor", "value": "7"})

		got := kept(t, r, "plugin.echo.keep", map[string]any{"op": "get", "key": "seen:a"})
		assert.Equal(t, true, got["found"])
		assert.Equal(t, map[string]any{"text": "hello"}, got["value"])

		assert.Equal(t, "seen:a,seen:b", kept(t, r, "plugin.echo.keep", map[string]any{"op": "keys", "key": "seen:"})["keys"])

		kept(t, r, "plugin.echo.keep", map[string]any{"op": "delete", "key": "seen:a"})
		assert.Equal(t, false, kept(t, r, "plugin.echo.keep", map[string]any{"op": "get", "key": "seen:a"})["found"])

		_, ok := st.values[ports.PluginNode{Workflow: "wf-1", Node: "n-1", Plugin: "echo"}]
		assert.True(t, ok, "the node's in its workflow, as the plugin's")
	})

	t.Run("within its size", func(t *testing.T) {
		got := kept(t, r, "plugin.echo.keep", map[string]any{"op": "set", "key": "big", "value": strings.Repeat("x", 64)})
		assert.Equal(t, true, got["limit"])
		assert.Equal(t, "the node's store is full: its keys and values may take 64 bytes", got["error"])

		got = kept(t, r, "plugin.echo.keep", map[string]any{"op": "set", "key": strings.Repeat("k", 257), "value": "x"})
		assert.Equal(t, "a key: 1 to 256 bytes", got["error"])
	})

	t.Run("none for a node not marked remembers", func(t *testing.T) {
		got := kept(t, r, "plugin.echo.forgetful", nil)
		assert.Equal(t, true, got["notAllowed"])
		assert.Equal(t, "plugin.echo.forgetful keeps nothing: the manifest marks remembers the nodes that keep data", got["error"])
	})

	t.Run("none where there is no store", func(t *testing.T) {
		_, bare := start(t)
		assert.Equal(t, "the store is not available here", kept(t, bare, "plugin.echo.keep", map[string]any{"op": "get", "key": "a"})["error"])
	})
}

func TestData(t *testing.T) {
	d := &data{}
	_, r := startWith(t, pluginhost.Deps{Data: pluginhost.Data{Events: d, Books: d, History: d, Trades: d, Positions: d, Wallets: d}})

	read := func(what, arg string) map[string]any {
		t.Helper()
		return kept(t, r, "plugin.echo.read", map[string]any{"what": what, "arg": arg})
	}

	t.Run("markets, typed, decimals exact", func(t *testing.T) {
		got := read("search", "rain")["got"].([]any)
		require.Len(t, got, 1, "open ones only")

		m := got[0].(map[string]any)
		assert.Equal(t, "Will it rain Monday?", m["Question"])
		assert.Equal(t, "1234.5678", m["Volume24h"])
		assert.Equal(t, "Rain in Paris?", m["Event"])
		assert.Equal(t, "2026-10-24T00:00:00Z", m["EndsAt"])
		assert.Equal(t, "0.42", m["Outcomes"].([]any)[0].(map[string]any)["Price"])

		assert.Equal(t, "a query: say what to look for", read("search", " ")["error"])
	})

	t.Run("an order book", func(t *testing.T) {
		book := read("book", "tok-yes")["got"].(map[string]any)
		assert.Equal(t, "0.41", book["Bids"].([]any)[0].(map[string]any)["Price"])
		assert.Equal(t, "0.43", book["Asks"].([]any)[0].(map[string]any)["Price"])

		assert.Equal(t, "no order book for token tok-none", read("book", "tok-none")["error"])
	})

	t.Run("price history, oldest first", func(t *testing.T) {
		points := read("history", "tok-yes")["got"].([]any)
		require.Len(t, points, 3)
		assert.Equal(t, "0.4", points[0].(map[string]any)["Price"])
		assert.Equal(t, "0.44", points[2].(map[string]any)["Price"])

		assert.Equal(t, time.Minute, d.history.Bucket, "an hour in 1000 points: a minute at least")
		assert.Equal(t, 1000, d.history.Limit)
	})

	t.Run("trades and positions, anyone's", func(t *testing.T) {
		trades := read("trades", "0xm1")["got"].([]any)
		require.Len(t, trades, 1)
		assert.Equal(t, "buy", trades[0].(map[string]any)["Side"])
		assert.Equal(t, 1, d.trades.Limit)

		positions := read("positions", string(trader))["got"].([]any)
		require.Len(t, positions, 1)
		assert.Equal(t, "10.5", positions[0].(map[string]any)["Value"])

		assert.Contains(t, read("positions", "nope")["error"], "the address: ")
	})

	t.Run("the owner's wallets, with the permission", func(t *testing.T) {
		wallets := read("wallets", "")["got"].([]any)
		assert.Equal(t, []any{map[string]any{"ID": "w-1", "Name": "Main", "Address": string(aliceW)}}, wallets)
	})

	t.Run("none where there is nothing to read", func(t *testing.T) {
		_, none := start(t)

		got := kept(t, none, "plugin.echo.read", map[string]any{"what": "search", "arg": "rain"})
		assert.Equal(t, "this is not available here", got["error"])
	})
}

func TestNoPermissions(t *testing.T) {
	p, err := pluginhost.Start(context.Background(), bare, pluginhost.Deps{Data: pluginhost.Data{Wallets: &data{}}})
	require.NoError(t, err)
	t.Cleanup(p.Close)

	r := engine.NewRegistry()
	require.NoError(t, p.Register(r))

	got := kept(t, r, "plugin.bare.try", nil)

	for name, want := range map[string]string{
		"wallets": "the owner's wallets: its permissions do not name them (wallets)",
		"fetch":   "fetch from example.com: its permissions do not name it (fetch:example.com, or fetch:*)",
		"message": "telegram: its permissions do not name it (telegram)",
		"ask":     "telegram: its permissions do not name it (telegram)",
		"orders":  "orders: its permissions do not name them (orders)",
	} {
		assert.Equal(t, true, got[name+"Refused"], name)
		assert.Equal(t, want, got[name], name)
	}
}
