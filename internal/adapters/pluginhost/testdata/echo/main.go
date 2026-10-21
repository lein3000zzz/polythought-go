// Echo is the host's test plugin: a node of each shape, a trigger, a credential type and
// a scenario, a node that misbehaves, and nodes that ask the host for what they may and
// may not.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

var manifest = plugin.Manifest{
	ID: "echo", Version: "1.2.0", Name: "Echo", Description: "Says it back.",
	// The tests' server; localhost, the same address, is not named.
	Permissions: []string{"fetch:127.0.0.1", "wallets"},
	Metrics: []plugin.Metric{
		{Name: "shouts_total", Kind: "counter", Help: "Shouts, by case.", Labels: []string{"case"}},
		{Name: "loudness", Kind: "histogram", Help: "How loud.", Buckets: []float64{1, 10}},
	},
	Nodes: []plugin.NodeType{
		{
			Name: "shout", Label: "Shout", Outputs: []string{"main"},
			Params: []plugin.Param{{Name: "field", Kind: "string", Default: "text"}},
			Items:  &plugin.Items{Passed: true, Fields: []plugin.Field{{Name: "shouted", Kind: "text"}}},
		},
		{Name: "count", Label: "Count", Items: &plugin.Items{Fields: []plugin.Field{{Name: "count", Kind: "number"}}}},
		{Name: "env", Label: "Environment"},
		{Name: "misbehave", Label: "Misbehave", Params: []plugin.Param{{Name: "how", Kind: "string"}}},
		{
			Name: "keyed", Label: "Keyed",
			Params: []plugin.Param{
				{Name: "key", Kind: "credential", CredentialTypes: []string{"token", "http.header"}},
				{Name: "which", Kind: "string", Default: "key"}, // the setting it reads
			},
		},
		{
			Name: "fetch", Label: "Fetch",
			Params: []plugin.Param{{Name: "url", Kind: "string"}, {Name: "method", Kind: "string"}, {Name: "times", Kind: "number", Default: 1}},
		},
		{Name: "chatty", Label: "Chatty"},
		{Name: "remember", Label: "Remember"},
		{Name: "late", Label: "Late"},
		{
			Name: "keep", Label: "Keep", Remembers: true,
			Params: []plugin.Param{{Name: "op", Kind: "string"}, {Name: "key", Kind: "string"}, {Name: "value", Kind: "string"}},
		},
		{Name: "forgetful", Label: "Forgetful"},
		{Name: "read", Label: "Read", Params: []plugin.Param{{Name: "what", Kind: "string"}, {Name: "arg", Kind: "string"}}},
	},
	Triggers: []plugin.NodeType{
		{Name: "tick", Label: "Tick", Params: []plugin.Param{
			{Name: "times", Kind: "number", Default: 3},
			{Name: "key", Kind: "credential", CredentialTypes: []string{"token"}},
		}},
	},
	CredentialTypes: []plugin.CredentialType{{
		Name: "token", Label: "Echo token", Testable: true,
		Fields: []plugin.CredentialField{{Name: "token", Label: "Token", Required: true, Secret: true}},
	}},
	Scenarios: []plugin.Scenario{{
		ID: "demo", Name: "Echo demo", Summary: "Shouts what comes in.",
		Nodes:       json.RawMessage(`[{"id":"manual","type":"trigger.manual","position":{"x":0,"y":0}},{"id":"shout","type":"plugin.echo.shout","params":{"field":"question"},"position":{"x":300,"y":0}}]`),
		Connections: json.RawMessage(`[{"from":"manual","fromPort":"main","to":"shout","toPort":"main"}]`),
	}},
}

func main() {
	plugin.Serve(plugin.Plugin{
		Manifest: manifest,
		Nodes: map[string]plugin.Node{
			"shout":     plugin.PerItem(shout),
			"count":     plugin.NodeFunc(count),
			"env":       plugin.PerItem(env),
			"misbehave": plugin.NodeFunc(misbehave),
			"keyed":     plugin.PerItem(keyed),
			"fetch":     plugin.NodeFunc(fetch),
			"chatty":    plugin.PerItem(chatty),
			"remember":  plugin.PerItem(remember),
			"late":      plugin.PerItem(late),
			"keep":      plugin.PerItem(keep),
			"forgetful": plugin.PerItem(forgetful),
			"read":      plugin.PerItem(read),
		},
		Triggers: map[string]plugin.Trigger{"tick": tick{}},
		Credentials: map[string]plugin.CredentialChecks{"token": {
			Check: func(values map[string]string) error {
				if !strings.HasPrefix(values["token"], "tok_") {
					return errors.New("a token starts with tok_")
				}

				return nil
			},
			Test: func(_ context.Context, values map[string]string) error {
				if values["token"] != "tok_good" {
					return errors.New("the echo service refused it")
				}

				return nil
			},
		}},
	})
}

