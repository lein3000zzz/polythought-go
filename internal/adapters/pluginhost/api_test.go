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