func shout(_ context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	field, _ := item.Params["field"].(string)

	text, ok := item.JSON[field].(string)
	if !ok {
		return "", nil, fmt.Errorf("no text in %s", field)
	}

	out := map[string]any{"shouted": strings.ToUpper(text), "owner": run.OwnerID}
	for k, v := range item.JSON {
		out[k] = v
	}

	return "", out, nil
}

func count(_ context.Context, _ plugin.Run, in plugin.Ports) (plugin.Ports, error) {
	var sources []plugin.ItemRef
	for i := range in[plugin.MainPort] {
		sources = append(sources, plugin.ItemRef{Port: plugin.MainPort, Index: i})
	}

	out := plugin.Ports{}
	out.Add(plugin.MainPort, plugin.Item{JSON: map[string]any{"count": len(in[plugin.MainPort])}, Sources: sources})

	return out, nil
}

// env is what the plugin's process can see of the server's environment: nothing.
func env(context.Context, plugin.Run, plugin.Item) (string, map[string]any, error) {
	var names []string
	for _, kv := range os.Environ() {
		names = append(names, strings.SplitN(kv, "=", 2)[0])
	}

	return "", map[string]any{"names": strings.Join(names, ","), "secret": os.Getenv("POLYTHOUGHT_SECRET_KEYS")}, nil
}

// keyed reads the credential in the setting which names.
func keyed(ctx context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	which, _ := item.Params["which"].(string)

	c, err := run.Host.Credential(ctx, which, item)
	if err != nil {
		return "", nil, err
	}

	return "", map[string]any{"type": c.Type, "token": c.Values["token"]}, nil
}

// fetch fetches the first item's url times over, and says how the last went: what came
// back, or the error, as the SDK tells it.
func fetch(ctx context.Context, run plugin.Run, in plugin.Ports) (plugin.Ports, error) {
	item := in[plugin.MainPort][0]

	url, _ := item.Params["url"].(string)
	method, _ := item.Params["method"].(string)
	times := int64(1)
	if n, ok := item.Params["times"].(json.Number); ok {
		times, _ = n.Int64()
	}

	out := map[string]any{}

	for range times {
		res, err := run.Host.Fetch(ctx, plugin.Request{
			Method: method, URL: url, Header: http.Header{"X-Echo": {"hi"}}, Body: []byte(method),
		})
		if err != nil {
			out = map[string]any{
				"error": err.Error(), "notAllowed": errors.Is(err, plugin.ErrNotAllowed), "limit": errors.Is(err, plugin.ErrLimit),
			}

			continue
		}

		out = map[string]any{"status": res.Status, "body": string(res.Body), "seen": res.Header.Get("X-Seen")}
	}

	return plugin.Ports{plugin.MainPort: {{JSON: out, Sources: []plugin.ItemRef{{Port: plugin.MainPort}}}}}, nil
}

// chatty logs, counts and traces, and says what the host refused.
func chatty(ctx context.Context, run plugin.Run, _ plugin.Item) (string, map[string]any, error) {
	ctx, end := run.Host.Span(ctx, "think", map[string]string{"depth": "1"})

	inner, endInner := run.Host.Span(ctx, "think harder", nil)
	run.Log.InfoContext(inner, "Thinking", "about", "rain", slog.Group("odds", "yes", 0.42), "workflow_id", "spoofed")
	endInner(errors.New("too hard"))

	end(nil)

	refused := map[string]any{}
	for name, err := range map[string]error{
		"counted":  run.Host.Metric(ctx, "shouts_total", 2, map[string]string{"case": "upper"}),
		"observed": run.Host.Metric(ctx, "loudness", 5, nil),
		"unknown":  run.Host.Metric(ctx, "whispers", 1, nil),
		"labels":   run.Host.Metric(ctx, "shouts_total", 1, map[string]string{"tone": "low"}),
		"negative": run.Host.Metric(ctx, "shouts_total", -1, map[string]string{"case": "upper"}),
	} {
		if err != nil {
			refused[name] = err.Error()
		}
	}

	return "", map[string]any{"refused": refused}, nil
}

var (
	mu   sync.Mutex
	kept plugin.Host
)

// remember keeps its run's Host, for late to use once the run is over.
func remember(_ context.Context, run plugin.Run, _ plugin.Item) (string, map[string]any, error) {
	mu.Lock()
	defer mu.Unlock()

	kept = run.Host

	return "", map[string]any{}, nil
}

func late(ctx context.Context, _ plugin.Run, item plugin.Item) (string, map[string]any, error) {
	mu.Lock()
	host := kept
	mu.Unlock()

	_, err := host.Credential(ctx, "key", item)

	return "", map[string]any{"error": fmt.Sprint(err), "notAllowed": errors.Is(err, plugin.ErrNotAllowed)}, nil
}

// keep does op with the node's store: set, get, delete or keys.
func keep(ctx context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	op, _ := item.Params["op"].(string)
	key, _ := item.Params["key"].(string)
	value, _ := item.Params["value"].(string)

	var (
		out = map[string]any{}
		err error
	)

	switch op {
	case "set":
		err = run.Host.Set(ctx, key, map[string]any{"text": value})
	case "get":
		var v map[string]any
		out["found"], err = run.Host.Get(ctx, key, &v)
		out["value"] = v
	case "delete":
		err = run.Host.Delete(ctx, key)
	case "keys":
		var keys []string
		keys, err = run.Host.Keys(ctx, key, "", 0)
		out["keys"] = strings.Join(keys, ",")
	}

	return "", withError(out, err), nil
}

// forgetful may keep nothing: its manifest does not mark it.
func forgetful(ctx context.Context, run plugin.Run, _ plugin.Item) (string, map[string]any, error) {
	return "", withError(map[string]any{}, run.Host.Set(ctx, "k", 1)), nil
}

// read reads what says, with arg, and gives it back as JSON would have it.
func read(ctx context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	what, _ := item.Params["what"].(string)
	arg, _ := item.Params["arg"].(string)

	var (
		got any
		err error
	)

	switch what {
	case "search":
		got, err = run.Host.SearchMarkets(ctx, arg, false, 0)
	case "book":
		got, err = run.Host.OrderBook(ctx, arg)
	case "history":
		got, err = run.Host.PriceHistory(ctx, plugin.PriceQuery{
			TokenID: arg, Since: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC),
		})
	case "trades":
		got, err = run.Host.Trades(ctx, plugin.TradeQuery{MarketID: arg, Limit: 1})
	case "positions":
		got, err = run.Host.Positions(ctx, arg, 0)
	case "wallets":
		got, err = run.Host.Wallets(ctx)
	}

	out := map[string]any{}

	if err == nil {
		raw, _ := json.Marshal(got)

		var back any
		_ = json.Unmarshal(raw, &back)
		out["got"] = back
	}

	return "", withError(out, err), nil
}

// withError adds how err went to out, as the SDK tells it.
func withError(out map[string]any, err error) map[string]any {
	if err != nil {
		out["error"] = err.Error()
		out["notAllowed"] = errors.Is(err, plugin.ErrNotAllowed)
		out["limit"] = errors.Is(err, plugin.ErrLimit)
	}

	return out
}

func misbehave(_ context.Context, _ plugin.Run, in plugin.Ports) (plugin.Ports, error) {
	how := ""
	if items := in[plugin.MainPort]; len(items) > 0 {
		how, _ = items[0].Params["how"].(string)
	}

	out := plugin.Ports{}

	switch how {
	case "fail":
		return nil, errors.New("told to fail")
	case "port":
		out.Add("nowhere", plugin.Item{JSON: map[string]any{}})
	case "source":
		out.Add(plugin.MainPort, plugin.Item{JSON: map[string]any{}, Sources: []plugin.ItemRef{{Port: plugin.MainPort, Index: 9}}})
	case "crash":
		os.Exit(3)
	}

	return out, nil
}

// tick emits times events, 0 for until it is stopped, each saying its turn and whether the
// last one's execution failed.
type tick struct{}

func (tick) Start(ctx context.Context, run plugin.Run, params map[string]any, resumed bool, emit plugin.Emit) error {
	times := 3
	if n, ok := params["times"].(json.Number); ok {
		v, _ := n.Int64()
		times = int(v)
	}

	// With a credential picked, each event says its token: a trigger's settings are its
	// own.
	token := ""
	if params["key"] != nil {
		c, err := run.Host.Credential(ctx, "key", plugin.Item{})
		if err != nil {
			return err
		}

		token = c.Values["token"]
	}

	last := ""

	for i := 0; times == 0 || i < times; i++ {
		err := emit(ctx, plugin.Ports{plugin.MainPort: {{JSON: map[string]any{"n": i, "resumed": resumed, "lastFailed": last, "token": token}}}})
		if ctx.Err() != nil {
			return nil
		}

		last = ""
		if err != nil {
			last = err.Error()
		}
	}

	return nil
}
